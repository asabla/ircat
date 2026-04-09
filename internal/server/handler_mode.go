package server

import (
	"strconv"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleMode dispatches MODE to either the channel or user form
// based on the target's first character.
//
//	MODE <channel> [<modes> [<args>...]]
//	MODE <nick>    [<modes>]
//
// With no <modes>, both forms are queries: channel form returns
// RPL_CHANNELMODEIS + RPL_CREATIONTIME, user form returns
// RPL_UMODEIS (which we send via a fake numeric since RPL_UMODEIS is
// 221 and we just emit the user's mode word).
func (c *Conn) handleMode(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("MODE")
		return
	}
	target := m.Params[0]
	if isChannelName(target) {
		c.handleChannelMode(target, m.Params[1:])
		return
	}
	c.handleUserMode(target, m.Params[1:])
}

// handleChannelMode handles the channel form of MODE.
func (c *Conn) handleChannelMode(name string, params []string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	ch := c.server.world.FindChannel(name)
	if ch == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL, name, "No such channel"))
		return
	}

	// Query form: no mode arguments at all -> return current modes.
	if len(params) == 0 {
		modes, modeParams := ch.ModeString()
		out := []string{ch.Name(), modes}
		out = append(out, modeParams...)
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_CHANNELMODEIS, out...))
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_CREATIONTIME,
			ch.Name(), itoaPositive(int(ch.CreatedAt().Unix()))))
		return
	}

	// Modeless channels (RFC 2811 §4.2.1, '+' prefix) reject every
	// mutation MODE. Bare-list queries above are still answered.
	if isModelessChannel(ch.Name()) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANOPRIVSNEEDED,
			ch.Name(), "Channel does not support modes"))
		return
	}

	// Special case: a bare list query for +b, +e, +I (e.g.
	// "MODE #x +b" with no mask). Treated as a list dump rather than
	// a mutation.
	if len(params) == 1 && isListQueryMode(params[0]) {
		c.sendChannelListMode(ch, params[0])
		return
	}

	// Mutation requires channel-op rights.
	if !ch.Membership(c.user.ID).IsOp() {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANOPRIVSNEEDED,
			ch.Name(), "You are not channel operator"))
		return
	}

	applied, badChars := c.applyChannelModes(ch, params)
	for _, ch := range badChars {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_UNKNOWNMODE,
			string(ch), "is unknown mode char to me"))
	}
	if len(applied.changes) == 0 {
		return
	}
	// Persist the new state before broadcasting so a crash between
	// the in-memory mutation and the wire echo cannot leave the
	// channel record stale on restart.
	c.server.persistChannel(c.ctx, ch)
	c.server.emitAudit(c.ctx, AuditTypeMode, c.user.Hostmask(), ch.Name(), map[string]any{
		"changes": applied.changes,
		"params":  applied.params,
	})
	// Broadcast the actually-applied changes to every channel member.
	out := []string{ch.Name(), applied.changes}
	out = append(out, applied.params...)
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "MODE",
		Params:  out,
	}
	c.server.broadcastToChannelFederated(ch, msg, 0, true)
}

// applied accumulates a successful run of mode changes so the
// caller can build a single MODE broadcast that reflects what
// actually happened (skipping no-ops and unknowns).
type applied struct {
	changes string
	params  []string
}

