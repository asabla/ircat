package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
)

// handleUserhost implements USERHOST (RFC 2812 §4.8).
//
//	USERHOST <nickname> *( SPACE <nickname> )
//
// Up to 5 nicks per call. The reply is a single 302 line
// whose trailing parameter is a space-separated list of
// "<nick>[*]=<+|->host" tokens. The "*" suffix marks the
// nick as +o. The "+" prefix on the host marks the user as
// not-away; "-" marks them as away.
//
// Used by mIRC and bouncer scripts on every join to refresh
// their internal user table without round-tripping WHOIS for
// each entry.
const userhostMaxArgs = 5

func (c *Conn) handleUserhost(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("USERHOST")
		return
	}
	// Per the RFC, only the first 5 nicks are looked up; we
	// silently drop the rest. The 302 line is rendered even
	// when none of the nicks resolve, so a client always sees
	// a confirmation that the lookup happened.
	parts := m.Params
	if len(parts) > userhostMaxArgs {
		parts = parts[:userhostMaxArgs]
	}
	out := make([]string, 0, len(parts))
	for _, target := range parts {
		u := c.server.world.FindByNick(target)
		if u == nil {
			continue
		}
		entry := u.Nick
		if strings.ContainsRune(u.Modes, 'o') {
			entry += "*"
		}
		away := "+"
		if u.Away != "" {
			away = "-"
		}
		host := u.Host
		if host == "" {
			host = "unknown"
		}
		entry += "=" + away + host
		out = append(out, entry)
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_USERHOST,
		strings.Join(out, " ")))
}

// handleIson implements ISON (RFC 2812 §4.9).
//
//	ISON <nickname> *( SPACE <nickname> )
//
// Returns 303 RPL_ISON with the subset of supplied nicks
// that are currently registered. Used by client buddy/notify
// lists to track presence cheaply — pulling 100 nicks via
// ISON is one round-trip; doing the same via WHOIS would be
// 100.
//
// The RFC does not cap the number of nicks; we honour that.
// The wire-line cap (512 bytes) is the only effective limit.
func (c *Conn) handleIson(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("ISON")
		return
	}
	// Some clients pack every nick into a single
	// space-separated trailing param; others pass them as
	// individual middle params. We accept both.
	var nicks []string
	for _, p := range m.Params {
		for _, n := range strings.Fields(p) {
			nicks = append(nicks, n)
		}
	}
	out := make([]string, 0, len(nicks))
	for _, n := range nicks {
		if u := c.server.world.FindByNick(n); u != nil {
			out = append(out, u.Nick)
		}
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ISON,
		strings.Join(out, " ")))
}
