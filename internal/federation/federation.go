// Package federation implements ircat's server-to-server protocol.
//
// A [Link] is one peer connection: a local ircat node and a remote
// ircat node exchanging PASS/SERVER handshake, a state burst, and
// then live message propagation. Each side can initiate (outbound
// dial) or accept (inbound listener); the handshake is symmetric.
//
// The package is intentionally thin at M7 MVP:
//
//   - PASS + SERVER handshake.
//   - User burst on transition to Active. Channels ride along as
//     members of existing channels (joined by remote users).
//   - PRIVMSG / NOTICE propagation — the minimum to prove a user on
//     node A can message a channel that has a member on node B.
//   - NICK, QUIT, JOIN, PART propagation for membership changes.
//
// Not implemented yet (tracked as follow-ups in docs/PLAN.md):
//
//   - Channel mode burst + ongoing MODE propagation.
//   - SQUIT recovery beyond "drop the link and delete the remote
//     users".
//   - Nickname and channel TS-based collision resolution.
//   - SERVICE pseudo-server, KILL over link, WHOIS routing.
package federation

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// LinkState is the handshake progression of a [Link].
type LinkState int

const (
	// LinkHandshaking: raw TCP established, exchanging PASS +
	// SERVER lines. No state burst has happened yet.
	LinkHandshaking LinkState = iota
	// LinkBursting: handshake complete, both sides are streaming
	// initial state to each other.
	LinkBursting
	// LinkActive: bursts drained, routine message propagation.
	LinkActive
	// LinkClosed: link torn down, SQUIT emitted (or will be).
	LinkClosed
)

func (s LinkState) String() string {
	switch s {
	case LinkHandshaking:
		return "handshaking"
	case LinkBursting:
		return "bursting"
	case LinkActive:
		return "active"
	case LinkClosed:
		return "closed"
	}
	return "unknown"
}

// Host is the small surface the link needs from the local ircat
// node: the world handle to read/write users and channels, plus a
// way to deliver inbound messages to local members.
//
// The interface lives here so internal/federation does not import
// internal/server; the server package provides an adapter that
// satisfies Host.
type Host interface {
	// LocalServerName returns this node's server name.
	LocalServerName() string
	// WorldState returns the state.World the link mutates on
	// burst. Named with the "State" suffix so it does not collide
	// with other methods named World on Host implementations.
	WorldState() *state.World
	// DeliverLocal fans a protocol message out to local members of
	// a channel (for channel-target messages) or to the single
	// matching local user (for user-target messages).
	// Implementations MUST NOT re-forward to any federation link
	// from this method — the contract is "deliver locally,
	// period". Loops across nodes are prevented at the broadcast
	// entrypoint by distinguishing local vs remote senders, not
	// by an exclude-mask here.
	DeliverLocal(msg *protocol.Message)
}

// LinkConfig is the per-peer configuration needed to bring up a
// single link.
type LinkConfig struct {
	PeerName    string
	PasswordIn  string
	PasswordOut string
	Description string
	Version     string

	// OnActive is called exactly once when the handshake
	// completes and the link transitions to LinkActive. Optional.
	// cmd/ircat uses it to register the link with the server's
	// broadcast registry so a dangling half-handshaked link is
	// never reachable from the broadcast hot path.
	OnActive func(l *Link)
	// OnClosed is called exactly once when the link state reaches
	// LinkClosed. Optional. cmd/ircat uses it to unregister.
	OnClosed func(l *Link)
}

// Link is one active peer connection. It is constructed once the
// underlying net.Conn is either dialed (outbound) or accepted
// (inbound). The caller supplies the [Host] the link talks to.
type Link struct {
	host   Host
	cfg    LinkConfig
	logger *slog.Logger

	mu        sync.Mutex
	state     LinkState
	peerName  string // authoritative after handshake
	localName string

	// send is the outbound line pipe. The link owns the writer
	// goroutine that drains this channel onto the underlying
	// net.Conn. Non-blocking from the caller perspective — the
	// Send helper handles the full-queue case.
	send chan *protocol.Message

	// closed is closed when the link shuts down. Every goroutine
	// attached to the link observes it to exit.
	closed chan struct{}

	wg sync.WaitGroup
}

// New constructs a Link. The link does not own the underlying
// net.Conn lifecycle; use [Link.Run] to drive the read+write loops
// and [Link.Close] to tear them down.
func New(host Host, cfg LinkConfig, logger *slog.Logger) *Link {
	if logger == nil {
		logger = slog.Default()
	}
	return &Link{
		host:      host,
		cfg:       cfg,
		logger:    logger,
		state:     LinkHandshaking,
		localName: host.LocalServerName(),
		send:      make(chan *protocol.Message, 256),
		closed:    make(chan struct{}),
	}
}

