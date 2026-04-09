package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
)

// handleTrace implements TRACE (RFC 2812 §3.4.8).
//
//	TRACE [ <target> ]
//
// Operator-only. Walks the local connection table and emits one
// 205 RPL_TRACEUSER line per registered user, one 204
// RPL_TRACEOPERATOR for each +o user, one 206 RPL_TRACESERVER per
// active federation link, and a final 262 RPL_TRACEEND. The
// hopcount/class fields are reported as zero/"users" since this
// daemon does not bucket connections into traffic classes.
//
// The optional target parameter is accepted but ignored — TRACE
// only reports the local node; remote tracing would require routing
// the request across the mesh, which we deliberately do not do.
func (c *Conn) handleTrace(m *protocol.Message) {
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

	for _, u := range c.server.world.Snapshot() {
		if u.IsRemote() {
			continue
		}
		// "User <class> <nick>"
		c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_TRACEUSER,
			"User", "users", u.Nick))
		if strings.ContainsRune(u.Modes, 'o') {
			// "Oper <class> <nick>"
			c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_TRACEOPERATOR,
				"Oper", "users", u.Nick))
		}
	}
	for _, row := range c.server.FederationSnapshot() {
		// "Serv <class> 0S 0C <server> *!*@<server> V0"
		c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_TRACESERVER,
			"Serv", "links", "0S", "0C", row.Peer(), "*!*@"+row.Peer(), "V0"))
	}
	c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_TRACEEND,
		srv, "ircat.0", "End of TRACE"))
}
