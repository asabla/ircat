package server

import (
	"context"
	"strconv"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// fedLinkSender is the tiny surface the server needs from a
// federation link: a way to enqueue a message for the peer. The
// federation package's Link type satisfies this directly.
//
// The interface lives here so the server package does not import
// internal/federation from every file — only the cmd/ircat wiring
// and the tests need the concrete type.
type fedLinkSender interface {
	Send(*protocol.Message)
}

// RegisterLink associates a link with its peer name. Called by
// cmd/ircat (or a future federation listener) after the link
// handshake completes.
func (s *Server) RegisterLink(peerName string, link fedLinkSender) {
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	if s.fedLinks == nil {
		s.fedLinks = make(map[string]fedLinkSender)
	}
	s.fedLinks[peerName] = link
}

// UnregisterLink drops the link from the registry. Called on link
// close. Also drops every channel subscription the peer had so a
// reconnect starts with a clean slate.
func (s *Server) UnregisterLink(peerName string) {
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	delete(s.fedLinks, peerName)
	delete(s.fedSubs, peerName)
}

// SubscribePeerToChannel records that peerName has been told
// about the named channel and should receive subsequent runtime
// events for it under the subscription broadcast mode. Idempotent.
//
// Called from sendBurst when we tell a peer about a local
// channel, and from the federation Link's runtime ingestion
// path when a remote JOIN/MODE/TOPIC/PART tells us the peer
// already knows about the channel.
func (s *Server) SubscribePeerToChannel(peerName, channelName string) {
	if peerName == "" || channelName == "" {
		return
	}
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	if s.fedSubs == nil {
		s.fedSubs = make(map[string]map[string]bool)
	}
	subs, ok := s.fedSubs[peerName]
	if !ok {
		subs = make(map[string]bool)
		s.fedSubs[peerName] = subs
	}
	subs[channelName] = true
}

// DropLocalUser disconnects the named local user with reason.
// Implements internal/federation.Host. Called by the federation
// Link in two scenarios:
//
//   - A remote KILL targets a user that happens to live on this
//     node — the kill should also drop the live conn.
//   - A nick collision lost the TS tiebreaker on this side — the
//     world record is already gone, so we walk the conn registry
//     directly by nick and cancel any matching conn.
//
// The walk-by-nick approach is safe even when the world record
// has already been removed: every Conn keeps a pointer to the
// state.User it was registered with, so the registry lookup
// works regardless of world state.
func (s *Server) DropLocalUser(nick, reason string) {
	if reason == "" {
		reason = "Killed"
	}
	s.connsMu.RLock()
	var target *Conn
	for _, c := range s.conns {
		if c.user != nil && c.user.Nick == nick {
			target = c
			break
		}
	}
	s.connsMu.RUnlock()
	if target == nil {
		return
	}
	target.quitReason = reason
	target.sendError(reason)
	target.cancel(context.Canceled)
}

// squitSeenKey is the dedup key for the SQUIT loop guard.
type squitSeenKey struct {
	peer   string
	reason string
}

// squitSeenRecently reports whether a (peer, reason) tuple has
// been observed in the last squitSeenTTL. Records the
// observation as a side effect when it is fresh, so the next
// call within the TTL returns true. Stale entries are dropped
// opportunistically on every call to keep the map small
// without a separate sweeper goroutine.
func (s *Server) squitSeenRecently(peer, reason string) bool {
	key := squitSeenKey{peer: peer, reason: reason}
	now := time.Now()
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	if s.squitSeen == nil {
		s.squitSeen = make(map[squitSeenKey]time.Time)
	}
	// Drop stale entries opportunistically.
	for k, exp := range s.squitSeen {
		if now.After(exp) {
			delete(s.squitSeen, k)
		}
	}
	if exp, ok := s.squitSeen[key]; ok && now.Before(exp) {
		return true
	}
	s.squitSeen[key] = now.Add(squitSeenTTL)
	return false
}

// squitSeenTTL is how long a (peer, reason) seen-set entry
// stays in the dedup map. Picked long enough to cover the
// fan-out propagation delay across a 5-hop mesh on a busy
// host, short enough to never block a legitimate
// reconnect-then-disconnect sequence.
const squitSeenTTL = 5 * time.Second

// HandleSquit performs the SQUIT recovery flow when a federation
// peer drops: every user whose HomeServer matches peerName is
// removed from the world, and a synthetic QUIT broadcast goes
// out to every local member of every channel the dropped user
// belonged to so local clients see the disappearance with the
// right hostmasks. The reason is forwarded as the QUIT trailing
// param so operators can distinguish a planned shutdown from a
// crash.
//
// HandleSquit also forwards :localServer SQUIT peerName :reason
// to every remaining federation link so the rest of the mesh
// learns about the loss without needing each node to detect it
// independently. A small (peer, reason) seen-set with a few-
// second TTL guards against a fan-out loop in a >3-node mesh:
// every node forwards SQUIT exactly once and ignores duplicates.
//
// This is the receiver-of-loss path. It does NOT call
// UnregisterLink — the supervisor's OnClosed callback is the
// piece that drops the link from the broadcast registry, and it
// runs separately. HandleSquit is safe to call even if the link
// has already been unregistered.
func (s *Server) HandleSquit(peerName, reason string) {
	if peerName == "" {
		return
	}
	if reason == "" {
		reason = "Net split"
	}
	// Loop guard: drop SQUITs we have seen recently for the
	// same (peer, reason) tuple. The TTL is short so a
	// reconnect-then-disconnect sequence is not blocked, and
	// the key includes the reason so two distinct disconnect
	// events for the same peer still propagate.
	if s.squitSeenRecently(peerName, reason) {
		return
	}
	// Snapshot the world under the world lock so a concurrent
	// runtime announce cannot race the cleanup. Walk the snapshot
	// out of band — the per-channel broadcasts re-acquire the
	// world locks they need on their own.
	users := s.world.Snapshot()
	for _, u := range users {
		if u.HomeServer != peerName {
			continue
		}
		quitMsg := &protocol.Message{
			Prefix:  u.Hostmask(),
			Command: "QUIT",
			Params:  []string{reason},
		}
		// Fan the synthetic QUIT to every local member of every
		// channel this user belonged to. We use the same
		// per-user dedup helper as a real QUIT path so a peer
		// in two shared channels does not receive the message
		// twice.
		s.deliverPerUserChannels(quitMsg)
		uCopy := u
		s.recordWhowas(&uCopy)
		// Drop the user from the world. RemoveUser walks the
		// channel set and unhooks them.
		s.world.RemoveUser(u.ID)
	}
	// Forward SQUIT to every remaining peer.
	squitMsg := &protocol.Message{
		Prefix:  s.cfg.Server.Name,
		Command: "SQUIT",
		Params:  []string{peerName, reason},
	}
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	for name, link := range s.fedLinks {
		if name == peerName || link == nil {
			continue
		}
		link.Send(squitMsg)
	}
}

// LinkFor returns the active link sender for peer name, or nil.
func (s *Server) LinkFor(peerName string) fedLinkSender {
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	return s.fedLinks[peerName]
}

// FederationLinkRow is the row shape returned by
// FederationSnapshot. Implements the four getters
// internal/dashboard.FederationLinkRow expects via value
// methods so the dashboard package never has to import
// internal/server or internal/federation for the type.
type FederationLinkRow struct {
	peer        string
	state       string
	description string
	subscribed  []string
}

func (r FederationLinkRow) Peer() string         { return r.peer }
func (r FederationLinkRow) State() string        { return r.state }
func (r FederationLinkRow) Description() string  { return r.description }
func (r FederationLinkRow) Subscribed() []string { return r.subscribed }

// dashboardLinkRow is the wider interface used to type-assert
// inside FederationSnapshot. The federation.Link struct
// satisfies it via its existing methods, but we keep the
// assertion local so the server package never imports
// internal/federation just for the type.
type dashboardLinkRow interface {
	State() string
	PeerName() string
}

// FederationSnapshot returns a slice of FederationLinkRow
// describing every currently registered federation link plus
// its known channel subscriptions. The result is a snapshot
// taken under the federation registry RLock, so callers can
// walk the slice without holding any server-side locks.
//
// Implements internal/dashboard.FederationLister via duck-
// typing on the FederationLinkRow getter methods. The dashboard
// package consumes the returned slice as
// []internal/dashboard.FederationLinkRow; the conversion is
// handled at the call site so server stays independent of
// dashboard.
func (s *Server) FederationSnapshot() []FederationLinkRow {
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	out := make([]FederationLinkRow, 0, len(s.fedLinks))
	for name, link := range s.fedLinks {
		row := FederationLinkRow{peer: name, state: "active"}
		// link is the unexported fedLinkSender alias which only
		// exposes Send. The concrete federation.Link satisfies
		// the wider dashboardLinkRow interface above, so we
		// type-assert here for the extra fields. A future
		// refactor can fold this into the registry directly.
		if rich, ok := link.(dashboardLinkRow); ok {
			row.state = rich.State()
		}
		if subs, ok := s.fedSubs[name]; ok {
			for chName := range subs {
				row.subscribed = append(row.subscribed, chName)
			}
		}
		out = append(out, row)
	}
	return out
}

// ----- federation.Host implementation ---------------------------
//
// Server satisfies internal/federation.Host so a Link can be
// constructed against it directly (no adapter type). The three
// methods are intentionally small — everything else flows through
// the existing World and broadcast machinery.

// LocalServerName implements federation.Host.
func (s *Server) LocalServerName() string { return s.cfg.Server.Name }

// World returns the state.World handle (the same one other server
// code uses). Implements federation.Host.
//
// The api package already exposes a method with the same name and
// signature, so this is a no-op gain — we keep the method set
// consistent and let Go's structural typing hand the Server to the
// federation.Link constructor.
func (s *Server) WorldState() *state.World { return s.world }

// DeliverLocal implements federation.Host. Called by a Link when
// it receives a message it wants broadcast to local members only.
// This method MUST NOT re-forward to any federation link — the
// contract is "deliver locally, period". Loops across nodes are
// prevented at the broadcast entrypoint by treating local origins
// and remote origins differently, not by an exclude-mask here.
func (s *Server) DeliverLocal(msg *protocol.Message) {
	switch msg.Command {
	case "PRIVMSG", "NOTICE":
		if len(msg.Params) < 1 {
			return
		}
		target := msg.Params[0]
		if len(target) > 0 && (target[0] == '#' || target[0] == '&') {
			ch := s.world.FindChannel(target)
			if ch == nil {
				return
			}
			s.deliverChannelLocalOnly(ch, msg)
			return
		}
		u := s.world.FindByNick(target)
		if u == nil || u.IsRemote() {
			return
		}
		if c := s.connFor(u.ID); c != nil {
			c.send(msg)
		}
	case "JOIN", "PART", "KICK", "TOPIC", "MODE":
		if len(msg.Params) < 1 {
			return
		}
		ch := s.world.FindChannel(msg.Params[0])
		if ch == nil {
			return
		}
		s.deliverChannelLocalOnly(ch, msg)
	case "QUIT", "NICK":
		// QUIT and post-registration NICK have no channel target;
		// they apply to every channel the sender is in. Fan out
		// to every local member of every shared channel.
		s.deliverPerUserChannels(msg)
	}
}

// deliverPerUserChannels delivers msg to every local member of
// every channel the message's sender (identified by the prefix)
// currently belongs to. Used for QUIT and NICK-change events
// forwarded from federation so local peers see them just like a
// local QUIT/NICK.
func (s *Server) deliverPerUserChannels(msg *protocol.Message) {
	senderNick := senderNickFromPrefix(msg.Prefix)
	u := s.world.FindByNick(senderNick)
	if u == nil {
		return
	}
	seen := make(map[state.UserID]bool)
	for _, ch := range s.world.UserChannels(u.ID) {
		for id := range ch.MemberIDs() {
			if seen[id] || id == u.ID {
				continue
			}
			peer := s.world.FindByID(id)
			if peer == nil || peer.IsRemote() {
				continue
			}
			seen[id] = true
			if c := s.connFor(id); c != nil {
				c.send(msg)
			}
		}
	}
}

// senderNickFromPrefix extracts the nick half of a "nick!user@host"
// prefix. Local helper so this file does not import strings just
// for one lookup.
func senderNickFromPrefix(prefix string) string {
	for i := 0; i < len(prefix); i++ {
		if prefix[i] == '!' {
			return prefix[:i]
		}
	}
	return prefix
}

// deliverChannelLocalOnly fans msg out to local conns and bots in
// ch, skipping remote members entirely (we never forward back over
// federation from a delivery that arrived over federation).
func (s *Server) deliverChannelLocalOnly(ch *state.Channel, msg *protocol.Message) {
	for id := range ch.MemberIDs() {
		u := s.world.FindByID(id)
		if u == nil || u.IsRemote() {
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

// broadcastToChannelFederated is the federation-aware wrapper
// around broadcastToChannel. The base broadcastToChannel handles
// local delivery; this helper additionally forwards the message
// to the appropriate federation peers based on the configured
// broadcast mode.
//
// Routing rules in v1.1 (federation.broadcast_mode):
//
//   - "subscription" (default): JOIN is fanned out to every peer
//     because it is the discovery message that establishes a
//     remote subscription. Every other channel command (PRIVMSG,
//     NOTICE, PART, KICK, TOPIC, MODE) routes only to peers that
//     already have at least one member in the channel, dedup'd
//     by HomeServer so a peer with two members in the channel
//     receives the message exactly once.
//   - "fanout": every event goes to every peer regardless. v1.0
//     behaviour, retained for one minor cycle behind a config
//     knob so a regression can be flipped without a redeploy.
func (s *Server) broadcastToChannelFederated(ch *state.Channel, msg *protocol.Message, exceptID state.UserID, includeSelf bool) {
	s.broadcastToChannel(ch, msg, exceptID, includeSelf)

	if s.fedBroadcastMode() == "fanout" {
		s.forwardToAllLinks(msg)
		return
	}
	// Subscription mode. JOIN is the establishment message — it
	// must reach every peer so the channel materializes there
	// and a future PRIVMSG can route through the new
	// subscription. We also self-subscribe each peer on the
	// way out: we just told them about this channel, so from
	// our point of view they are now a subscriber and the next
	// non-JOIN event for the channel routes back to them
	// without waiting for the receiver-side handleRemoteJoin
	// to subscribe in the other direction.
	if msg.Command == "JOIN" {
		s.fedMu.RLock()
		peers := make([]string, 0, len(s.fedLinks))
		for name := range s.fedLinks {
			peers = append(peers, name)
		}
		s.fedMu.RUnlock()
		for _, name := range peers {
			s.SubscribePeerToChannel(name, ch.Name())
		}
		s.forwardToAllLinks(msg)
		return
	}
	s.forwardChannelToSubscribed(ch, msg)
}

// fedBroadcastMode returns the configured federation broadcast
// mode, normalized and defaulted to "subscription".
func (s *Server) fedBroadcastMode() string {
	mode := s.cfg.Federation.BroadcastMode
	if mode == "" {
		return "subscription"
	}
	return mode
}

// forwardChannelToSubscribed sends msg to every peer subscribed
// to ch.Name(). The subscription set is built from two sources:
//
//   - Bursts we sent: when sendBurst delivers channel state to a
//     peer, the supervisor calls SubscribePeerToChannel so we
//     remember that the peer knows about the channel even though
//     they may not yet have any members in it.
//   - Remote JOINs: when the federation Link processes a remote
//     JOIN/MODE/TOPIC/PART for a channel, the receiver calls
//     SubscribePeerToChannel so we remember that the peer cares.
//
// Used by the subscription broadcast mode for every channel
// event except JOIN.
func (s *Server) forwardChannelToSubscribed(ch *state.Channel, msg *protocol.Message) {
	channelName := ch.Name()
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	if len(s.fedLinks) == 0 {
		return
	}
	for peerName, subs := range s.fedSubs {
		if !subs[channelName] {
			continue
		}
		if link := s.fedLinks[peerName]; link != nil {
			link.Send(msg)
		}
	}
}

// announceUserToFederation broadcasts a burst-form NICK line for a
// newly-registered local user so every peer can add them to their
// world. The eight-param shape matches what sendBurst emits at
// link-up time, including the TS so collision resolution works
// for users that registered after the burst.
func (s *Server) announceUserToFederation(u *state.User) {
	if u == nil {
		return
	}
	msg := &protocol.Message{
		Prefix:  s.cfg.Server.Name,
		Command: "NICK",
		Params: []string{
			u.Nick, "1", u.User, u.Host, s.cfg.Server.Name, "+" + u.Modes,
			strconv.FormatInt(u.TS, 10),
			u.Realname, // trailing — must be last
		},
	}
	s.forwardToAllLinks(msg)
}

// forwardToAllLinks sends msg to every active federation link.
// Used for events that have no single channel target — NICK
// changes and QUIT — because every peer's world holds a copy of
// the affected user (the burst sent the full local user set).
func (s *Server) forwardToAllLinks(msg *protocol.Message) {
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	for _, link := range s.fedLinks {
		if link != nil {
			link.Send(msg)
		}
	}
}