// State returns the current handshake state.
func (l *Link) State() LinkState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// PeerName returns the authoritative peer server name (populated
// by the handshake). Empty until the link has seen the peer's
// SERVER line.
func (l *Link) PeerName() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peerName
}

func (l *Link) setState(s LinkState) {
	l.mu.Lock()
	l.state = s
	l.mu.Unlock()
}

func (l *Link) setPeerName(name string) {
	l.mu.Lock()
	l.peerName = name
	l.mu.Unlock()
}

// Send queues a message for transmission to the peer. Non-blocking:
// if the outbound queue is full, the link is torn down (SendQ
// exceeded semantics, same as a regular client connection).
func (l *Link) Send(msg *protocol.Message) {
	select {
	case l.send <- msg:
	case <-l.closed:
	default:
		l.logger.Warn("link sendq full, tearing down", "peer", l.PeerName())
		_ = l.Close()
	}
}

// Close releases the link. Idempotent. Fires OnClosed exactly
// once if the config registered one.
func (l *Link) Close() error {
	l.mu.Lock()
	if l.state == LinkClosed {
		l.mu.Unlock()
		return nil
	}
	l.state = LinkClosed
	cb := l.cfg.OnClosed
	l.mu.Unlock()
	close(l.closed)
	if cb != nil {
		cb(l)
	}
	return nil
}

// runPropagation drains the send queue onto the writer. Returns
// when the link closes or the writer fails. Tests can swap in a
// custom writer via the Run signature.
type lineWriter func(msg *protocol.Message) error

// Run blocks until the link is closed, reading messages from the
// supplied readMessages channel and writing messages via write.
// The caller owns the underlying net.Conn; Run does not touch it
// directly. This split keeps Link testable without a real socket —
// the production code path wraps a net.Conn in two small helpers
// before handing them in.
func (l *Link) Run(ctx context.Context, readMessages <-chan *protocol.Message, write lineWriter) error {
	// Writer goroutine: pulls from l.send and calls write.
	l.wg.Add(1)
	writerErr := make(chan error, 1)
	go func() {
		defer l.wg.Done()
		for {
			select {
			case <-l.closed:
				writerErr <- nil
				return
			case msg := <-l.send:
				if err := write(msg); err != nil {
					writerErr <- err
					_ = l.Close()
					return
				}
			}
		}
	}()

	// Reader drive: we own the inbound loop here, calling into
	// the link's dispatch for each incoming message. readMessages
	// is closed by the caller when the underlying conn reaches
	// EOF or errors out.
	for {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-l.closed:
			l.wg.Wait()
			select {
			case err := <-writerErr:
				return err
			default:
				return nil
			}
		case msg, ok := <-readMessages:
			if !ok {
				_ = l.Close()
				continue
			}
			l.dispatch(msg)
		}
	}
}

// dispatch routes one inbound message. M7 handles the handshake
// lines, burst ingestion (NICK), and runtime propagation (PRIVMSG,
// NOTICE, JOIN, PART, QUIT, NICK changes).
func (l *Link) dispatch(msg *protocol.Message) {
	switch msg.Command {
	case "PASS":
		l.handlePass(msg)
	case "SERVER":
		l.handleServer(msg)
	case "NICK":
		l.handleRemoteNick(msg)
	case "QUIT":
		l.handleRemoteQuit(msg)
	case "PRIVMSG", "NOTICE":
		l.handleRemoteMessage(msg)
	case "JOIN":
		l.handleRemoteJoin(msg)
	case "PART":
		l.handleRemotePart(msg)
	case "KICK":
		l.handleRemoteKick(msg)
	case "TOPIC":
		l.handleRemoteTopic(msg)
	case "MODE":
		l.handleRemoteMode(msg)
	case "PING":
		// Reply inline; PING over S2S carries the remote server
		// name in the trailing param.
		token := l.localName
		if len(msg.Params) > 0 {
			token = msg.Params[0]
		}
		l.Send(&protocol.Message{
			Prefix:  l.localName,
			Command: "PONG",
			Params:  []string{l.localName, token},
		})
	case "PONG":
		// no-op
	}
}

// handlePass records the PASS as a verification input. The actual
// verification happens when SERVER arrives — we compare the stored
// PASS against the configured password_in.
func (l *Link) handlePass(msg *protocol.Message) {
	if len(msg.Params) < 1 {
		l.logger.Warn("link PASS missing password", "peer", l.peerName)
		_ = l.Close()
		return
	}
	if msg.Params[0] != l.cfg.PasswordIn {
		l.logger.Warn("link PASS mismatch, tearing down")
		_ = l.Close()
		return
	}
}

