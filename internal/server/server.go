// Package server owns the IRC TCP listeners and the per-connection
// lifecycle. It is the network-side counterpart to internal/protocol
// (which only deals with bytes) and internal/state (which holds the
// authoritative in-memory data model).
//
// Responsibilities, in order of importance:
//   - Accept TCP/TLS connections on the listeners declared in
//     [config.Config.Server.Listeners].
//   - Drive the registration state machine (PASS -> NICK -> USER ->
//     welcome burst) for each connection.
//   - Dispatch parsed messages to per-command handlers.
//   - Run the PING/PONG keepalive and disconnect idle clients.
//   - Drain everything cleanly when the parent context cancels.
//
// What this package does NOT do (yet):
//   - Channels — M2.
//   - Persistent storage of operator accounts — M3.
//   - Dashboard or admin API — M4.
//   - Federation links — M7.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// Server is the running IRC daemon.
//
// Construct one with [New], call [Server.Run] from main, and cancel
// the supplied context to shut it down. The Run method blocks until
// every accepted connection has drained.
type Server struct {
	cfg    *config.Config
	world  *state.World
	store  storage.Store
	logger *slog.Logger
	now    func() time.Time

	// createdAt is what RPL_CREATED reports. Captured at New time so
	// the welcome burst is consistent across reconfigures.
	createdAt time.Time

	// listeners holds the bound listeners. They are closed in Run on
	// shutdown. Protected by listenerMu so external callers can read
	// the bound addresses without racing the Run goroutine.
	listenerMu sync.RWMutex
	listeners  []net.Listener

	// connWG counts every active per-connection goroutine tree so
	// shutdown can wait for them to finish.
	connWG sync.WaitGroup

	// conns maps registered UserID to its owning Conn so handlers
	// can fan messages out to other users (PRIVMSG, JOIN, NICK,
	// QUIT broadcasts). Pre-registration connections are not in the
	// map; they have no UserID yet.
	connsMu sync.RWMutex
	conns   map[state.UserID]*Conn

	// motd is the message-of-the-day file content split into lines.
	// Loaded once at startup; nil if no MOTD is configured or the
	// file is missing (we send ERR_NOMOTD in that case).
	motd []string

	// shuttingDown is set to 1 once Run begins its drain. New accepts
	// observe it and refuse cleanly instead of racing the close.
	shuttingDown atomic.Bool
}

// Option lets callers override defaults at construction time. Tests
// use [WithClock] to make timestamps deterministic.
type Option func(*Server)

// WithClock overrides the time source. Production never sets it.
func WithClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// WithStore wires a persistent storage backend into the server.
// Without it OPER fails with ERR_NOOPERHOST and persistent channel
// state is not restored on startup. Tests that exercise non-OPER
// surfaces can leave it nil.
func WithStore(store storage.Store) Option {
	return func(s *Server) { s.store = store }
}

