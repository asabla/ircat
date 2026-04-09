package server

import (
	"errors"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

var errFlood = errors.New("excess flood")

// handlePrivmsg implements PRIVMSG (RFC 2812 §3.3.1).
//
//	PRIVMSG <target>{,<target>} <text>
//
// Targets are channel names or nicknames. The text parameter is the
// trailing param, so the parser already collapsed any embedded
// whitespace into a single value.
func (c *Conn) handlePrivmsg(m *protocol.Message) {
	c.deliverMessage(m, "PRIVMSG", true)
}

// handleNotice implements NOTICE (RFC 2812 §3.3.2). The wire shape
// is identical to PRIVMSG; the only meaningful difference is that
// servers MUST NOT generate any error replies in response to a
// NOTICE — clients use this to send messages that are guaranteed
// to be silent on failure (server bots, away messages, etc.).
func (c *Conn) handleNotice(m *protocol.Message) {
	c.deliverMessage(m, "NOTICE", false)
}

// deliverMessage is the shared implementation for PRIVMSG and NOTICE.
// emitErrors controls whether the function sends ERR_* replies on
// failure; PRIVMSG sets it true, NOTICE false (RFC 2812 §3.3.2).
func (c *Conn) deliverMessage(m *protocol.Message, command string, emitErrors bool) {
	if c.user == nil || !c.user.Registered {
		if emitErrors {
			c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
				protocol.ERR_NOTREGISTERED, "You have not registered"))
		}
		return
	}

	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	if len(m.Params) < 1 || m.Params[0] == "" {
		if emitErrors {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NORECIPIENT,
				"No recipient given ("+command+")"))
		}
		return
	}
	if len(m.Params) < 2 || m.Params[1] == "" {
		if emitErrors {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTEXTTOSEND,
				"No text to send"))
		}
		return
	}

	// Flood control: each PRIVMSG/NOTICE consumes one token from the
	// per-connection bucket. If the bucket is empty, drop the message
	// silently and increment the violations counter; persistent
	// flooders are disconnected once the threshold is exceeded.
	if !c.msgBucket.Take() {
		c.msgViolations++
		if c.msgViolations >= c.server.cfg.Server.Limits.MessageViolationsToKick {
			c.sendError("Excess flood")
			c.cancel(errFlood)
		}
		return
	}

	text := m.Params[1]
	for _, target := range strings.Split(m.Params[0], ",") {
		c.deliverOneTarget(target, text, command, emitErrors)
	}
	// Stamp the speaking-activity timer used by WHOIS 317. We
	// stamp once per command, after the bucket take, so a
	// rate-limited drop does not move the clock forward.
	c.lastMessageAt.Store(c.server.now().UnixNano())
}

