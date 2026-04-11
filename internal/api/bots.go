package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// BotManager is the small interface the api package uses for bot
// mutations. internal/bots.Supervisor implements it. We need the
// indirection so internal/api does not import internal/bots and so
// the CRUD path triggers the supervisor hot-reload rather than just
// touching the store.
type BotManager interface {
	CreateBot(ctx context.Context, bot *storage.Bot) error
	UpdateBot(ctx context.Context, bot *storage.Bot) error
	DeleteBot(ctx context.Context, id string) error
}

// BotValidator is the small hook the /bots/validate endpoint
// calls into. internal/bots.Validate satisfies it. Kept as a
// plain func type (not an interface) so callers can pass the
// package-level function directly without wrapping it.
type BotValidator func(source string) error

type botRecord struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	Enabled         bool   `json:"enabled"`
	TickIntervalSec int    `json:"tick_interval_seconds,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

func toBotRecord(b storage.Bot) botRecord {
	return botRecord{
		ID:              b.ID,
		Name:            b.Name,
		Source:          b.Source,
		Enabled:         b.Enabled,
		TickIntervalSec: int(b.TickInterval / time.Second),
		CreatedAt:       b.CreatedAt.UTC().Format(rfc3339Nano),
		UpdatedAt:       b.UpdatedAt.UTC().Format(rfc3339Nano),
	}
}

type createBotRequest struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Enabled         bool   `json:"enabled"`
	TickIntervalSec int    `json:"tick_interval_seconds"`
}

type updateBotRequest struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Enabled         bool   `json:"enabled"`
	TickIntervalSec int    `json:"tick_interval_seconds"`
}

func (a *API) handleListBots(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeError(w, http.StatusServiceUnavailable, "no_store", "storage not configured")
		return
	}
	bs, err := a.store.Bots().List(r.Context())
	if err != nil {
		a.logger.Warn("bots.List failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "list bots")
		return
	}
	out := make([]botRecord, 0, len(bs))
	for _, b := range bs {
		out = append(out, toBotRecord(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": out})
}

func (a *API) handleGetBot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := a.store.Bots().Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "bot does not exist")
			return
		}
		a.logger.Warn("bots.Get failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "get bot")
		return
	}
	writeJSON(w, http.StatusOK, toBotRecord(*b))
}

func (a *API) handleCreateBot(w http.ResponseWriter, r *http.Request) {
	if a.botManager == nil {
		writeError(w, http.StatusServiceUnavailable, "no_manager", "bot manager not configured")
		return
	}
	var req createBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.Name == "" || req.Source == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name and source are required")
		return
	}
	bot := &storage.Bot{
		Name:         req.Name,
		Source:       req.Source,
		Enabled:      req.Enabled,
		TickInterval: time.Duration(req.TickIntervalSec) * time.Second,
	}
	if err := a.botManager.CreateBot(r.Context(), bot); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "bot with that name already exists")
			return
		}
		a.logger.Warn("CreateBot failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toBotRecord(*bot))
}

func (a *API) handleUpdateBot(w http.ResponseWriter, r *http.Request) {
	if a.botManager == nil {
		writeError(w, http.StatusServiceUnavailable, "no_manager", "bot manager not configured")
		return
	}
	id := r.PathValue("id")
	var req updateBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	bot := &storage.Bot{
		ID:           id,
		Name:         req.Name,
		Source:       req.Source,
		Enabled:      req.Enabled,
		TickInterval: time.Duration(req.TickIntervalSec) * time.Second,
	}
	if err := a.botManager.UpdateBot(r.Context(), bot); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "bot does not exist")
			return
		}
		a.logger.Warn("UpdateBot failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toBotRecord(*bot))
}

// validateBotRequest is the body shape for POST /bots/validate.
type validateBotRequest struct {
	Source string `json:"source"`
}

// validateBotResponse is the success/failure envelope the
// /bots/validate endpoint returns. Both shapes share the same
// struct so the JSON client sees a uniform payload.
type validateBotResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// handleValidateBot compiles the supplied Lua source in a
// throwaway sandboxed lua.LState via the wired BotValidator and
// returns {"ok": true} on success or {"ok": false, "error": "..."}
// on failure. Never touches the bot store: this is the pre-save
// syntax-check path the dashboard "Validate" button hits via
// htmx. Returns 503 when no validator is wired (test harness).
func (a *API) handleValidateBot(w http.ResponseWriter, r *http.Request) {
	if a.botValidator == nil {
		writeError(w, http.StatusServiceUnavailable, "no_validator", "bot validator not configured")
		return
	}
	var req validateBotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if err := a.botValidator(req.Source); err != nil {
		writeJSON(w, http.StatusOK, validateBotResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, validateBotResponse{OK: true})
}

func (a *API) handleDeleteBot(w http.ResponseWriter, r *http.Request) {
	if a.botManager == nil {
		writeError(w, http.StatusServiceUnavailable, "no_manager", "bot manager not configured")
		return
	}
	id := r.PathValue("id")
	if err := a.botManager.DeleteBot(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "bot does not exist")
			return
		}
		a.logger.Warn("DeleteBot failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
