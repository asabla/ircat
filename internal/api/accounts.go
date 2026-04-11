package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
)

// accountRecord is the JSON shape used by the accounts endpoints.
// PasswordHash is never returned to the client.
type accountRecord struct {
	ID        string   `json:"id"`
	Username  string   `json:"username"`
	Email     string   `json:"email,omitempty"`
	Verified  bool     `json:"verified"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Nicks     []string `json:"nicks,omitempty"`
}

type createAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

func (a *API) toAccountRecord(ctx context.Context, acct storage.Account) accountRecord {
	rec := accountRecord{
		ID:        acct.ID,
		Username:  acct.Username,
		Email:     acct.Email,
		Verified:  acct.Verified,
		CreatedAt: acct.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: acct.UpdatedAt.UTC().Format(rfc3339Nano),
	}
	owners, err := a.store.NickOwners().ListByAccount(ctx, acct.ID)
	if err == nil {
		for _, no := range owners {
			rec.Nicks = append(rec.Nicks, no.Nick)
		}
		sort.Strings(rec.Nicks)
	}
	return rec
}

func (a *API) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accts, err := a.store.Accounts().List(r.Context())
	if err != nil {
		a.logger.Warn("accounts.List failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "list accounts")
		return
	}
	out := make([]accountRecord, 0, len(accts))
	for _, acct := range accts {
		out = append(out, a.toAccountRecord(r.Context(), acct))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (a *API) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := a.store.Accounts().GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "account does not exist")
			return
		}
		a.logger.Warn("accounts.GetByID failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get account")
		return
	}
	writeJSON(w, http.StatusOK, a.toAccountRecord(r.Context(), *acct))
}

func (a *API) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "username and password are required")
		return
	}
	hash, err := auth.Hash(auth.AlgorithmArgon2id, req.Password, auth.DefaultArgon2idParams())
	if err != nil {
		a.logger.Warn("hash failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "hash password")
		return
	}
	acct := &storage.Account{
		ID:           req.Username, // match NickServ convention
		Username:     req.Username,
		PasswordHash: hash,
		Email:        req.Email,
	}
	if err := a.store.Accounts().Create(r.Context(), acct); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "account already exists")
			return
		}
		a.logger.Warn("accounts.Create failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "create account")
		return
	}
	writeJSON(w, http.StatusCreated, a.toAccountRecord(r.Context(), *acct))
}

func (a *API) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := a.store.Accounts().GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "account does not exist")
			return
		}
		a.logger.Warn("accounts.GetByID failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get account")
		return
	}
	// ON DELETE CASCADE on nick_owners removes linked nicks.
	if err := a.store.Accounts().Delete(r.Context(), acct.Username); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "account does not exist")
			return
		}
		a.logger.Warn("accounts.Delete failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleResetAccountPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "password is required")
		return
	}
	acct, err := a.store.Accounts().GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "account does not exist")
			return
		}
		a.logger.Warn("accounts.GetByID failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get account")
		return
	}
	hash, err := auth.Hash(auth.AlgorithmArgon2id, req.Password, auth.DefaultArgon2idParams())
	if err != nil {
		a.logger.Warn("hash failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "hash password")
		return
	}
	acct.PasswordHash = hash
	if err := a.store.Accounts().Update(r.Context(), acct); err != nil {
		a.logger.Warn("accounts.Update failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "update account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
