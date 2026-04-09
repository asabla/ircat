package server

import (
	"strconv"
	"time"

	"github.com/asabla/ircat/internal/protocol"
)

// This file holds the small handful of "tell me about the
// server" stat-query commands every modern IRC client expects:
// VERSION, TIME, ADMIN, INFO, MOTD, LUSERS. Each one is a
// short handler that walks the local state once and emits the
// numeric replies the RFC catalog defines for it.
//
// None of these reach into the federation transport — every
// reply is local-only. RFC 2812 lets the operator pass an
// optional <target> parameter to route the query through the
// mesh, but for v1 we ignore that and always answer for the
// local server. A future commit can layer routing on top.

// handleVersion implements VERSION (RFC 2812 §3.4.1).
//
//	VERSION [<target>]
//
// Returns 351 RPL_VERSION followed by an ISUPPORT line for
// parity with the welcome burst — modern clients refresh their
// capability set on every VERSION reply.
func (c *Conn) handleVersion(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_VERSION,
		c.server.Version(), srv,
		"ircat — running on Go, see https://github.com/asabla/ircat"))
	c.sendISupport()
}

// handleTime implements TIME (RFC 2812 §3.4.6).
//
//	TIME [<target>]
//
// Returns 391 RPL_TIME with the server's wall-clock formatted
// in RFC 3339 with the local timezone abbreviation, which is
// what every modern client renders.
func (c *Conn) handleTime(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	now := c.server.now().Format("2006-01-02 15:04:05 MST")
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_TIME, srv, now))
}

// handleAdmin implements ADMIN (RFC 2812 §3.4.9).
//
//	ADMIN [<target>]
//
// Emits 256-259 from cfg.Server.Admin. The four lines are
// always sent so a client never has to special-case "admin
// section incomplete" — empty fields render as "(unset)".
func (c *Conn) handleAdmin(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	admin := c.server.cfg.Server.Admin
	name := admin.Name
	email := admin.Email
	if name == "" {
		name = "(unset)"
	}
	if email == "" {
		email = "(unset)"
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ADMINME, srv, "Administrative info"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ADMINLOC1, "Name: "+name))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ADMINLOC2, "Network: "+c.server.cfg.Server.Network))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ADMINEMAIL, email))
}

// handleInfo implements INFO (RFC 2812 §3.4.10).
//
//	INFO [<target>]
//
// Emits a small block of 371 RPL_INFO lines describing the
// server software, then 374 RPL_ENDOFINFO. The block is
// hard-coded — operators who want a custom message can
// override the MOTD path.
func (c *Conn) handleInfo(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	for _, line := range []string{
		"ircat — a modern IRC server in Go.",
		"https://github.com/asabla/ircat",
		"",
		"Running version: " + c.server.Version(),
		"Server: " + srv,
		"Network: " + c.server.cfg.Server.Network,
	} {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_INFO, line))
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFINFO, "End of INFO list"))
}

// handleMotdCommand implements MOTD (RFC 2812 §3.4.1).
//
//	MOTD [<target>]
//
// The welcome burst already streams the MOTD on registration;
// this handler exists so a client that explicitly asks for it
// later (e.g. /motd in irssi) gets the same content. We just
// reuse sendMOTD which knows how to handle the empty-MOTD case
// via 422 ERR_NOMOTD.
func (c *Conn) handleMotdCommand(m *protocol.Message) {
	c.sendMOTD()
}

// handleLusers implements LUSERS (RFC 2812 §3.4.2).
//
//	LUSERS [<mask> [<target>]]
//
// Five-line summary of who is on the server right now. The
// numerics (251-255) were already in numeric.go but no command
// emitted them.
func (c *Conn) handleLusers(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()

	users := c.server.world.UserCount()
	channels := c.server.world.ChannelCount()
	opers := c.server.countOpers()
	bots := c.server.BotCount()
	fed := c.server.FederationLinkCount()

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LUSERCLIENT,
		"There are "+strconv.Itoa(users-bots)+" users and "+strconv.Itoa(bots)+" services on "+strconv.Itoa(1+fed)+" servers"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LUSEROP,
		strconv.Itoa(opers), "operator(s) online"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LUSERUNKNOWN,
		"0", "unknown connection(s)"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LUSERCHANNELS,
		strconv.Itoa(channels), "channels formed"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LUSERME,
		"I have "+strconv.Itoa(users)+" clients and "+strconv.Itoa(fed)+" servers"))
}

// countOpers returns the number of currently-registered users
// who carry the +o user mode. Walks the world snapshot — cheap
// at v1 user counts (sub-millisecond at 10k users) and avoids
// adding a separate counter the registration / OPER paths
// would have to keep in sync.
func (s *Server) countOpers() int {
	n := 0
	for _, u := range s.world.Snapshot() {
		for _, m := range u.Modes {
			if m == 'o' {
				n++
				break
			}
		}
	}
	return n
}

// sendISupport re-emits the 005 RPL_ISUPPORT block. The
// welcome burst calls this on registration; VERSION calls it
// so a client that re-fetches its capability set sees the
// current advertisement.
func (c *Conn) sendISupport() {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	for _, line := range c.server.buildISupport() {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ISUPPORT,
			append(line, "are supported by this server")...))
	}
}

// silence unused-import warning when this file ships before
// the handlers below grow to consume time directly.
var _ = time.Now