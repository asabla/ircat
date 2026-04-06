package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/asabla/ircat/internal/state"
)

type userRecord struct {
	Nick       string   `json:"nick"`
	User       string   `json:"user"`
	Host       string   `json:"host"`
	Realname   string   `json:"realname"`
	Modes      string   `json:"modes"`
	Hostmask   string   `json:"hostmask"`
	ConnectAt  string   `json:"connect_at"`
	Channels   []string `json:"channels"`
}

func (a *API) userToRecord(u state.User) userRecord {
	rec := userRecord{
		Nick:      u.Nick,
		User:      u.User,
		Host:      u.Host,
		Realname:  u.Realname,
		Modes:     u.Modes,
		Hostmask:  u.Hostmask(),
		ConnectAt: u.ConnectAt.UTC().Format(rfc3339Nano),
	}
	if a.world != nil {
		for _, ch := range a.world.UserChannels(u.ID) {
			rec.Channels = append(rec.Channels, ch.Name())
		}
		sort.Strings(rec.Channels)
	}
	return rec
}

func (a *API) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if a.world == nil {
		writeJSON(w, http.StatusOK, map[string]any{"users": []userRecord{}})
		return
	}
	snap := a.world.Snapshot()
	out := make([]userRecord, 0, len(snap))
	for _, u := range snap {
		out = append(out, a.userToRecord(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nick < out[j].Nick })
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (a *API) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if a.world == nil {
		writeError(w, http.StatusNotFound, "not_found", "user does not exist")
		return
	}
	nick := r.PathValue("nick")
	u := a.world.FindByNick(nick)
	if u == nil {
		writeError(w, http.StatusNotFound, "not_found", "user does not exist")
		return
	}
	writeJSON(w, http.StatusOK, a.userToRecord(*u))
}

type kickRequest struct {
	Reason string `json:"reason"`
}

// kickReasonCap is the hard upper bound on the kick reason an API
// client can supply. Mirrors the IRC kick reason length limit the
// M2 channel handlers enforce. We do not pull from the running
// config because the api package does not depend on config; a
// fixed cap is fine for the API surface.
const kickReasonCap = 255

func (a *API) handleKickUser(w http.ResponseWriter, r *http.Request) {
	if a.actuator == nil {
		writeError(w, http.StatusServiceUnavailable, "no_actuator", "server actuator not configured")
		return
	}
	nick := r.PathValue("nick")
	var req kickRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", err.Error())
			return
		}
	}
	if req.Reason == "" {
		req.Reason = "Kicked by admin API"
	}
	if len(req.Reason) > kickReasonCap {
		req.Reason = req.Reason[:kickReasonCap]
	}
	if err := a.actuator.KickUser(r.Context(), nick, req.Reason); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "user does not exist")
			return
		}
		a.logger.Warn("KickUser failed", "nick", nick, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "kick user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
