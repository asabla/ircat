package server

import (
	"context"
	"errors"
	"strings"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// handleOper implements OPER (RFC 2812 §3.1.4).
//
//	OPER <name> <password>
//
// Looks up the named operator in the configured Store, verifies the
// password against the stored hash via [internal/auth], and on
// success grants the user the +o user mode. The operator's host
// mask must match the connection's hostmask under simple IRC glob
// rules; otherwise we send 491 ERR_NOOPERHOST and refuse to even
// run the password compare (which would otherwise be a username
// enumeration oracle).
func (c *Conn) handleOper(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("OPER")
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	name := m.Params[0]
	password := m.Params[1]

	if c.server.store == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOOPERHOST,
			"No O-lines for your host"))
		return
	}

	op, err := c.server.store.Operators().Get(context.Background(), name)
	if err != nil {
		// Translate the not-found case to the same numeric the
		// host-mask mismatch path uses, so probing the operator
		// table for valid names returns the same response as
		// probing for the wrong host.
		if errors.Is(err, storage.ErrNotFound) {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOOPERHOST,
				"No O-lines for your host"))
			return
		}
		c.logger.Warn("operators.Get failed", "error", err)
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOOPERHOST,
			"No O-lines for your host"))
		return
	}

	if !operatorHostMatches(op.HostMask, c.user.Hostmask()) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOOPERHOST,
			"No O-lines for your host"))
		return
	}

	ok, err := auth.Verify(op.PasswordHash, password)
	if err != nil || !ok {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_PASSWDMISMATCH,
			"Password incorrect"))
		return
	}

	// Grant +o.
	if !strings.ContainsRune(c.user.Modes, 'o') {
		c.user.Modes += "o"
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_YOUREOPER,
		"You are now an IRC operator"))
	// Echo the resulting user mode word back so the client UI can
	// reflect the change.
	c.send(&protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "MODE",
		Params:  []string{nick, "+" + c.user.Modes},
	})
	c.logger.Info("oper success", "operator", op.Name, "nick", nick)
}

// operatorHostMatches reports whether userMask satisfies the
// operator's host_mask. Empty operator host mask is "match anything"
// (deliberately permissive — operators with no host restriction
// should set host_mask to "" rather than to "*@*").
func operatorHostMatches(operMask, userMask string) bool {
	if operMask == "" {
		return true
	}
	return state.GlobMatchHost(operMask, userMask)
}
