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
// excludeOrigin is the peer server name the message came from, so
// the fan-out never forwards back over the same link.
func (s *Server) DeliverLocal(msg *protocol.Message, excludeOrigin string) {
	switch msg.Command {
	case "PRIVMSG", "NOTICE", "JOIN", "PART":
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
		// User-target form: look up the local nick.
		u := s.world.FindByNick(target)
		if u == nil || u.IsRemote() {
			return
		}
		if c := s.connFor(u.ID); c != nil {
			c.send(msg)
		}
	}
	_ = excludeOrigin
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
// local delivery; this helper additionally forwards to every
// distinct remote server that hosts a member of the channel,
// sending the message over each link exactly once so loops are
// impossible even when multiple remote users share a home server.
func (s *Server) broadcastToChannelFederated(ch *state.Channel, msg *protocol.Message, exceptID state.UserID, includeSelf bool) {
	s.broadcastToChannel(ch, msg, exceptID, includeSelf)

	s.fedMu.RLock()
	defer s.fedMu.RUnlock()
	if len(s.fedLinks) == 0 {
		return
	}
	seen := make(map[string]bool, len(s.fedLinks))
	for id := range ch.MemberIDs() {
		u := s.world.FindByID(id)
		if u == nil || !u.IsRemote() {
			continue
		}
		if seen[u.HomeServer] {
			continue
		}
		seen[u.HomeServer] = true
		if link := s.fedLinks[u.HomeServer]; link != nil {
			link.Send(msg)
		}
	}
}
