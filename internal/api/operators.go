package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
)

// operatorRecord is the JSON shape used by the operators endpoints.
// PasswordHash is never returned to the client; it is write-only on
// the create form.
type operatorRecord struct {
	Name      string   `json:"name"`
	HostMask  string   `json:"host_mask"`
	Flags     []string `json:"flags"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type createOperatorRequest struct {
	Name     string   `json:"name"`
	Password string   `json:"password"`
	HostMask string   `json:"host_mask"`
	Flags    []string `json:"flags"`
}

func (a *API) handleListOperators(w http.ResponseWriter, r *http.Request) {
	ops, err := a.store.Operators().List(r.Context())
	if err != nil {
		a.logger.Warn("operators.List failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "list operators")
		return
	}
	out := make([]operatorRecord, 0, len(ops))
	for _, op := range ops {
		out = append(out, operatorRecord{
			Name:      op.Name,
			HostMask:  op.HostMask,
			Flags:     op.Flags,
			CreatedAt: op.CreatedAt.UTC().Format(rfc3339Nano),
			UpdatedAt: op.UpdatedAt.UTC().Format(rfc3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"operators": out})
}

func (a *API) handleGetOperator(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	op, err := a.store.Operators().Get(r.Context(), name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "operator does not exist")
			return
		}
		a.logger.Warn("operators.Get failed", "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get operator")
		return
	}
	writeJSON(w, http.StatusOK, operatorRecord{
		Name:      op.Name,
		HostMask:  op.HostMask,
		Flags:     op.Flags,
		CreatedAt: op.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: op.UpdatedAt.UTC().Format(rfc3339Nano),
	})
}

func (a *API) handleCreateOperator(w http.ResponseWriter, r *http.Request) {
	var req createOperatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Name == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name and password are required")
		return
	}
	hash, err := auth.Hash(auth.AlgorithmArgon2id, req.Password, auth.Argon2idParams{})
	if err != nil {
		a.logger.Warn("hash failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "hash password")
		return
	}
	op := &storage.Operator{
		Name:         req.Name,
		HostMask:     req.HostMask,
		PasswordHash: hash,
		Flags:        req.Flags,
	}
	if err := a.store.Operators().Create(r.Context(), op); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "operator already exists")
			return
		}
		a.logger.Warn("operators.Create failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "create operator")
		return
	}
	writeJSON(w, http.StatusCreated, operatorRecord{
		Name:      op.Name,
		HostMask:  op.HostMask,
		Flags:     op.Flags,
		CreatedAt: op.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt: op.UpdatedAt.UTC().Format(rfc3339Nano),
	})
}

func (a *API) handleDeleteOperator(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.store.Operators().Delete(r.Context(), name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "operator does not exist")
			return
		}
		a.logger.Warn("operators.Delete failed", "name", name, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete operator")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const rfc3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