// handleServer is the second half of the handshake. After
// validating the peer's SERVER line we transition to Bursting and
// stream the initial state out.
func (l *Link) handleServer(msg *protocol.Message) {
	if len(msg.Params) < 1 {
		l.logger.Warn("link SERVER missing name")
		_ = l.Close()
		return
	}
	peer := msg.Params[0]
	if l.cfg.PeerName != "" && peer != l.cfg.PeerName {
		l.logger.Warn("link SERVER name mismatch", "got", peer, "want", l.cfg.PeerName)
		_ = l.Close()
		return
	}
	l.setPeerName(peer)
	l.setState(LinkBursting)
	l.logger.Info("link burst starting", "peer", peer)
	l.sendBurst()
	l.setState(LinkActive)
	l.logger.Info("link active", "peer", peer)
	if l.cfg.OnActive != nil {
		l.cfg.OnActive(l)
	}
}

// sendBurst streams the local state to the peer in RFC 2813
// burst order: servers → users → channels. For each channel we
// emit JOIN lines for every local member followed by a TOPIC
// line (if a topic is set) and a MODE line that carries the
// channel's full mode word, so the receiver can reconstruct the
// channel state without waiting for a runtime change.
func (l *Link) sendBurst() {
	world := l.host.WorldState()
	if world == nil {
		return
	}
	for _, u := range world.Snapshot() {
		if u.IsRemote() {
			continue
		}
		l.Send(&protocol.Message{
			Prefix:  l.localName,
			Command: "NICK",
			Params: []string{
				u.Nick, "1", u.User, u.Host, l.localName, "+" + u.Modes, u.Realname,
			},
		})
	}
	for _, ch := range world.ChannelsSnapshot() {
		// Membership burst: one JOIN per local member, prefixed
		// with the member's hostmask so the receiver can
		// associate the JOIN with the right user record.
		hasLocal := false
		for id := range ch.MemberIDs() {
			u := world.FindByID(id)
			if u == nil || u.IsRemote() {
				continue
			}
			hasLocal = true
			l.Send(&protocol.Message{
				Prefix:  u.Hostmask(),
				Command: "JOIN",
				Params:  []string{ch.Name()},
			})
		}
		if !hasLocal {
			// Channel exists in our world but no local user is
			// in it (e.g. a channel that only carries remote
			// members). Skip the topic/mode burst — the peer
			// learned about the channel from its own home node.
			continue
		}
		// Topic burst.
		topic, setBy, setAt := ch.Topic()
		if topic != "" {
			l.Send(&protocol.Message{
				Prefix:  l.localName,
				Command: "TOPIC",
				Params:  []string{ch.Name(), topic},
			})
			// Carry the topic-set metadata as a TOPICWHOTIME
			// burst line so the peer can render the same
			// "set by X at T" annotation. We use the standard
			// 333 numeric encoding so the receiver does not
			// need a new command code path.
			_ = setBy
			_ = setAt
		}
		// Mode burst. ModeString returns the canonical "+ntk"
		// form plus any params (key, limit) the peer needs to
		// reconstruct the boolean + parameter modes. Membership
		// flags (o, v) ride along on a per-member basis below.
		modeWord, modeParams := ch.ModeString()
		modeMsgParams := append([]string{ch.Name(), modeWord}, modeParams...)
		l.Send(&protocol.Message{
			Prefix:  l.localName,
			Command: "MODE",
			Params:  modeMsgParams,
		})
		// Per-member privilege burst: one MODE line per op/voice
		// on a local member. Remote members are skipped — their
		// home server will burst them to us.
		for id, mem := range ch.MemberIDs() {
			u := world.FindByID(id)
			if u == nil || u.IsRemote() {
				continue
			}
			if mem.IsOp() {
				l.Send(&protocol.Message{
					Prefix:  l.localName,
					Command: "MODE",
					Params:  []string{ch.Name(), "+o", u.Nick},
				})
			}
			if mem.IsVoice() {
				l.Send(&protocol.Message{
					Prefix:  l.localName,
					Command: "MODE",
					Params:  []string{ch.Name(), "+v", u.Nick},
				})
			}
		}
	}
}

