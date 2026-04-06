package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
)

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

	text := m.Params[1]
	for _, target := range strings.Split(m.Params[0], ",") {
		c.deliverOneTarget(target, text, command, emitErrors)
	}
}

func (c *Conn) deliverOneTarget(target, text, command string, emitErrors bool) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	if isChannelName(target) {
		ch := c.server.world.FindChannel(target)
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

		msg := &protocol.Message{
			Prefix:  c.user.Hostmask(),
			Command: command,
			Params:  []string{ch.Name(), text},
		}
		// Channel broadcasts skip the sender so they do not see
		// their own message echoed (matching RFC 2812 §3.3 and
		// every production ircd). The sender knows what they sent.
		c.server.broadcastToChannel(ch, msg, c.user.ID, false)
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
	dest := c.server.connFor(u.ID)
	if dest == nil {
		// User is in the world but the conn is gone (mid-shutdown).
		if emitErrors {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
				target, "No such nick/channel"))
		}
		return
	}
	dest.send(&protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: command,
		Params:  []string{u.Nick, text},
	})
}

// isChannelName reports whether s looks like a channel target. We
// look only at the first byte; full validation lives in
// validChannelName for places that need it.
func isChannelName(s string) bool {
	return len(s) > 0 && (s[0] == '#' || s[0] == '&')
}
