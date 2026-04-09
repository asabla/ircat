package server

import (
	"strings"

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
		// Clear the user-mode 'a' away marker (RFC 2812
		// §3.1.5). The away mode is set automatically by the
		// AWAY command and cleared by the same when the user
		// returns; clients can read it via MODE <nick>.
		c.user.Modes = stripModeChar(c.user.Modes, 'a')
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_UNAWAY,
			"You are no longer marked as being away"))
		c.notifyAway("")
		return
	}

	// Cap the away message at the configured AwayLength so a
	// pathologically long message cannot blow the line size.
	if maxLen := c.server.cfg.Server.Limits.AwayLength; maxLen > 0 && len(away) > maxLen {
		away = away[:maxLen]
	}
	c.user.Away = away
	if !strings.ContainsRune(c.user.Modes, 'a') {
		c.user.Modes += "a"
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_NOWAWAY,
		"You have been marked as being away"))
	c.notifyAway(away)
}

// notifyAway emits an IRCv3 away-notify line to every shared-channel
// member who has the cap negotiated. reason is the away message
// (empty for the "back" form). Members are deduplicated across
// shared channels so a peer in two channels with c only receives
// one AWAY line.
//
// The user themselves is excluded — they already received the
// 305 / 306 confirmation reply.
func (c *Conn) notifyAway(reason string) {
	msg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "AWAY",
	}
	if reason != "" {
		msg.Params = []string{reason}
	}
	seen := make(map[uint64]bool)
	for _, ch := range c.server.world.UserChannels(c.user.ID) {
		for id := range ch.MemberIDs() {
			if id == c.user.ID || seen[uint64(id)] {
				continue
			}
			seen[uint64(id)] = true
			peer := c.server.connFor(id)
			if peer == nil || !peer.capsAccepted["away-notify"] {
				continue
			}
			peer.send(msg)
		}
	}
}

// stripModeChar removes every occurrence of m from modes. The
// result preserves the original ordering of the remaining bytes.
func stripModeChar(modes string, m byte) string {
	if !strings.ContainsRune(modes, rune(m)) {
		return modes
	}
	out := make([]byte, 0, len(modes))
	for i := 0; i < len(modes); i++ {
		if modes[i] != m {
			out = append(out, modes[i])
		}
	}
	return string(out)
}