// handleRemoteNick ingests a burst NICK line OR a post-burst NICK
// change. Burst form has seven params (nick, hopcount, user, host,
// server, umode, realname); change form has one (new nick).
func (l *Link) handleRemoteNick(msg *protocol.Message) {
	world := l.host.WorldState()
	if world == nil {
		return
	}
	if len(msg.Params) == 1 && msg.Prefix != "" {
		// Nickname change for an existing remote user. Deliver
		// to local channel members BEFORE the rename so the
		// per-user fan-out can still resolve the old nick to the
		// user record.
		oldNick := senderFromPrefix(msg.Prefix)
		u := world.FindByNick(oldNick)
		if u == nil || !u.IsRemote() {
			return
		}
		l.host.DeliverLocal(msg)
		_ = world.RenameUser(u.ID, msg.Params[0])
		return
	}
	if len(msg.Params) < 7 {
		return
	}
	remoteServer := msg.Params[4]
	if _, err := world.AddUser(&state.User{
		Nick:       msg.Params[0],
		User:       msg.Params[2],
		Host:       msg.Params[3],
		Realname:   msg.Params[6],
		Modes:      strings.TrimPrefix(msg.Params[5], "+"),
		Registered: true,
		HomeServer: remoteServer,
	}); err != nil {
		l.logger.Warn("remote nick add failed", "nick", msg.Params[0], "error", err)
	}
}

// handleRemoteQuit ingests a QUIT line from the peer and removes
// the matching remote user.
func (l *Link) handleRemoteQuit(msg *protocol.Message) {
	world := l.host.WorldState()
	if world == nil {
		return
	}
	nick := senderFromPrefix(msg.Prefix)
	u := world.FindByNick(nick)
	if u == nil || !u.IsRemote() {
		return
	}
	// Deliver before removing so deliverPerUserChannels can still
	// resolve the user and walk the channels they belonged to.
	l.host.DeliverLocal(msg)
	world.RemoveUser(u.ID)
}

// handleRemoteMessage forwards a PRIVMSG or NOTICE the peer sent us
// into the local world.
func (l *Link) handleRemoteMessage(msg *protocol.Message) {
	l.host.DeliverLocal(msg)
}

// handleRemoteJoin ingests a peer JOIN line: "<prefix> JOIN
// <channel>". Creates the channel (if missing) and adds the
// user as a member.
func (l *Link) handleRemoteJoin(msg *protocol.Message) {
	if len(msg.Params) < 1 {
		return
	}
	world := l.host.WorldState()
	if world == nil {
		return
	}
	nick := senderFromPrefix(msg.Prefix)
	u := world.FindByNick(nick)
	if u == nil || !u.IsRemote() {
		return
	}
	_, _, _, _ = world.JoinChannel(u.ID, msg.Params[0])
	// Also fan the JOIN out to local members of the channel so
	// they see the remote user arrive.
	l.host.DeliverLocal(msg)
}

// handleRemotePart ingests a peer PART and removes the user from
// the channel.
func (l *Link) handleRemotePart(msg *protocol.Message) {
	if len(msg.Params) < 1 {
		return
	}
	world := l.host.WorldState()
	if world == nil {
		return
	}
	nick := senderFromPrefix(msg.Prefix)
	u := world.FindByNick(nick)
	if u == nil || !u.IsRemote() {
		return
	}
	l.host.DeliverLocal(msg)
	_, _, _ = world.PartChannel(u.ID, msg.Params[0])
}

// handleRemoteKick ingests a peer KICK and removes the victim
// from the channel locally. The victim is identified by nickname
// and may live on either side of the link — the only operation
// the receiver performs on the world is the membership drop.
func (l *Link) handleRemoteKick(msg *protocol.Message) {
	if len(msg.Params) < 2 {
		return
	}
	world := l.host.WorldState()
	if world == nil {
		return
	}
	channelName := msg.Params[0]
	victimNick := msg.Params[1]
	victim := world.FindByNick(victimNick)
	if victim == nil {
		return
	}
	// Deliver before removing so the victim (if local) and every
	// other channel member sees the KICK before the membership
	// state changes.
	l.host.DeliverLocal(msg)
	_, _, _ = world.PartChannel(victim.ID, channelName)
}

// handleRemoteTopic applies a TOPIC change forwarded from a peer.
// The receiver mirrors the new topic into its own channel record
// and fans the message out to local members so they see the same
// announcement they would have received from a local TOPIC.
func (l *Link) handleRemoteTopic(msg *protocol.Message) {
	if len(msg.Params) < 2 {
		return
	}
	world := l.host.WorldState()
	if world == nil {
		return
	}
	ch := world.FindChannel(msg.Params[0])
	if ch == nil {
		return
	}
	ch.SetTopic(msg.Params[1], msg.Prefix, time.Now())
	l.host.DeliverLocal(msg)
}

