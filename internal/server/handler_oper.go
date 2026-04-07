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
	c.server.emitAudit(c.ctx, AuditTypeOperUp, c.user.Hostmask(), op.Name, map[string]any{
		"flags": op.Flags,
	})
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

// handleKill implements KILL (RFC 2812 §3.7.1).
//
//	KILL <nickname> <comment>
//
// Disconnects nickname from the network. Requires +o on the
// caller. The kill is broadcast to local channel members as a
// synthetic QUIT and forwarded to every federation peer so the
// rest of the mesh drops the user too. KILL targeting a remote
// user is honoured: the local node forwards the message and
// drops its own copy of the remote-user record on the way out
// so the user disappears from this node immediately rather than
// waiting for the home server's QUIT to come back.
func (c *Conn) handleKill(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if !strings.ContainsRune(c.user.Modes, 'o') {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator"))
		return
	}
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("KILL")
		return
	}
	targetNick := m.Params[0]
	comment := m.Params[1]

	target := c.server.world.FindByNick(targetNick)
	if target == nil {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOSUCHNICK, targetNick, "No such nick/channel"))
		return
	}

	// Build the KILL line for federation forwarding before we
	// touch the local state — once the conn is cancelled the
	// hostmask is no longer reachable.
	killMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "KILL",
		Params:  []string{target.Nick, comment},
	}

	// Forward to every peer regardless of whether the target is
	// local or remote: every node in the mesh that knows about
	// the user must drop them.
	c.server.forwardToAllLinks(killMsg)

	// Audit the kill on the home node.
	c.server.emitAudit(c.ctx, AuditTypeAdminAction, c.user.Hostmask(), target.Nick, map[string]any{
		"action": "kill",
		"reason": comment,
	})

	if target.IsRemote() {
		// Remote target — drop our copy of the record and fan a
		// synthetic QUIT to local channel members so they see
		// the disappearance immediately.
		quitMsg := &protocol.Message{
			Prefix:  target.Hostmask(),
			Command: "QUIT",
			Params:  []string{"Killed (" + c.user.Nick + " (" + comment + "))"},
		}
		c.server.deliverPerUserChannels(quitMsg)
		c.server.world.RemoveUser(target.ID)
		return
	}

	// Local target — disconnect via the same path KickUser uses.
	if err := c.server.KickUser(c.ctx, target.Nick,
		"Killed ("+c.user.Nick+" ("+comment+"))"); err != nil {
		c.logger.Warn("kill kick failed", "error", err, "target", target.Nick)
	}
}
