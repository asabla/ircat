package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
)

// tokenRecord is the JSON shape returned for an API token. The
// plaintext token itself is only ever returned by the create
// endpoint, and only once — see createTokenResponse.
type tokenRecord struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at,omitempty"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
}

type createTokenRequest struct {
	Label  string   `json:"label"`
	Scopes []string `json:"scopes"`
}

// createTokenResponse is the only place a plaintext token is exposed
// to the client. The dashboard / curl operator must capture it
// immediately; it is never retrievable from the store afterwards.
type createTokenResponse struct {
	tokenRecord
	Plaintext string `json:"plaintext"`
}

func (a *API) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := a.store.APITokens().List(r.Context())
	if err != nil {
		a.logger.Warn("tokens.List failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "list tokens")
		return
	}
	out := make([]tokenRecord, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toTokenRecord(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (a *API) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "label is required")
		return
	}
	minted, err := auth.GenerateAPIToken()
	if err != nil {
		a.logger.Warn("token mint failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "mint token")
		return
	}
	tok := &storage.APIToken{
		ID:     minted.ID,
		Label:  req.Label,
		Hash:   minted.Hash,
		Scopes: req.Scopes,
	}
	if err := a.store.APITokens().Create(r.Context(), tok); err != nil {
		a.logger.Warn("tokens.Create failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "create token")
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResponse{
		tokenRecord: toTokenRecord(*tok),
		Plaintext:   minted.Plaintext,
	})
}

func (a *API) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.store.APITokens().Delete(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "token does not exist")
			return
		}
		a.logger.Warn("tokens.Delete failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toTokenRecord(t storage.APIToken) tokenRecord {
	rec := tokenRecord{
		ID:        t.ID,
		Label:     t.Label,
		Scopes:    t.Scopes,
		CreatedAt: t.CreatedAt.UTC().Format(rfc3339Nano),
	}
	if !t.LastUsedAt.IsZero() {
		rec.LastUsedAt = t.LastUsedAt.UTC().Format(rfc3339Nano)
	}
	return rec
}
