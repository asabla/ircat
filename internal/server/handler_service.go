package server

import (
	"errors"
	"fmt"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleService implements the SERVICE registration command from
// RFC 2812 §3.1.6.
//
//	SERVICE <nickname> <reserved> <distribution> <type> <reserved> <info>
//
// SERVICE is an alternative to NICK + USER for service connections
// (network helpers like ChanServ, NickServ, BotServ on traditional
// IRC networks). The registered entity becomes a state.User with
// Service=true; the rest of the runtime treats it like any other
// user record except that:
//
//   - Channel broadcasts skip services (they only receive direct
//     SQUERY traffic)
//   - SERVLIST returns one row per registered service
//   - SQUERY routes a message specifically to a service
//
// We accept the wire form per the RFC; the <reserved> fields are
// ignored as the RFC requires. The <distribution> mask controls
// which servers in a federation see the service (we store it but do
// not yet enforce it — local-only services work today, federated
// services are deferred until services run on multiple nodes).
func (c *Conn) handleService(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	if c.user != nil && c.user.Registered {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_ALREADYREGISTRED, "You may not reregister"))
		return
	}
	if len(m.Params) < 6 {
		c.sendNeedMoreParams("SERVICE")
		return
	}
	nick := m.Params[0]
	// m.Params[1] reserved
	distribution := m.Params[2]
	stype := m.Params[3]
	// m.Params[4] reserved
	info := m.Params[5]

	if !validNickname(nick, c.server.cfg.Server.Limits.NickLength) {
		c.send(protocol.NumericReply(srv, "*",
			protocol.ERR_ERRONEUSNICKNAME, nick, "Erroneous service name"))
		return
	}

	user := &state.User{
		Nick:                nick,
		User:                "service",
		Host:                c.remoteHost,
		Realname:            info,
		Registered:          true,
		Service:             true,
		ServiceType:         stype,
		ServiceDistribution: distribution,
		TS:                  c.server.now().UnixNano(),
	}
	if _, err := c.server.world.AddUser(user); err != nil {
		if errors.Is(err, state.ErrNickInUse) {
			c.send(protocol.NumericReply(srv, "*",
				protocol.ERR_NICKNAMEINUSE, nick, "Service name is already in use"))
			return
		}
		c.logger.Warn("SERVICE AddUser failed", "error", err)
		c.sendError("Internal server error")
		c.cancel(err)
		return
	}
	c.user = user
	c.server.registerConn(c)
	c.logger.Info("service registered", "nick", nick, "type", stype, "distribution", distribution)

	// 383 RPL_YOURESERVICE: "You are service <name>"
	c.send(&protocol.Message{
		Prefix:  srv,
		Command: protocol.RPL_YOURESERVICE,
		Params:  []string{nick, fmt.Sprintf("You are service %s", nick)},
	})
	// Reuse YOURHOST/CREATED so the service connection sees the
	// same context a regular client gets.
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_YOURHOST,
		fmt.Sprintf("Your host is %s, running version ircat-0.0.1", srv)))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_MYINFO,
		srv, "ircat-0.0.1", "iow", "biklmnopstv"))
}
