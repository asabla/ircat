// Package dashboard owns ircat's HTTP listener: the operator-facing
// htmx UI, the live SSE streams, and the mount point for the
// internal/api admin endpoints.
//
// At M4 this package is intentionally thin — only /healthz, /readyz,
// and an empty router skeleton — so the rest of M4 can land
// incrementally on top of it. The actual login page, pages, and
// chat surface come in follow-up commits.
package dashboard

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/asabla/ircat/internal/config"
)

// Options configures a [Server] at construction time.
type Options struct {
	Config *config.Config
	Logger *slog.Logger

	// APIHandler, if non-nil, is mounted under /api/v1. It is
	// supplied by internal/api which has its own dependency tree.
	APIHandler http.Handler

	// PageDeps wires the operator-facing dashboard pages to the
	// running ircat node. Optional — when nil, the pages render
	// without runtime data and the login form refuses every
	// attempt.
	PageDeps *PageDeps

	// Metrics is the read-only counter/gauge source for the
	// /metrics endpoint. Optional — when nil, /metrics returns a
	// stub explaining that metrics are unavailable.
	Metrics MetricsSource

	// ReadyFunc reports readiness. The /readyz endpoint returns
	// 200 when this returns nil and 503 when it returns an error.
	// Used by container orchestration to delay traffic until the
	// IRC listener and storage are both up.
	ReadyFunc func() error
}

// Server is the HTTP-side counterpart to internal/server. Construct
// with [New], call [Server.Run] from main, cancel the supplied
// context to shut down.
type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	mux    *http.ServeMux

	listenerMu sync.RWMutex
	listener   net.Listener
	httpServer *http.Server

	readyFunc func() error

	pages    *PageDeps
	sessions *sessionStore
	tmpl     *templates
	metrics  MetricsSource
	series   *metricSeriesSet
}

// New constructs a Server. It does not bind any sockets.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg:     opts.Config,
		logger:  logger,
		mux:     http.NewServeMux(),
		pages:   opts.PageDeps,
		metrics: opts.Metrics,
		readyFunc: func() error {
			if opts.ReadyFunc != nil {
				return opts.ReadyFunc()
			}
			return nil
		},
	}
	cookieName := "ircat_session"
	maxAge := 24 * time.Hour
	secure := false
	if opts.Config != nil {
		if opts.Config.Dashboard.Session.CookieName != "" {
			cookieName = opts.Config.Dashboard.Session.CookieName
		}
		if h := opts.Config.Dashboard.Session.MaxAgeHours; h > 0 {
			maxAge = time.Duration(h) * time.Hour
		}
		secure = opts.Config.Dashboard.Session.Secure
	}
	store, err := newSessionStore(cookieName, maxAge, secure)
	if err != nil {
		logger.Error("session store init failed", "error", err)
	}
	s.sessions = store
	tmpl, err := loadTemplates()
	if err != nil {
		logger.Error("template load failed", "error", err)
	}
	s.tmpl = tmpl
	s.registerRoutes(opts.APIHandler)
	return s
}

