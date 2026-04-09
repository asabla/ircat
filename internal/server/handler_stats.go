package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/protocol"
)

// handleStats implements STATS (RFC 2812 §3.4.4).
//
//	STATS [ <query> [ <target> ] ]
//
// We support the four queries that operators reach for in practice:
//
//	l — federation link information (211 RPL_STATSLINKINFO)
//	m — command counters             (212 RPL_STATSCOMMANDS)
//	o — operator authorization lines (243 RPL_STATSOLINE)
//	u — server uptime                (242 RPL_STATSUPTIME)
//
// Every query terminates with 219 RPL_ENDOFSTATS. STATS is operator
// only on this server because the link/oper/uptime info leaks
// internal topology that we don't want to advertise to anonymous
// clients. The RFC permits this restriction.
func (c *Conn) handleStats(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if !strings.ContainsRune(c.user.Modes, 'o') {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator"))
		return
	}

	query := ""
	if len(m.Params) >= 1 {
		query = m.Params[0]
	}
	switch query {
	case "l":
		c.statsLinks()
	case "m":
		c.statsCommands()
	case "o":
		c.statsOperators()
	case "u":
		c.statsUptime()
	}
	c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_ENDOFSTATS,
		emptyOrQuery(query), "End of STATS report"))
}

func emptyOrQuery(q string) string {
	if q == "" {
		return "*"
	}
	return q
}

// statsLinks emits 211 for every active federation link. Without any
// per-link byte counters wired up yet (those will land alongside the
// link refactor) the rate fields are reported as zero.
func (c *Conn) statsLinks() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	for _, row := range c.server.FederationSnapshot() {
		// "<linkname> <sendq> <sent msgs> <sent kbytes> <recv msgs>
		//  <recv kbytes> <time open>"
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_STATSLINKINFO,
			row.Peer(), "0", "0", "0", "0", "0", row.State()))
	}
}

// statsCommands emits a single aggregate 212 line. We do not break
// the counter down per-command yet — that needs hooking the dispatch
// table — but the global in/out totals are already maintained on the
// hot path and they're the numbers an operator usually wants.
func (c *Conn) statsCommands() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	in := c.server.messagesIn.Load()
	out := c.server.messagesOut.Load()
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_STATSCOMMANDS,
		"TOTAL", fmt.Sprintf("%d", in), "0", fmt.Sprintf("%d", out)))
}

// statsOperators emits a 243 line per configured operator. The host
// mask is exposed (it's a glob, not a credential) but the password
// hash is never sent. Falls back to a single zero-row if the store
// is unset (e.g. tests that didn't wire one).
func (c *Conn) statsOperators() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	if c.server.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ops, err := c.server.store.Operators().List(ctx)
	if err != nil {
		c.logger.Warn("stats o: list failed", "error", err)
		return
	}
	for _, op := range ops {
		// "O <hostmask> * <name>"
		host := op.HostMask
		if host == "" {
			host = "*"
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_STATSOLINE,
			"O", host, "*", op.Name))
	}
}

// statsUptime emits 242 with the formatted server uptime.
func (c *Conn) statsUptime() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	d := c.server.now().Sub(c.server.createdAt)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_STATSUPTIME,
		fmt.Sprintf("Server Up %d days %d:%02d:%02d", days, hours, mins, secs)))
}
