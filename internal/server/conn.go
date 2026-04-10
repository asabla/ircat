package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// outboundQueue is the bounded per-connection write queue. Picked to
// be small enough that a stuck client triggers SendQ-exceeded quickly
// without blocking the rest of the network, and large enough that
// normal bursty traffic (a JOIN that triggers NAMES + WHO replies)
// flows through without back-pressuring the sender.
const outboundQueue = 64

// Conn is one accepted client connection. The lifetime is owned by
// [Server.acceptLoop]; nothing outside this package should hold a
// reference to a Conn.
//
// Locking discipline:
//   - Fields touched only by the read goroutine (registration state,
//     pending) need no synchronization.
//   - lastActivity is touched by both the read goroutine (each
//     parsed line) and the ping goroutine (timeout check), so it
//     uses atomic.Int64.
//   - The send method is the only writer for the outbound channel
//     and is safe to call from any goroutine; the actual socket
//     write happens in writeLoop.
type Conn struct {
	server *Server
	nc     net.Conn
	logger *slog.Logger

	// out is the bounded outbound queue. writeLoop drains it; send
	// fills it. Closed by the lifecycle owner during teardown.
	out chan *protocol.Message

	// ctx is the per-connection context. Cancelling it stops every
	// goroutine attached to this connection. Cancellation is the
	// universal "this connection is going away" signal.
	ctx    context.Context
	cancel context.CancelCauseFunc

	// closeOnce guarantees the underlying net.Conn is closed exactly
	// once even if multiple goroutines reach for the brakes.
	closeOnce sync.Once

	// lastActivity is the unix-nanos timestamp of the most recent
	// inbound message. Read by the ping goroutine, written by the
	// read goroutine.
	lastActivity atomic.Int64

	// lastMessageAt is the unix-nanos timestamp of the most recent
	// PRIVMSG / NOTICE this user sent. Used by WHOIS 317 to report
	// the RFC 2812 §3.6.2 idle time. Distinct from lastActivity
	// because PING/PONG/WHO and other administrative traffic does
	// not count as "speaking activity" for the idle timer.
	lastMessageAt atomic.Int64

	// pending tracks the registration state machine.
	pending pending

	// user is the state.User backing this connection once
	// registration completes. Nil before that.
	user *state.User

	// remoteHost is the resolved peer host, captured at connect time.
	remoteHost string

	// quitReason is set by handleQuit so close() can broadcast a
	// QUIT to channel peers with the client-supplied reason rather
	// than the generic fallback. Atomic-stored under the same lock
	// as the rest of the lifecycle (the close path is single-shot).
	quitReason string

	// msgBucket is the per-connection flood-control bucket consumed
	// by PRIVMSG and NOTICE.
	msgBucket *tokenBucket
	// msgViolations counts dropped messages so the server can
	// disconnect a persistently flooding client.
	msgViolations int

	// capsAccepted is the set of IRCv3 capabilities the client
	// has successfully REQ'd. Membership in this set lets handlers
	// decide whether to attach extra metadata (e.g. message-tags
	// pass-through) on outbound messages aimed at this conn.
	capsAccepted map[string]bool
}

// pending holds the partial state collected during registration.
// Once both nick and userParams are present (and any pending CAP
// negotiation has finished) we promote it to a state.User and add
// it to the world.
type pending struct {
	nick     string
	user     string
	realname string
	password string
	// passVersion / passFlags are the optional 2nd / 3rd params on
	// PASS (RFC 2813 §4.1.1). Federation peers populate them on the
	// outgoing handshake; client connections leave them empty.
	passVersion string
	passFlags   string
	nickSet     bool
	userSet     bool

	// capNegotiating is true once we have seen CAP LS or CAP REQ from
	// the client. While true, registration must wait for CAP END
	// before sending the welcome burst, even if NICK and USER are
	// already in. This matches the IRCv3 capability negotiation spec
	// — clients that opt into CAP signal "I'm done negotiating" via
	// CAP END, and servers must hold the welcome burst until then.
	capNegotiating bool
	// capEnded is set when CAP END arrives. The two flags together
	// give a clear "negotiation in progress" / "negotiation done"
	// state machine that tryCompleteRegistration can ask about.
	capEnded bool

	// account is the authenticated account name, populated by a
	// successful SASL PLAIN exchange before registration completes.
	account string
}