func (c *Conn) deliverOneTarget(target, text, command string, emitErrors bool) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	// Operator-only mask broadcasts (RFC 2812 §3.3.1):
	//   $<servermask> — every user on a matching server
	//   #<hostmask>   — every user on a matching host
	// The host-mask form is disambiguated from a channel name
	// by the presence of a "." in the rest of the mask: "#chan"
	// is a channel, "#*.example.com" is a host mask.
	if len(target) > 1 && target[0] == '$' {
		c.deliverMaskBroadcast(target[1:], text, command, false, emitErrors)
		return
	}
	if len(target) > 1 && target[0] == '#' && strings.ContainsRune(target[1:], '.') {
		c.deliverMaskBroadcast(target[1:], text, command, true, emitErrors)
		return
	}

	if isChannelName(target) {
		ch := c.server.world.FindChannel(target)
		if ch == nil && isSafeChannel(target) {
			// "!short" PRIVMSG/NOTICE: resolve the short
			// suffix to a canonical "!IDshort" before giving
			// up. The double-bang form is meaningless on
			// PRIVMSG (it would create a channel via a
			// message), so only the single-bang short form
			// is supported here.
			if resolved, ok := c.resolveSafeChannel(target); ok {
				ch = c.server.world.FindChannel(resolved)
			}
		}
		if ch == nil {
			if emitErrors {
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
					target, "No such nick/channel"))
			}
			return
		}

		_, moderated, noExternal, _, _, _, _, _ := ch.Modes()
		isMember := ch.IsMember(c.user.ID)

		// +n: only members can speak.
		if noExternal && !isMember {
			if emitErrors {
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_CANNOTSENDTOCHAN,
					ch.Name(), "Cannot send to channel"))
			}
			return
		}
		// +m: only voiced or opped members can speak.
		if moderated {
			mem := ch.Membership(c.user.ID)
			if !mem.IsOp() && !mem.IsVoice() {
				if emitErrors {
					c.send(protocol.NumericReply(srv, nick, protocol.ERR_CANNOTSENDTOCHAN,
						ch.Name(), "Cannot send to channel"))
				}
				return
			}
		}
		// +q quiet list: a user matching any quiet mask cannot
		// speak. +o and +v override the gag — operators and
		// voiced members can talk over a quiet, mirroring how
		// charybdis and inspircd handle the conflict.
		mem := ch.Membership(c.user.ID)
		if !mem.IsOp() && !mem.IsVoice() &&
			ch.MatchesQuiet(c.user.Hostmask(), c.server.world.CaseMapping().Fold) {
			if emitErrors {
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_CANNOTSENDTOCHAN,
					ch.Name(), "Cannot send to channel (+q)"))
			}
			return
		}

		msg := &protocol.Message{
			Prefix:  c.user.Hostmask(),
			Command: command,
			Params:  []string{ch.Name(), text},
		}
		// Channel broadcasts skip the sender so they do not see
		// their own message echoed (matching RFC 2812 §3.3 and
		// every production ircd). The sender knows what they sent.
		// We use the federation-aware variant so the broadcast
		// also reaches any remote members via their peer links.
		c.server.broadcastToChannelFederated(ch, msg, c.user.ID, false)
		return
	}

	// User-target form.
	target = strings.TrimSpace(target)
	u := c.server.world.FindByNick(target)
	if u == nil {
		if emitErrors {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
				target, "No such nick/channel"))
		}
		return
	}
	// AWAY echo (RFC 2812 §4.1): when a PRIVMSG is aimed at a
	// user who is currently away, the server replies with 301
	// RPL_AWAY back to the SENDER carrying the away message.
	// NOTICE deliberately does NOT trigger the 301 — that is
	// the standard rule and the reason notices are used by
	// scripts that do not want to clutter the away echo.
	if command == "PRIVMSG" && u.Away != "" {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_AWAY,
			u.Nick, u.Away))
	}
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: command,
		Params:  []string{u.Nick, text},
	}
	// Human target: deliver to the registered Conn.
	if destConn := c.server.connFor(u.ID); destConn != nil {
		destConn.send(msg)
		return
	}
	// Bot target: deliver via the virtual BotDeliverer. The bot's
	// Session.dispatchOne rewrites event.channel to the sender
	// for DMs so ctx:say(event.channel, ...) replies to the human.
	if destBot := c.server.botFor(u.ID); destBot != nil {
		destBot.Deliver(msg)
		return
	}
	// User is in the world but neither a conn nor a bot — mid
	// shutdown or similar.
	if emitErrors {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
			target, "No such nick/channel"))
	}
}

// isChannelName reports whether s looks like a channel target. We
// look only at the first byte; full validation lives in
// validChannelName for places that need it.
//
// The '+' prefix is only treated as a channel target when followed
// by at least one more byte — bare "+" is the modeless prefix on
// its own and not a valid name. The deliverOneTarget caller has
// already split off "$mask" / "#hostmask" before reaching here, so
// we do not need to disambiguate those.
func isChannelName(s string) bool {
	if len(s) == 0 {
		return false
	}
	switch s[0] {
	case '#', '&':
		return true
	case '+', '!':
		return len(s) > 1
	}
	return false
}

// deliverMaskBroadcast handles the operator-only $servermask /
// #hostmask forms of PRIVMSG and NOTICE per RFC 2812 §3.3.1. The
// hostMask flag selects which user field the glob matches against:
// false → HomeServer (or local server name for local users), true →
// the user's host. Non-operators are rejected with 481.
func (c *Conn) deliverMaskBroadcast(mask, text, command string, hostMask bool, emitErrors bool) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	if !strings.ContainsRune(c.user.Modes, 'o') {
		if emitErrors {
			c.send(protocol.NumericReply(srv, nick,
				protocol.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator"))
		}
		return
	}
	prefix := "$"
	if hostMask {
		prefix = "#"
	}
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: command,
		Params:  []string{prefix + mask, text},
	}
	for _, snap := range c.server.world.Snapshot() {
		if snap.IsRemote() {
			continue
		}
		var candidate string
		if hostMask {
			candidate = snap.Host
		} else {
			candidate = snap.HomeServer
			if candidate == "" {
				candidate = srv
			}
		}
		if !state.GlobMatchHost(mask, candidate) {
			continue
		}
		if dest := c.server.connFor(snap.ID); dest != nil {
			dest.send(msg)
		}
	}
	// Forward to every federation peer so remote +o nodes can
	// fan the broadcast out to their own local users matching
	// the same mask.
	c.server.forwardToAllLinks(msg)
}
