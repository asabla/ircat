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