func newConn(srv *Server, nc net.Conn) *Conn {
	ctx, cancel := context.WithCancelCause(context.Background())
	host, _, err := net.SplitHostPort(nc.RemoteAddr().String())
	if err != nil {
		host = nc.RemoteAddr().String()
	}
	c := &Conn{
		server:     srv,
		nc:         nc,
		logger:     srv.logger.With("remote", nc.RemoteAddr().String()),
		out:        make(chan *protocol.Message, outboundQueue),
		ctx:        ctx,
		cancel:     cancel,
		remoteHost: host,
	}
	c.lastActivity.Store(srv.now().UnixNano())
	c.msgBucket = newTokenBucket(
		srv.cfg.Server.Limits.MessageBurst,
		srv.cfg.Server.Limits.MessageRefillPerSecond,
		srv.now,
	)
	return c
}

// serve runs the read, write, and ping goroutines for this connection
// and returns once they have all finished. The parent context is the
// server-wide shutdown signal; cancelling it tears down the connection.
//
// Shutdown ordering is delicate. The cycle is:
//
//  1. Something cancels the per-conn context (handleQuit, sendq
//     overflow, parent shutdown, write error).
//  2. writeLoop sees ctx.Done, drains any pending outbound messages
//     to the socket so a queued ERROR (e.g. from QUIT) actually
//     reaches the client, then closes the underlying net.Conn.
//  3. Closing the socket unblocks readLoop, which is otherwise
//     parked in ReadBytes on a 15-minute deadline.
//  4. wg.Wait() returns once all three loops have exited.
//  5. close() runs the broadcastQuit + world cleanup.
func (c *Conn) serve(parent context.Context) {
	stop := context.AfterFunc(parent, func() {
		c.cancel(errors.New("server shutting down"))
	})
	defer stop()
	defer c.close()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); c.readLoop() }()
	go func() { defer wg.Done(); c.writeLoop() }()
	go func() { defer wg.Done(); c.pingLoop() }()
	wg.Wait()
}

func (c *Conn) close() {
	c.closeOnce.Do(func() {
		c.cancel(io.EOF)
		_ = c.nc.Close()
		// Drop the user from the world if we promoted one. Order
		// matters here:
		//   1. Build a QUIT broadcast so peers in shared channels
		//      see the disappearance with the right hostmask.
		//   2. Walk the user channels, send QUIT to every other
		//      member, and remove the user from each channel.
		//   3. Unregister from the conn registry so no in-flight
		//      broadcast lands on us.
		//   4. Drop the user record from the world.
		if c.user == nil {
			return
		}
		reason := c.quitReason
		if reason == "" {
			reason = "Client closed connection"
		}
		c.broadcastQuit(reason)
		c.server.recordWhowas(c.user)
		c.server.unregisterConn(c.user.ID)
		c.server.world.RemoveUser(c.user.ID)
		c.user = nil
	})
}

// broadcastQuit sends a QUIT message to every member of every
// channel the user is currently in (excluding the user themselves)
// and then removes the user from each of those channels. Used by
// both close() and handleQuit.
func (c *Conn) broadcastQuit(reason string) {
	if c.user == nil {
		return
	}
	quitMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "QUIT",
		Params:  []string{reason},
	}
	chans := c.server.world.UserChannels(c.user.ID)
	seen := map[state.UserID]bool{c.user.ID: true}
	for _, ch := range chans {
		for id := range ch.MemberIDs() {
			if seen[id] {
				continue
			}
			seen[id] = true
			if peer := c.server.connFor(id); peer != nil {
				peer.send(quitMsg)
			}
		}
		// Remove the user from this channel; if it becomes empty
		// the world will drop it.
		_, _, _ = c.server.world.PartChannel(c.user.ID, ch.Name())
	}
	// Forward the QUIT to every federation peer so remote nodes
	// can drop their copy of the user. Done after the local fan-
	// out so a slow link cannot delay our local cleanup.
	c.server.forwardToAllLinks(quitMsg)
}

// readLoop reads CRLF-delimited lines, parses them, and dispatches
// each one to the command handler. It exits on EOF, parse limit
// breach, or write error from the dispatcher.
func (c *Conn) readLoop() {
	defer c.cancel(errors.New("read loop exited"))
	r := bufio.NewReaderSize(c.nc, protocol.MaxMessageBytes*2)
	for {
		// Read deadline so a stuck client cannot hold us forever.
		// We refresh the deadline far enough in the future that the
		// ping loop is responsible for actually disconnecting idle
		// clients; this deadline only catches half-open sockets.
		_ = c.nc.SetReadDeadline(c.server.now().Add(15 * time.Minute))

		line, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || isClosedNetError(err) {
				return
			}
			c.logger.Debug("read error", "error", err)
			return
		}
		if len(line) > protocol.MaxMessageBytes {
			c.logger.Debug("oversized line, dropping client", "bytes", len(line))
			c.sendError("Message too long")
			return
		}
		c.lastActivity.Store(c.server.now().UnixNano())

		msg, err := protocol.Parse(line)
		if err != nil {
			// Tolerate garbage from the wire — many real clients
			// occasionally emit empty lines or stray bytes.
			continue
		}
		c.server.messagesIn.Add(1)
		c.dispatch(msg)
	}
}

