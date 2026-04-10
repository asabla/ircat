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
	cryptorand "crypto/rand"
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
	"github.com/asabla/ircat/internal/services/chanserv"
	"github.com/asabla/ircat/internal/services/nickserv"
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

	// bots maps registered UserID to a virtual delivery hook, used
	// by non-TCP members (the Lua bot supervisor in M5). The
	// broadcast path falls through to this map when a channel
	// member has no matching real Conn. Protected by connsMu since
	// the two registries are always accessed together on the
	// broadcast hot path.
	bots map[state.UserID]BotDeliverer

	// eventBus is the optional outbound event publisher. Audit
	// events land here alongside the store write so external
	// sinks (jsonl, webhook) can observe them. Nil means
	// "no fan-out" — the server still writes to the store.
	eventBus EventPublisher

	// nickserv is the live NickServ service instance. Nil when
	// no store is wired (tests without persistence). Used by the
	// registration and nick-change paths to trigger enforcement.
	nickserv *nickserv.Service

	// chanserv is the live ChanServ service instance. Nil when
	// no store is wired. Used by the JOIN path to trigger auto-op.
	chanserv *chanserv.Service

	// fedLinks is the federation link registry, keyed by peer
	// server name. See internal/server/federation.go for the
	// API the broadcast path uses.
	//
	// fedSubs is the per-peer channel subscription set used by
	// the subscription broadcast mode. peerName -> channelName ->
	// true. A peer is subscribed to a channel iff this node has
	// either bursted the channel state to it, received a runtime
	// JOIN for the channel from it, or accepted a remote member
	// from it via DeliverLocal. Subscriptions are dropped on
	// link close via DropPeerSubscriptions.
	fedMu    sync.RWMutex
	fedLinks map[string]fedLinkSender
	fedSubs  map[string]map[string]bool
	// squitSeen is the (peer, reason) -> expiry map used by
	// HandleSquit to short-circuit a fan-out loop in a
	// >3-node mesh. Entries are added on first observation and
	// drop on the next call after their expiry. Guarded by
	// fedMu so concurrent SQUIT receivers do not race each
	// other or the LinkFor read path.
	squitSeen map[squitSeenKey]time.Time

	// motd is the message-of-the-day file content split into lines.
	// Loaded at startup and re-read on ReloadMOTD; nil if no MOTD
	// is configured or the file is missing (we send ERR_NOMOTD in
	// that case). Guarded by motdMu so the SIGHUP reload path can
	// swap it from a non-conn goroutine without racing the
	// welcome burst that reads it.
	motdMu sync.RWMutex
	motd   []string

	// whowas is the historical-nick ring buffer driven by RFC 2812
	// §3.6.3 WHOWAS. Entries are appended on disconnect, KILL, and
	// nick change so a later WHOWAS lookup can recover what the
	// nick used to point at. Capacity is fixed at construction.
	whowas *state.Whowas

	// reloader is the optional config reloader the REHASH command
	// drives. Wired in by the host (cmd/ircat) so the server does
	// not import the cmd package.
	reloader Reloader

	// shutdown is the optional process-exit callback the DIE and
	// RESTART operator commands fire. Wired in by the host so the
	// server can ask its parent to terminate without importing
	// process-management glue.
	shutdown func(reason string)

	// connector is the optional runtime federation dialer the
	// operator CONNECT command drives. Wired in by the host.
	connector Connector

	// shuttingDown is set to 1 once Run begins its drain. New accepts
	// observe it and refuse cleanly instead of racing the close.
	shuttingDown atomic.Bool

	// messagesIn / messagesOut are global counters incremented by
	// every connection's read/write loop. They drive the
	// /metrics endpoint. Atomic so the dashboard scrape can read
	// them without taking any of the server's per-conn locks.
	messagesIn  atomic.Uint64
	messagesOut atomic.Uint64
}