// Addr returns the bound listener address, or "" if [Server.Run]
// has not yet bound the listener. Used by tests.
func (s *Server) Addr() string {
	s.listenerMu.RLock()
	defer s.listenerMu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Run binds the configured dashboard listener and serves until ctx
// is cancelled. Returns nil on a clean shutdown, an error on bind
// failure or unexpected stop.
//
// If cfg.Dashboard.Enabled is false, Run returns immediately with
// nil — the operator can disable the dashboard entirely.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg == nil || !s.cfg.Dashboard.Enabled {
		s.logger.Info("dashboard disabled")
		<-ctx.Done()
		return nil
	}
	addr := s.cfg.Dashboard.Address
	if addr == "" {
		return errors.New("dashboard: dashboard.address is empty")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dashboard listen %s: %w", addr, err)
	}

	// Optional in-process TLS termination. The operator may
	// instead front the dashboard with a reverse proxy and leave
	// dashboard.tls.enabled false; both deployment modes are
	// supported. When enabled we wrap the underlying TCP
	// listener in a tls.Listener so the standard http.Server
	// path stays untouched.
	tlsCfg := s.cfg.Dashboard.TLS
	if tlsCfg.Enabled {
		if tlsCfg.CertFile == "" || tlsCfg.KeyFile == "" {
			_ = ln.Close()
			return errors.New("dashboard: tls.enabled but cert_file/key_file are empty")
		}
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("dashboard tls keypair: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
		s.logger.Info("dashboard tls enabled")
	}

	s.listenerMu.Lock()
	s.listener = ln
	s.listenerMu.Unlock()

	s.httpServer = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          nil, // suppress stdlib log; we use slog via the handler
	}

	s.logger.Info("dashboard listener bound", "address", ln.Addr().String(), "tls", tlsCfg.Enabled)

	// Spin up the metric sample loop now that the listener is
	// up. The loop ticks every sparklineInterval and stops when
	// ctx is cancelled, so it dies cleanly with the rest of the
	// server.
	s.startSampleLoop(ctx)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.httpServer.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		<-serveErr
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// registerRoutes wires the static / always-on routes onto s.mux.
// Dynamic routes (login, pages, SSE) get added by later commits.
//
// The root pattern uses {$} so it matches only the literal "/"
// path. Without that, "GET /" would conflict with the more
// specific "/api/v1/" subtree pattern: Go 1.22+ ServeMux refuses
// to disambiguate the case where one pattern matches more methods
// than the other but a more specific path.
func (s *Server) registerRoutes(api http.Handler) {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /{$}", s.handleRoot)

	// Static assets (CSS).
	s.mux.Handle("GET /dashboard/static/", http.StripPrefix("/dashboard/", http.FileServer(http.FS(staticFS))))

	// Login surface.
	s.mux.HandleFunc("GET /login", s.handleLoginGet)
	s.mux.HandleFunc("POST /dashboard/login", s.handleLoginPost)
	s.mux.HandleFunc("POST /dashboard/logout", s.requireSession(s.handleLogout))

	// Authenticated pages.
	s.mux.HandleFunc("GET /dashboard", s.requireSession(s.handleOverview))
	s.mux.HandleFunc("GET /dashboard/overview/cards", s.requireSession(s.handleOverviewCards))
	s.mux.HandleFunc("GET /dashboard/users", s.requireSession(s.handleUsersPage))
	s.mux.HandleFunc("GET /dashboard/users/{nick}", s.requireSession(s.handleUserDetailPage))
	s.mux.HandleFunc("POST /dashboard/users/{nick}/kick", s.requireSession(s.handleKickUserPage))
	s.mux.HandleFunc("GET /dashboard/channels", s.requireSession(s.handleChannelsPage))
	s.mux.HandleFunc("GET /dashboard/channels/{name}", s.requireSession(s.handleChannelDetailPage))
	s.mux.HandleFunc("POST /dashboard/channels/{name}/topic", s.requireSession(s.handleChannelTopicPost))
	s.mux.HandleFunc("GET /dashboard/federation", s.requireSession(s.handleFederationPage))
	s.mux.HandleFunc("GET /dashboard/bots", s.requireSession(s.handleBotsPage))
	s.mux.HandleFunc("POST /dashboard/bots", s.requireSession(s.handleBotsCreate))
	s.mux.HandleFunc("GET /dashboard/bots/{id}", s.requireSession(s.handleBotDetailPage))
	s.mux.HandleFunc("POST /dashboard/bots/{id}/source", s.requireSession(s.handleBotSourcePost))
	s.mux.HandleFunc("POST /dashboard/bots/{id}/toggle", s.requireSession(s.handleBotTogglePost))
	s.mux.HandleFunc("POST /dashboard/bots/{id}/delete", s.requireSession(s.handleBotDelete))
	s.mux.HandleFunc("GET /dashboard/operators", s.requireSession(s.handleOperatorsPage))
	s.mux.HandleFunc("POST /dashboard/operators", s.requireSession(s.handleOperatorCreate))
	s.mux.HandleFunc("POST /dashboard/operators/{name}/delete", s.requireSession(s.handleOperatorDelete))
	s.mux.HandleFunc("GET /dashboard/tokens", s.requireSession(s.handleTokensPage))
	s.mux.HandleFunc("POST /dashboard/tokens", s.requireSession(s.handleTokenCreate))
	s.mux.HandleFunc("POST /dashboard/tokens/{id}/delete", s.requireSession(s.handleTokenDelete))
	s.mux.HandleFunc("GET /dashboard/events", s.requireSession(s.handleEventsPage))
	s.mux.HandleFunc("GET /dashboard/logs", s.requireSession(s.handleLogsPage))
	s.mux.HandleFunc("GET /dashboard/logs/sse", s.requireSession(s.handleLogsSSE))

	if api != nil {
		s.mux.Handle("/api/v1/", http.StripPrefix("/api/v1", api))
	}
}

// handleHealthz returns 200 unconditionally — it is the "process
// is alive" probe. Use /readyz for the "process is ready to take
// traffic" probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz returns 200 if the configured ReadyFunc says we are
// ready, 503 otherwise.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.readyFunc(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleRoot is a placeholder until the dashboard pages land. It
// returns a tiny "ircat" page so an operator hitting / from a
// browser sees something other than a 404. The mux pattern already
// scopes this to the literal "/" path.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>ircat</title></head><body><h1>ircat</h1><p>Dashboard pages land in M4 follow-ups.</p></body></html>`))
}

// writeJSON is the small helper every JSON-returning route uses so
// the Content-Type and trailing newline are consistent.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}