// applyChannelModes parses params as a mode string + arg list and
// applies each change to ch. Returns the accumulated applied set
// plus any unknown mode characters that the caller should bounce
// back as ERR_UNKNOWNMODE.
func (c *Conn) applyChannelModes(ch *state.Channel, params []string) (applied, []byte) {
	out := applied{}
	var badChars []byte
	if len(params) == 0 {
		return out, nil
	}
	modeStr := params[0]
	args := params[1:]
	argi := 0

	dir := byte('+')
	var dirOut []byte // accumulates "+abc-de" for the broadcast

	flushDir := func(d byte) {
		if len(dirOut) == 0 || dirOut[len(dirOut)-1] != d {
			dirOut = append(dirOut, d)
		}
	}

	popArg := func() (string, bool) {
		if argi >= len(args) {
			return "", false
		}
		v := args[argi]
		argi++
		return v, true
	}

	for i := 0; i < len(modeStr); i++ {
		mc := modeStr[i]
		switch mc {
		case '+', '-':
			dir = mc
			continue
		}
		switch mc {
		case 'i', 'm', 'n', 'p', 's', 't', 'a':
			if ch.SetBoolMode(mc, dir == '+') {
				flushDir(dir)
				dirOut = append(dirOut, mc)
			}
		case 'k':
			if dir == '+' {
				key, ok := popArg()
				if !ok || key == "" {
					continue
				}
				if ch.SetKey(key) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, key)
				}
			} else {
				if ch.SetKey("") {
					flushDir(dir)
					dirOut = append(dirOut, mc)
				}
			}
		case 'l':
			if dir == '+' {
				raw, ok := popArg()
				if !ok {
					continue
				}
				n, err := strconv.Atoi(raw)
				if err != nil || n < 0 {
					continue
				}
				if ch.SetLimit(n) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, raw)
				}
			} else {
				if ch.SetLimit(0) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
				}
			}
		case 'o', 'v':
			arg, ok := popArg()
			if !ok {
				continue
			}
			target := c.server.world.FindByNick(arg)
			if target == nil || !ch.IsMember(target.ID) {
				continue
			}
			flag := state.MemberOp
			if mc == 'v' {
				flag = state.MemberVoice
			}
			var changed bool
			if dir == '+' {
				_, changed = ch.AddMembership(target.ID, flag)
			} else {
				_, changed = ch.RemoveMembership(target.ID, flag)
			}
			if changed {
				flushDir(dir)
				dirOut = append(dirOut, mc)
				out.params = append(out.params, target.Nick)
			}
		case 'b':
			if dir == '+' {
				mask, ok := popArg()
				if !ok || mask == "" {
					// Bare "+b" is a list query handled separately.
					continue
				}
				if ch.AddBan(mask, c.server.now()) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			} else {
				mask, ok := popArg()
				if !ok || mask == "" {
					continue
				}
				if ch.RemoveBan(mask) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			}
		case 'e':
			// Ban-exception list (RFC 2811 §4.3.2). +e takes a
			// hostmask the same shape as +b; matching exceptions
			// override matching bans on the join check.
			mask, ok := popArg()
			if !ok || mask == "" {
				continue
			}
			if dir == '+' {
				if ch.AddException(mask, c.server.now()) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			} else {
				if ch.RemoveException(mask) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			}
		case 'I':
			// Invite-exception list (RFC 2811 §4.3.3). Hosts
			// matching an +I mask are allowed past +i without an
			// explicit per-user invite.
			mask, ok := popArg()
			if !ok || mask == "" {
				continue
			}
			if dir == '+' {
				if ch.AddInvex(mask, c.server.now()) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			} else {
				if ch.RemoveInvex(mask) {
					flushDir(dir)
					dirOut = append(dirOut, mc)
					out.params = append(out.params, mask)
				}
			}
		default:
			badChars = append(badChars, mc)
		}
	}
	out.changes = string(dirOut)
	return out, badChars
}

// isListQueryMode reports whether s is a bare "+b" / "+e" / "+I"
// (or the same without the leading "+") signalling a list dump.
func isListQueryMode(s string) bool {
	switch s {
	case "+b", "b", "+e", "e", "+I", "I":
		return true
	}
	return false
}

func (c *Conn) sendChannelListMode(ch *state.Channel, mode string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	switch strings.TrimPrefix(mode, "+") {
	case "b":
		for mask := range ch.Bans() {
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_BANLIST, ch.Name(), mask))
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFBANLIST,
			ch.Name(), "End of channel ban list"))
	case "e":
		for mask := range ch.Exceptions() {
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_EXCEPTLIST, ch.Name(), mask))
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFEXCEPTLIST,
			ch.Name(), "End of channel exception list"))
	case "I":
		for mask := range ch.Invexes() {
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_INVITELIST, ch.Name(), mask))
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFINVITELIST,
			ch.Name(), "End of channel invite list"))
	}
}

// handleUserMode handles the user form of MODE. Only the user
// themselves can read or modify their own modes. The +o flag cannot
// be set via user MODE — it can only be granted by OPER (M3) and
// must be removable by the user themselves.
func (c *Conn) handleUserMode(target string, params []string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	if !c.server.world.CaseMapping().Equal(target, c.user.Nick) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
			target, "No such nick/channel"))
		return
	}

	// Query form.
	if len(params) == 0 {
		c.send(&protocol.Message{
			Prefix:  srv,
			Command: protocol.RPL_UMODEIS,
			Params:  []string{nick, "+" + c.user.Modes},
		})
		return
	}

	// Mutation. Walk the mode string applying allowed changes.
	modeStr := params[0]
	dir := byte('+')
	current := []byte(c.user.Modes)
	addMode := func(b byte) {
		for _, x := range current {
			if x == b {
				return
			}
		}
		current = append(current, b)
	}
	delMode := func(b byte) {
		out := current[:0]
		for _, x := range current {
			if x != b {
				out = append(out, x)
			}
		}
		current = out
	}

	for i := 0; i < len(modeStr); i++ {
		mc := modeStr[i]
		if mc == '+' || mc == '-' {
			dir = mc
			continue
		}
		switch mc {
		case 'i', 'w', 's':
			if dir == '+' {
				addMode(mc)
			} else {
				delMode(mc)
			}
		case 'o':
			// Only the OPER command can grant +o; -o is allowed.
			if dir == '-' {
				delMode(mc)
			}
		default:
			// Unknown user modes are silently ignored per RFC 2812
			// section 3.1.5.
		}
	}
	c.user.Modes = string(current)
	// Echo the resulting modes back to the user.
	c.send(&protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "MODE",
		Params:  []string{nick, "+" + c.user.Modes},
	})
}
