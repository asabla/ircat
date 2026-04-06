// Package api implements ircat's admin HTTP/JSON API. It mounts
// under /api/v1 inside the dashboard listener.
//
// Design notes:
//
//   - Every endpoint speaks JSON. The error envelope is
//     `{ "error": { "code": "...", "message": "..." } }` with an
//     appropriate HTTP status. Success responses use whatever shape
//     the endpoint documents in docs/API.md.
//   - Authentication is bearer-token only at this layer; the
//     dashboard cookie path is implemented in internal/dashboard.
//     The token is a plaintext value of the form
//     "ircat_<id>_<secret>" minted by [internal/auth.GenerateAPIToken];
//     the storage layer keeps only the sha256 hash.
//   - The API package depends on internal/storage and internal/state,
//     plus a small Actuator interface for the live actions (kick,
//     kill) that internal/server provides. That keeps the dependency
//     arrow one-way: server can import api but api never imports
//     server.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// API is the admin HTTP API. Construct with [New] and call
// [API.Handler] to get an http.Handler suitable for mounting under
// the dashboard's /api/v1 prefix.
type API struct {
	store      storage.Store
	world      *state.World
	logger     *slog.Logger
	actuator   Actuator
	botManager BotManager
	now        func() time.Time
	allowList  []string // optional CORS allow-list (M4 polish: defer)

	// serverInfo describes the running ircat node. Captured at
	// New time so the /server endpoint does not need to grow a
	// closure for every field.
	serverInfo ServerInfoSource
}

// Actuator is the small surface the API uses to take live actions
// against the running server. Implemented by internal/server. Defined
// here so the api package does not import server.
type Actuator interface {
	// KickUser removes a user from every channel they are in and
	// disconnects the connection. Returns ErrNotFound if no such
	// user is registered.
	KickUser(ctx context.Context, nick, reason string) error
	// ListenerAddresses returns the IRC listener bind addresses.
	ListenerAddresses() []string
	// SnapshotUsers returns a copy of every registered user.
	SnapshotUsers() []state.User
	// SnapshotChannels returns a copy of every channel pointer the
	// world is currently tracking. Channels still need their own
	// locks for field reads (the api package handles that).
	SnapshotChannels() []*state.Channel
}

// ServerInfoSource is the small interface the /server endpoint
// pulls from. Implemented by internal/server.Server.
type ServerInfoSource interface {
	ServerName() string
	NetworkName() string
	Version() string
	StartedAt() time.Time
}

// ErrNotFound is returned by Actuator.KickUser when the target nick
// is unknown. The api package translates it to a 404 JSON envelope.
var ErrNotFound = errors.New("api: not found")

// Options configures [New].
type Options struct {
	Store      storage.Store
	World      *state.World
	Actuator   Actuator
	BotManager BotManager
	ServerInfo ServerInfoSource
	Logger     *slog.Logger
}

// New constructs an API.
func New(opts Options) *API {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &API{
		store:      opts.Store,
		world:      opts.World,
		actuator:   opts.Actuator,
		botManager: opts.BotManager,
		serverInfo: opts.ServerInfo,
		logger:     logger,
		now:        time.Now,
	}
}

// Handler returns the http.Handler for the api routes. Mount under
// /api/v1 (the dashboard package strips that prefix automatically).
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /server", a.requireToken(a.handleGetServer))
	mux.HandleFunc("GET /operators", a.requireToken(a.handleListOperators))
	mux.HandleFunc("POST /operators", a.requireToken(a.handleCreateOperator))
	mux.HandleFunc("GET /operators/{name}", a.requireToken(a.handleGetOperator))
	mux.HandleFunc("DELETE /operators/{name}", a.requireToken(a.handleDeleteOperator))
	mux.HandleFunc("GET /users", a.requireToken(a.handleListUsers))
	mux.HandleFunc("GET /users/{nick}", a.requireToken(a.handleGetUser))
	mux.HandleFunc("POST /users/{nick}/kick", a.requireToken(a.handleKickUser))
	mux.HandleFunc("GET /channels", a.requireToken(a.handleListChannels))
	mux.HandleFunc("GET /channels/{name}", a.requireToken(a.handleGetChannel))
	mux.HandleFunc("GET /tokens", a.requireToken(a.handleListTokens))
	mux.HandleFunc("POST /tokens", a.requireToken(a.handleCreateToken))
	mux.HandleFunc("DELETE /tokens/{id}", a.requireToken(a.handleDeleteToken))
	mux.HandleFunc("GET /events", a.requireToken(a.handleListEvents))
	mux.HandleFunc("GET /bots", a.requireToken(a.handleListBots))
	mux.HandleFunc("GET /bots/{id}", a.requireToken(a.handleGetBot))
	mux.HandleFunc("POST /bots", a.requireToken(a.handleCreateBot))
	mux.HandleFunc("PUT /bots/{id}", a.requireToken(a.handleUpdateBot))
	mux.HandleFunc("DELETE /bots/{id}", a.requireToken(a.handleDeleteBot))
	return mux
}

// ----- middleware ------------------------------------------------

// requireToken wraps an http.HandlerFunc with bearer-token auth. It
// extracts the Authorization header, hashes the supplied token, and
// looks it up in the configured token store. On success it stores
// the matched token id on the request context for downstream use.
func (a *API) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.store == nil {
			writeError(w, http.StatusServiceUnavailable, "no_store", "storage not configured")
			return
		}
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, "missing_token", "Authorization: Bearer required")
			return
		}
		plaintext := strings.TrimSpace(header[len(prefix):])
		if plaintext == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "empty bearer token")
			return
		}
		hash := auth.HashAPIToken(plaintext)
		tok, err := a.store.APITokens().GetByHash(r.Context(), hash)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "invalid_token", "token rejected")
				return
			}
			a.logger.Warn("token lookup failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "token lookup failed")
			return
		}
		// Stamp last_used on success. Failures are logged but do
		// not block the request.
		_ = a.store.APITokens().TouchLastUsed(r.Context(), tok.ID, a.now())
		next(w, r)
	}
}

// ----- shared response helpers -----------------------------------

// errorEnvelope is the JSON shape every error response uses.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}
