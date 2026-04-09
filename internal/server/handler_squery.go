package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
)

// handleSquery implements SQUERY (RFC 2812 §3.3.3).
//
//	SQUERY <servicename> :<text>
//
// SQUERY is the service-targeted variant of PRIVMSG. It is
// addressed to a registered service (a state.User with Service=true)
// rather than a channel or regular user. If the target is not a
// service, the server replies with 408 ERR_NOSUCHSERVICE — even if
// a regular user with that nick exists. This is the gate that
// keeps service traffic and user traffic from leaking into each
// other.
func (c *Conn) handleSquery(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 || m.Params[0] == "" {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NORECIPIENT, "No recipient given (SQUERY)"))
		return
	}
	if len(m.Params) < 2 || m.Params[1] == "" {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOTEXTTOSEND, "No text to send"))
		return
	}
	target := m.Params[0]
	text := m.Params[1]

	u := c.server.world.FindByNick(target)
	if u == nil || !u.Service {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOSUCHSERVICE, target, "No such service"))
		return
	}
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "SQUERY",
		Params:  []string{u.Nick, text},
	}
	if dest := c.server.connFor(u.ID); dest != nil {
		dest.send(msg)
		return
	}
	if dest := c.server.botFor(u.ID); dest != nil {
		dest.Deliver(msg)
		return
	}
	c.send(protocol.NumericReply(srv, c.user.Nick,
		protocol.ERR_NOSUCHSERVICE, target, "No such service"))
}

// handleServlist implements SERVLIST (RFC 2812 §3.5.1).
//
//	SERVLIST [<mask> [<type>]]
//
// Lists every registered service whose name matches the optional
// glob mask and whose type matches the optional type filter. One
// 234 RPL_SERVLIST line per match, terminated by 235
// RPL_SERVLISTEND with the same mask + type so a client can match
// the response to the request.
func (c *Conn) handleServlist(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	mask := "*"
	if len(m.Params) >= 1 && m.Params[0] != "" {
		mask = m.Params[0]
	}
	typeFilter := ""
	if len(m.Params) >= 2 {
		typeFilter = m.Params[1]
	}

	for _, u := range c.server.world.Snapshot() {
		if !u.Service {
			continue
		}
		if mask != "*" && !linkMatch(mask, u.Nick) {
			continue
		}
		if typeFilter != "" && typeFilter != u.ServiceType {
			continue
		}
		// "<name> <server> <mask> <type> <hopcount> <info>"
		serverName := u.HomeServer
		if serverName == "" {
			serverName = srv
		}
		distribution := u.ServiceDistribution
		if distribution == "" {
			distribution = "*"
		}
		c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_SERVLIST,
			u.Nick, serverName, distribution, u.ServiceType, "0", u.Realname))
	}
	c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_SERVLISTEND,
		mask, ifEmpty(typeFilter, "*"), "End of service listing"))
}

// ifEmpty returns alt if s is empty, otherwise s.
func ifEmpty(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}
