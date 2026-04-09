package server

import (
	"github.com/asabla/ircat/internal/protocol"
)

// handleAway implements AWAY (RFC 2812 §4.1).
//
//	AWAY [ :<message> ]
//
// With a message: marks the user as away. With no message (or
// an empty trailing): clears the away marker. The server
// confirms with 305 RPL_UNAWAY (clear) or 306 RPL_NOWAWAY
// (set). Subsequent direct PRIVMSGs to the user trigger a 301
// RPL_AWAY back to the sender — handled in the PRIVMSG path.
//
// AWAY does not require operator privileges and is intended
// to be cheap: every modern client sets/clears it on a tab-
// away timer, so the handler does the smallest amount of work
// possible — a single field write under the conn's user
// pointer plus one numeric reply.
func (c *Conn) handleAway(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	nick := c.user.Nick

	// AWAY with no params (or empty trailing) clears the marker.
	var away string
	if t, ok := m.Trailing(); ok {
		away = t
	}
	if away == "" {
		c.user.Away = ""
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_UNAWAY,
			"You are no longer marked as being away"))
		return
	}

	// Cap the away message at the configured AwayLength so a
	// pathologically long message cannot blow the line size.
	if maxLen := c.server.cfg.Server.Limits.AwayLength; maxLen > 0 && len(away) > maxLen {
		away = away[:maxLen]
	}
	c.user.Away = away
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_NOWAWAY,
		"You have been marked as being away"))
}
