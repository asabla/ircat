package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
)

// handleWallops implements WALLOPS (RFC 2812 §4.7).
//
//	WALLOPS :<text>
//
// Operator-only command that broadcasts a message to every user on
// the network with user-mode +w set. The message is forwarded across
// every federation link so the broadcast reaches all nodes; each
// node fans it out to its own local +w members.
//
// The RFC restricts WALLOPS to operators (and to server-to-server
// traffic). We enforce the operator check the same way KILL does.
func (c *Conn) handleWallops(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if !strings.ContainsRune(c.user.Modes, 'o') {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator"))
		return
	}
	if len(m.Params) < 1 || m.Params[0] == "" {
		c.sendNeedMoreParams("WALLOPS")
		return
	}
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "WALLOPS",
		Params:  []string{m.Params[0]},
	}
	c.server.deliverWallopsLocal(msg)
	c.server.forwardToAllLinks(msg)
}

// deliverWallopsLocal fans the WALLOPS msg out to every local user
// who has +w set in their user modes. The originator is not excluded
// — operators sending a WALLOPS see their own broadcast just like
// every other +w user, which matches the behaviour of every
// production ircd.
func (s *Server) deliverWallopsLocal(msg *protocol.Message) {
	for _, snap := range s.world.Snapshot() {
		if snap.IsRemote() {
			continue
		}
		if !strings.ContainsRune(snap.Modes, 'w') {
			continue
		}
		if c := s.connFor(snap.ID); c != nil {
			c.send(msg)
		}
	}
}
