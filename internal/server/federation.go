package server

import (
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
// close.
func (s *Server) UnregisterLink(peerName string) {
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	delete(s.fedLinks, peerName)
}

// LinkFor returns the active link sender for peer name, or nil.
func (s *Server) LinkFor(peerName string) fedLinkSender {
	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	return s.fedLinks[peerName]
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
// to every active federation peer.
//
// For M7 MVP we send to every peer rather than only peers with a
// member in the channel: dedup-by-channel-member has a chicken-
// and-egg problem (we cannot forward a JOIN until a remote user
// has already joined, which never happens). Receiving peers that
// have no local members for the channel simply drop the message
// in their DeliverLocal path, so the wire cost is the only loss.
// A future commit can introduce subscription-based routing.
func (s *Server) broadcastToChannelFederated(ch *state.Channel, msg *protocol.Message, exceptID state.UserID, includeSelf bool) {
	s.broadcastToChannel(ch, msg, exceptID, includeSelf)
	s.forwardToAllLinks(msg)
}

// announceUserToFederation broadcasts a burst-form NICK line for a
// newly-registered local user so every peer can add them to their
// world. The seven-param shape matches what sendBurst emits at
// link-up time, so the receiving Link.handleRemoteNick path
// reuses the same code that ingests bursted users.
func (s *Server) announceUserToFederation(u *state.User) {
	if u == nil {
		return
	}
	modes := u.Modes
	msg := &protocol.Message{
		Prefix:  s.cfg.Server.Name,
		Command: "NICK",
		Params: []string{
			u.Nick, "1", u.User, u.Host, s.cfg.Server.Name, "+" + modes, u.Realname,
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
