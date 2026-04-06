package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

type eventRecord struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Actor     string `json:"actor"`
	Target    string `json:"target,omitempty"`
	DataJSON  string `json:"data,omitempty"`
}

func (a *API) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := storage.ListEventsOptions{
		Type:     q.Get("type"),
		BeforeID: q.Get("before"),
	}
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = t
		}
	}
	if limit := q.Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil {
			opts.Limit = n
		}
	}
	events, err := a.store.Events().List(r.Context(), opts)
	if err != nil {
		a.logger.Warn("events.List failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "list events")
		return
	}
	out := make([]eventRecord, 0, len(events))
	for _, e := range events {
		out = append(out, eventRecord{
			ID:        e.ID,
			Timestamp: e.Timestamp.UTC().Format(rfc3339Nano),
			Type:      e.Type,
			Actor:     e.Actor,
			Target:    e.Target,
			DataJSON:  e.DataJSON,
		})
	}
	resp := map[string]any{"events": out}
	if len(out) > 0 {
		resp["next_cursor"] = out[len(out)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}