// New constructs a Server. It does not bind any sockets; that happens
// in [Server.Run].
func New(cfg *config.Config, world *state.World, logger *slog.Logger, opts ...Option) *Server {
	s := &Server{
		cfg:    cfg,
		world:  world,
		logger: logger,
		now:    time.Now,
		conns:  make(map[state.UserID]*Conn),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.createdAt = s.now()
	s.motd = loadMOTD(cfg.Server.MOTDFile, logger)
	return s
}

// registerConn associates a registered Conn with its UserID so other
// handlers can find it for fan-out. Called from
// tryCompleteRegistration after the user has been added to the world.
func (s *Server) registerConn(c *Conn) {
	if c.user == nil {
		return
	}
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	s.conns[c.user.ID] = c
}

// unregisterConn drops a Conn from the registry. Called from
// Conn.close.
func (s *Server) unregisterConn(id state.UserID) {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	delete(s.conns, id)
}

// connFor returns the Conn currently registered for id, or nil. Used
// by message fan-out to look up the target of a directed message
// and by channel broadcast to walk the membership.
func (s *Server) connFor(id state.UserID) *Conn {
	s.connsMu.RLock()
	defer s.connsMu.RUnlock()
	return s.conns[id]
}

// broadcastToChannel sends msg to every Conn whose user is currently
// a member of ch. If except is non-zero, the matching member is
// skipped (used by PRIVMSG so the sender doesn't echo to themselves).
// If includeSelf is true, the except parameter is ignored.
func (s *Server) broadcastToChannel(ch *state.Channel, msg *protocol.Message, except state.UserID, includeSelf bool) {
	for id := range ch.MemberIDs() {
		if !includeSelf && id == except {
			continue
		}
		if c := s.connFor(id); c != nil {
			c.send(msg)
		}
	}
}

// Run binds every configured listener and serves until ctx is
// cancelled. On shutdown it stops accepting, closes all listeners,
// then waits for in-flight connections to finish their drain.
func (s *Server) Run(ctx context.Context) error {
	if len(s.cfg.Server.Listeners) == 0 {
		return errors.New("server: no listeners configured")
	}

	bound := make([]net.Listener, 0, len(s.cfg.Server.Listeners))
	for _, lc := range s.cfg.Server.Listeners {
		l, err := bindListener(lc)
		if err != nil {
			for _, prev := range bound {
				_ = prev.Close()
			}
			return fmt.Errorf("bind %s: %w", lc.Address, err)
		}
		s.logger.Info("listener bound", "address", l.Addr().String(), "tls", lc.TLS)
		bound = append(bound, l)
	}
	s.listenerMu.Lock()
	s.listeners = bound
	s.listenerMu.Unlock()

	// Per-listener accept loop. Each loop runs in its own goroutine
	// and feeds new Conns into connWG.
	var acceptWG sync.WaitGroup
	for _, l := range s.listeners {
		l := l
		acceptWG.Add(1)
		go func() {
			defer acceptWG.Done()
			s.acceptLoop(ctx, l)
		}()
	}

	<-ctx.Done()
	s.shuttingDown.Store(true)
	s.closeAllListeners()
	acceptWG.Wait()
	s.connWG.Wait()
	return nil
}

func (s *Server) acceptLoop(ctx context.Context, l net.Listener) {
	for {
		nc, err := l.Accept()
		if err != nil {
			if s.shuttingDown.Load() {
				return
			}
			// Transient accept errors get logged and we keep going;
			// fatal errors (like the listener being closed) are
			// indistinguishable from a normal shutdown so we still
			// drop out of the loop.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			s.logger.Warn("accept error", "error", err)
			return
		}
		c := newConn(s, nc)
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			c.serve(ctx)
		}()
	}
}

func (s *Server) closeAllListeners() {
	s.listenerMu.RLock()
	defer s.listenerMu.RUnlock()
	for _, l := range s.listeners {
		_ = l.Close()
	}
}

// ListenerAddrs returns the addresses the server has actually bound,
// in the order the listeners were declared in the config. Useful for
// tests that ask the server to bind ":0" and need to discover the
// kernel-assigned port. Returns nil before [Server.Run] has finished
// binding.
func (s *Server) ListenerAddrs() []net.Addr {
	s.listenerMu.RLock()
	defer s.listenerMu.RUnlock()
	out := make([]net.Addr, 0, len(s.listeners))
	for _, l := range s.listeners {
		out = append(out, l.Addr())
	}
	return out
}

// bindListener binds either a plain TCP or a TLS listener depending
// on the [config.Listener.TLS] flag.
func bindListener(lc config.Listener) (net.Listener, error) {
	if !lc.TLS {
		return net.Listen("tcp", lc.Address)
	}
	cert, err := tls.LoadX509KeyPair(lc.CertFile, lc.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls keypair: %w", err)
	}
	return tls.Listen("tcp", lc.Address, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
}