// writeLoop drains the outbound queue and writes to the socket. It
// exits when ctx is done *and* the queue has drained, or when a
// write fails.
//
// On exit it closes the underlying net.Conn so readLoop (which is
// otherwise parked in a long ReadBytes) unblocks immediately. The
// drain-then-close ordering is what lets handleQuit queue an ERROR,
// cancel the context, and still see the ERROR reach the client
// before the socket goes away.
func (c *Conn) writeLoop() {
	defer c.cancel(errors.New("write loop exited"))
	defer func() { _ = c.nc.Close() }()
	for {
		select {
		case <-c.ctx.Done():
			c.drainOutbound()
			return
		case msg, ok := <-c.out:
			if !ok {
				return
			}
			if !c.writeMessage(msg) {
				return
			}
		}
	}
}

// drainOutbound writes every message currently sitting in the
// outbound queue and then returns. It is non-blocking — once the
// queue is empty it stops.
func (c *Conn) drainOutbound() {
	for {
		select {
		case msg := <-c.out:
			if !c.writeMessage(msg) {
				return
			}
		default:
			return
		}
	}
}

// writeMessage encodes msg and writes it to the socket. Returns
// false on write error so the caller can stop.
func (c *Conn) writeMessage(msg *protocol.Message) bool {
	data, err := msg.Bytes()
	if err != nil {
		c.logger.Warn("encode error, dropping message", "error", err, "command", msg.Command)
		return true
	}
	_ = c.nc.SetWriteDeadline(c.server.now().Add(30 * time.Second))
	if _, err := c.nc.Write(data); err != nil {
		c.logger.Debug("write error", "error", err)
		return false
	}
	c.server.messagesOut.Add(1)
	return true
}

// pingLoop sends a PING when the connection has been idle for longer
// than the configured interval and disconnects when the idle time
// exceeds the configured timeout.
func (c *Conn) pingLoop() {
	interval := time.Duration(c.server.cfg.Server.Limits.PingIntervalSeconds) * time.Second
	timeout := time.Duration(c.server.cfg.Server.Limits.PingTimeoutSeconds) * time.Second
	if interval <= 0 {
		interval = 120 * time.Second
	}
	if timeout <= 0 {
		timeout = 240 * time.Second
	}
	// Poll roughly four times per interval; the cost is negligible
	// and it gives the timeout sub-interval resolution.
	tick := interval / 4
	if tick < time.Second {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			now := c.server.now()
			last := time.Unix(0, c.lastActivity.Load())
			idle := now.Sub(last)
			if idle >= timeout {
				c.sendError("Ping timeout: " + idle.Truncate(time.Second).String())
				c.cancel(errors.New("ping timeout"))
				return
			}
			if idle >= interval {
				c.send(&protocol.Message{
					Command: "PING",
					Params:  []string{c.server.cfg.Server.Name},
				})
			}
		}
	}
}

// send queues a message for delivery. If the queue is full the
// connection is killed (the historical "SendQ exceeded" behaviour).
//
// IRCv3 capability gating: when the recipient has negotiated
// server-time, we attach a fresh @time tag to a copy of m before
// queuing. The copy is per-recipient so a broadcast Message can be
// reused across many conns without leaking tags between them.
func (c *Conn) send(m *protocol.Message) {
	if c.capsAccepted["server-time"] {
		m = m.WithTag("time", c.server.now().UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	// IRCv3 account-tag: attach @account=<name> when the sender is
	// logged in and the recipient negotiated the cap. We extract the
	// nick from the message prefix and look up the user in the world.
	if c.capsAccepted["account-tag"] && m.Prefix != "" {
		if nick, _, ok := strings.Cut(m.Prefix, "!"); ok {
			if sender := c.server.world.FindByNick(nick); sender != nil && sender.Account != "" {
				m = m.WithTag("account", sender.Account)
			}
		}
	}
	select {
	case c.out <- m:
	default:
		c.logger.Debug("sendq exceeded, killing client")
		c.cancel(errors.New("sendq exceeded"))
	}
}

// sendError emits an ERROR line and is the canonical way to tell a
// client we are about to drop them. ERROR is unusual in that it has
// only a trailing parameter and never carries a numeric prefix.
func (c *Conn) sendError(reason string) {
	c.send(&protocol.Message{
		Command: "ERROR",
		Params:  []string{"Closing Link: " + c.remoteHost + " (" + reason + ")"},
	})
}

// isClosedNetError reports whether err is the "use of closed network
// connection" error or its modern equivalent. The standard library
// does not export a sentinel for this so we string-match, like every
// other Go network library.
func isClosedNetError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
