package server

import (
	"strconv"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleWhowas implements WHOWAS (RFC 2812 §3.6.3).
//
//	WHOWAS <nickname> [ <count> [ <target> ] ]
//
// Returns historical information about a nick that has either
// disconnected or been renamed away from. Each matching entry emits
// 314 RPL_WHOWASUSER and 312 RPL_WHOISSERVER, then the lookup is
// terminated by 369 RPL_ENDOFWHOWAS. If no entries exist for the
// nick we send 406 ERR_WASNOSUCHNICK first, then the 369.
//
// Multiple comma-separated nicks are supported per RFC. The <target>
// parameter is accepted but ignored — there is no remote routing of
// WHOWAS in this implementation; queries are answered from the local
// ring buffer.
func (c *Conn) handleWhowas(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 || m.Params[0] == "" {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NONICKNAMEGIVEN, "No nickname given"))
		return
	}
	max := 0
	if len(m.Params) >= 2 {
		if n, err := strconv.Atoi(m.Params[1]); err == nil && n > 0 {
			max = n
		}
	}
	for _, target := range splitCommaList(m.Params[0]) {
		entries := c.server.whowas.Lookup(target, max)
		if len(entries) == 0 {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_WASNOSUCHNICK,
				target, "There was no such nickname"))
		}
		for _, e := range entries {
			user := e.User
			if user == "" {
				user = e.Nick
			}
			host := e.Host
			if host == "" {
				host = "unknown"
			}
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOWASUSER,
				e.Nick, user, host, "*", e.Realname))
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISSERVER,
				e.Nick, e.Server, e.When.UTC().Format("Mon Jan 2 15:04:05 2006 UTC")))
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHOWAS,
			target, "End of WHOWAS"))
	}
}

// recordWhowas snapshots the user into the historical ring. Called
// from the disconnect/KILL/rename paths so a future WHOWAS lookup can
// recover what the nick used to refer to.
func (s *Server) recordWhowas(u *state.User) {
	if u == nil || s.whowas == nil {
		return
	}
	srv := u.HomeServer
	if srv == "" {
		srv = s.cfg.Server.Name
	}
	s.whowas.Record(state.WhowasEntry{
		Nick:     u.Nick,
		User:     u.User,
		Host:     u.Host,
		Realname: u.Realname,
		Server:   srv,
		When:     s.now(),
	})
}

// splitCommaList trims and splits a "a,b,c" target list, dropping
// empty entries. Used by handlers that accept comma-separated nicks.
func splitCommaList(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 2)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