// BotDeliverer is the small interface the bot supervisor registers
// with the server so channel broadcasts reach bots. Deliver is
// called synchronously from the broadcast path and MUST NOT block;
// implementations should queue the message onto an internal
// goroutine channel and return immediately.
type BotDeliverer interface {
	Deliver(*protocol.Message)
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

// WithEventBus attaches an outbound event publisher. Audit events
// are published to it alongside the store write so external sinks
// can observe them.
func WithEventBus(bus EventPublisher) Option {
	return func(s *Server) { s.eventBus = bus }
}

// Reloader is the small surface the operator REHASH command uses
// to trigger a SIGHUP-equivalent config reload. Implemented by
// *cmd/ircat.reloadDeps. Optional — when nil, REHASH replies with
// 481 (no operator) or a benign no-op message.
type Reloader interface {
	Reload(ctx context.Context) error
}

// WithReloader wires a config reloader so REHASH can apply config
// changes without restarting the process.
func WithReloader(r Reloader) Option {
	return func(s *Server) { s.reloader = r }
}

// Connector is the small surface the operator CONNECT command uses
// to ask the host to dial a federation peer at runtime. Optional —
// when nil, CONNECT returns a NOTICE explaining the no-op.
type Connector interface {
	Connect(ctx context.Context, target string, port int) error
}

// WithConnector wires a runtime federation dialer so the operator
// CONNECT command can bring up new peer links without restarting
// the daemon.
func WithConnector(c Connector) Option {
	return func(s *Server) { s.connector = c }
}

// WithShutdown installs a callback the DIE/RESTART operator commands
// will fire to ask the host process to exit. The reason string is
// surfaced in the shutdown log line. The callback should be
// non-blocking and return immediately; the actual shutdown happens
// asynchronously when the host's run loop notices.
func WithShutdown(fn func(reason string)) Option {
	return func(s *Server) { s.shutdown = fn }
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
		bots:   make(map[state.UserID]BotDeliverer),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.createdAt = s.now()
	s.motd = loadMOTD(cfg.Server.MOTDFile, logger)
	s.whowas = state.NewWhowas(cfg.Server.Limits.WhowasHistory, world.CaseMapping())
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

// RegisterBot associates a virtual delivery hook with id so
// broadcasts reach the supplied bot. The id must already belong
// to a state.User in the world (the bot supervisor creates one
// per bot). Called from [internal/bots.Supervisor] on bot start.
func (s *Server) RegisterBot(id state.UserID, deliverer BotDeliverer) {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	s.bots[id] = deliverer
}

// UnregisterBot drops a virtual delivery hook. Called from the bot
// supervisor on bot stop.
func (s *Server) UnregisterBot(id state.UserID) {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	delete(s.bots, id)
}

// botFor returns the deliverer registered for id, or nil. Used by
// broadcastToChannel to fall through to the virtual path when the
// conn registry has no real Conn for the member.
func (s *Server) botFor(id state.UserID) BotDeliverer {
	s.connsMu.RLock()
	defer s.connsMu.RUnlock()
	return s.bots[id]
}

// broadcastToChannel sends msg to every member of ch. Real
// connections receive via Conn.send; virtual bot members receive
// via their registered BotDeliverer. If except is non-zero, the
// matching member is skipped (used by PRIVMSG so the sender doesn't
// echo to themselves). If includeSelf is true, the except parameter
// is ignored.
//
// On +a (anonymous) channels per RFC 2811 §4.2.1, the prefix of
// every outbound message is rewritten to the canonical
// anonymous!anonymous@anonymous. mask so members cannot identify
// each other. The rewrite happens on a copy so the same Message
// can be reused on non-anonymous fan-out paths without leaking
// the rewrite.
func (s *Server) broadcastToChannel(ch *state.Channel, msg *protocol.Message, except state.UserID, includeSelf bool) {
	if ch.Anonymous() {
		anon := *msg
		anon.Prefix = anonymousMask
		msg = &anon
	}
	for id := range ch.MemberIDs() {
		if !includeSelf && id == except {
			continue
		}
		if c := s.connFor(id); c != nil {
			c.send(msg)
			continue
		}
		if b := s.botFor(id); b != nil {
			b.Deliver(msg)
		}
	}
}

// anonymousMask is the canonical hostmask used as the prefix on
// every message originating from a +a channel (RFC 2811 §4.2.1).
const anonymousMask = "anonymous!anonymous@anonymous."

// newSafeChannelID generates a fresh 5-character RFC 2811 §3 safe
// channel ID. The character set is uppercase letters and digits.
// We use crypto/rand because the search space (36^5 = 60M) is
// large enough that any collision is essentially impossible, but
// we still re-roll on the off chance an existing channel happens
// to share the suffix.
func (s *Server) newSafeChannelID() string {
	for attempt := 0; attempt < 8; attempt++ {
		var raw [safeChannelIDLen]byte
		if _, err := cryptorand.Read(raw[:]); err != nil {
			// Should never happen on a working OS; fall back
			// to a low-entropy time-derived ID rather than
			// crashing.
			now := s.now().UnixNano()
			for i := 0; i < safeChannelIDLen; i++ {
				raw[i] = byte(now >> (i * 8))
			}
		}
		var buf [safeChannelIDLen]byte
		for i := 0; i < safeChannelIDLen; i++ {
			buf[i] = safeChannelIDAlphabet[int(raw[i])%len(safeChannelIDAlphabet)]
		}
		id := string(buf[:])
		// Reject if any existing safe channel already uses this
		// exact ID, regardless of suffix.
		clash := false
		for _, ch := range s.world.ChannelsSnapshot() {
			n := ch.Name()
			if isSafeChannel(n) && len(n) > 1+safeChannelIDLen && n[1:1+safeChannelIDLen] == id {
				clash = true
				break
			}
		}
		if !clash {
			return id
		}
	}
	// Astronomically unlikely. Fall through with the last buf;
	// the resulting channel will share an ID with another, which
	// the resolveSafeChannel suffix lookup arbitrates by first
	// match.
	return "AAAAA"
}

// Run binds every configured listener and serves until ctx is
// cancelled. On shutdown it stops accepting, closes all listeners,
// then waits for in-flight connections to finish their drain.
// SendNoticeToNick delivers a NOTICE from the given prefix to the
// named nick. Used by in-process services (NickServ) to reply to
// users. Satisfies nickserv.ReplySender.
func (s *Server) SendNoticeToNick(from, target, text string) {
	u := s.world.FindByNick(target)
	if u == nil {
		return
	}
	msg := &protocol.Message{
		Prefix:  from,
		Command: "NOTICE",
		Params:  []string{u.Nick, text},
	}
	if c := s.connFor(u.ID); c != nil {
		c.send(msg)
	}
}

func (s *Server) Run(ctx context.Context) error {
	if len(s.cfg.Server.Listeners) == 0 {
		return errors.New("server: no listeners configured")
	}

	// Start in-process services if the account store is available.
	if s.store != nil {
		s.startNickServ(ctx)
		s.startChanServ(ctx)
	}

	if err := s.restorePersistentChannels(ctx); err != nil {
		return fmt.Errorf("restore persistent channels: %w", err)
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

// startNickServ registers a NickServ service user in the world and
// wires it up as a BotDeliverer so PRIVMSG/SQUERY reach it.
func (s *Server) startNickServ(ctx context.Context) {
	svc, err := nickserv.Start(ctx, s.store.Accounts(), s.world, s, s.logger)
	if err != nil {
		s.logger.Warn("NickServ failed to start", "error", err)
		return
	}
	s.RegisterBot(svc.User().ID, svc)
	s.nickserv = svc
}

// ForceNickChange renames a user on the server. Used by NickServ
// enforcement to guest-rename unidentified users. Satisfies
// nickserv.ReplySender.
func (s *Server) ForceNickChange(oldNick, newNick string) bool {
	u := s.world.FindByNick(oldNick)
	if u == nil {
		return false
	}
	if err := s.world.RenameUser(u.ID, newNick); err != nil {
		s.logger.Warn("ForceNickChange failed", "from", oldNick, "to", newNick, "error", err)
		return false
	}
	// Broadcast the nick change to the user and their channels.
	nickMsg := &protocol.Message{
		Prefix:  u.Hostmask(),
		Command: "NICK",
		Params:  []string{newNick},
	}
	if c := s.connFor(u.ID); c != nil {
		c.send(nickMsg)
	}
	for _, ch := range s.world.UserChannels(u.ID) {
		s.broadcastToChannel(ch, nickMsg, u.ID, false)
	}
	return true
}

// notifyNickServ tells NickServ to check whether a nick is
// registered and start enforcement if needed. No-op when
// NickServ is not running.
func (s *Server) notifyNickServ(nick string, account string) {
	if s.nickserv == nil {
		return
	}
	s.nickserv.CheckNick(nick, account != "")
}

// startChanServ registers a ChanServ service user in the world and
// wires it up as a BotDeliverer so PRIVMSG/SQUERY reach it.
func (s *Server) startChanServ(ctx context.Context) {
	svc, err := chanserv.Start(
		ctx,
		s.store.RegisteredChannels(),
		s.store.Accounts(),
		s.world, s, s.logger,
	)
	if err != nil {
		s.logger.Warn("ChanServ failed to start", "error", err)
		return
	}
	s.RegisterBot(svc.User().ID, svc)
	s.chanserv = svc
}

// SetChannelMode broadcasts a MODE change on a channel. Used by
// ChanServ to set +o/-o. Satisfies chanserv.ReplySender.
func (s *Server) SetChannelMode(from, channel, modeStr, target string) {
	ch := s.world.FindChannel(channel)
	if ch == nil {
		return
	}
	msg := &protocol.Message{
		Prefix:  from,
		Command: "MODE",
		Params:  []string{channel, modeStr, target},
	}
	s.broadcastToChannel(ch, msg, 0, true)
}

// notifyChanServ tells ChanServ to check whether a joining user
// should be auto-opped. No-op when ChanServ is not running.
func (s *Server) notifyChanServ(nick, account, channel string) {
	if s.chanserv == nil {
		return
	}
	s.chanserv.CheckJoin(nick, account, channel)
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