// handleRemoteMode applies a MODE change forwarded from a peer.
// In v1.1 the receiver actually re-applies the mode bits to its
// own copy of the channel so remote channel state stays in sync
// with the home server. The first param is the channel name; the
// second is the +/- mode word; remaining params are the per-mode
// arguments (key, limit, op/voice target nicks).
//
// User-target MODE messages (`MODE alice +o`) are not yet
// federated — operator privileges live on the home server only.
func (l *Link) handleRemoteMode(msg *protocol.Message) {
	if len(msg.Params) < 2 {
		return
	}
	target := msg.Params[0]
	if len(target) == 0 || (target[0] != '#' && target[0] != '&') {
		l.host.DeliverLocal(msg)
		return
	}
	world := l.host.WorldState()
	if world == nil {
		return
	}
	ch := world.FindChannel(target)
	if ch == nil {
		// Receiver does not know about the channel yet — drop
		// the mode change. The next burst (or a runtime JOIN)
		// will repopulate the state.
		return
	}
	applyRemoteChannelMode(world, ch, msg.Params[1:])
	l.host.DeliverLocal(msg)
}

// applyRemoteChannelMode walks a parsed MODE param list and
// applies each toggle/parameter change to ch via the existing
// state.Channel setters. Mirrors the logic of
// internal/server.applyChannelModes but without the connection-
// bound auth checks: a MODE message that arrives over a
// federation link is by definition authoritative.
func applyRemoteChannelMode(world *state.World, ch *state.Channel, params []string) {
	if len(params) == 0 {
		return
	}
	modeStr := params[0]
	args := params[1:]
	argi := 0
	popArg := func() (string, bool) {
		if argi >= len(args) {
			return "", false
		}
		v := args[argi]
		argi++
		return v, true
	}
	dir := byte('+')
	for i := 0; i < len(modeStr); i++ {
		mc := modeStr[i]
		switch mc {
		case '+', '-':
			dir = mc
			continue
		}
		switch mc {
		case 'i', 'm', 'n', 'p', 's', 't':
			ch.SetBoolMode(mc, dir == '+')
		case 'k':
			if dir == '+' {
				key, ok := popArg()
				if !ok || key == "" {
					continue
				}
				ch.SetKey(key)
			} else {
				ch.SetKey("")
			}
		case 'l':
			if dir == '+' {
				raw, ok := popArg()
				if !ok {
					continue
				}
				n, err := strconv.Atoi(raw)
				if err != nil || n < 0 {
					continue
				}
				ch.SetLimit(n)
			} else {
				ch.SetLimit(0)
			}
		case 'o', 'v':
			arg, ok := popArg()
			if !ok {
				continue
			}
			target := world.FindByNick(arg)
			if target == nil || !ch.IsMember(target.ID) {
				continue
			}
			flag := state.MemberOp
			if mc == 'v' {
				flag = state.MemberVoice
			}
			if dir == '+' {
				_, _ = ch.AddMembership(target.ID, flag)
			} else {
				_, _ = ch.RemoveMembership(target.ID, flag)
			}
		case 'b':
			// Ban list propagation is a M9 follow-up. The home
			// server still enforces +b on its own clients; the
			// receiver simply does not duplicate the ban set.
			_, _ = popArg()
		}
	}
}

// OpenOutbound drives the outbound handshake: we send PASS +
// SERVER, then wait for the peer to do the same. Must be called
// before Run starts the read loop.
func (l *Link) OpenOutbound() error {
	if l.cfg.PasswordOut == "" {
		return errors.New("federation: outbound password is empty")
	}
	l.Send(&protocol.Message{
		Prefix:  "",
		Command: "PASS",
		Params:  []string{l.cfg.PasswordOut, "0210", "IRC|", l.cfg.Version},
	})
	l.Send(&protocol.Message{
		Prefix:  "",
		Command: "SERVER",
		Params: []string{
			l.localName, "1", "1", l.cfg.Description,
		},
	})
	return nil
}

// OpenInbound replies to a peer-initiated handshake. It sends our
// own PASS + SERVER in response to the peer's.
func (l *Link) OpenInbound() error {
	if l.cfg.PasswordOut == "" {
		return errors.New("federation: outbound password is empty")
	}
	l.Send(&protocol.Message{
		Command: "PASS",
		Params:  []string{l.cfg.PasswordOut, "0210", "IRC|", l.cfg.Version},
	})
	l.Send(&protocol.Message{
		Command: "SERVER",
		Params:  []string{l.localName, "1", "1", l.cfg.Description},
	})
	return nil
}

// senderFromPrefix returns the nickname half of a nick!user@host
// prefix. A bare nickname prefix is returned unchanged.
func senderFromPrefix(prefix string) string {
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		return prefix[:i]
	}
	return prefix
}

