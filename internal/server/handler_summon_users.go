package server

import "github.com/asabla/ircat/internal/protocol"

// handleSummon implements SUMMON (RFC 2812 §4.5).
//
//	SUMMON <user> [ <target> [ <channel> ] ]
//
// SUMMON was designed to ping a user via local Unix `wall` when
// they were not on IRC. It is universally disabled on modern
// networks. RFC 2812 says a server that does not implement SUMMON
// MUST reply with 445 ERR_SUMMONDISABLED rather than the generic
// "unknown command" error so the client knows the command exists
// but is intentionally turned off.
func (c *Conn) handleSummon(_ *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	c.send(protocol.NumericReply(srv, c.user.Nick,
		protocol.ERR_SUMMONDISABLED, "SUMMON has been disabled"))
}

// handleUsers implements USERS (RFC 2812 §4.6).
//
//	USERS [<server>]
//
// Same story as SUMMON: USERS once enumerated the local Unix users
// logged in on the server host. Universally disabled. We reply with
// 446 ERR_USERSDISABLED.
func (c *Conn) handleUsers(_ *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	c.send(protocol.NumericReply(srv, c.user.Nick,
		protocol.ERR_USERSDISABLED, "USERS has been disabled"))
}
