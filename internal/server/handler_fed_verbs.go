package server

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/protocol"
)

// handleLinks implements LINKS (RFC 2812 §3.4.5).
//
//	LINKS [ [ <remote server> ] <server mask> ]
//
// Returns one 364 RPL_LINKS line per known server in the federation
// mesh (the local node plus every active peer link), terminated by
// 365 RPL_ENDOFLINKS. The optional mask is matched against the
// server name; "*" or empty match everything. The remote-server
// parameter is accepted but ignored — we do not currently relay
// LINKS to other nodes, all answers come from the local view.
//
// LINKS is intentionally not operator-gated. The list of peer
// names is already visible to anyone running STATS l on a friendly
// node, and ircds historically expose it to clients.
func (c *Conn) handleLinks(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	nick := c.starOrNick()
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	mask := "*"
	if len(m.Params) >= 2 {
		mask = m.Params[1]
	} else if len(m.Params) == 1 {
		mask = m.Params[0]
	}

	emit := func(name string) {
		if !linkMatch(mask, name) {
			return
		}
		// "<mask> <server> :<hopcount> <server info>"
		c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_LINKS,
			name, name, "0 ircat node"))
	}
	// The local node always appears in LINKS so a client can see
	// where the answers are coming from.
	emit(srv)
	for _, row := range c.server.FederationSnapshot() {
		emit(row.Peer())
	}
	c.send(protocol.NumericReply(srv, c.user.Nick, protocol.RPL_ENDOFLINKS,
		mask, "End of LINKS list"))
}

// linkMatch is the trivial "*" match LINKS uses. We do not implement
// the full glob grammar (it's a sharp tool that nothing relies on);
// either the mask is "*"/empty (match all) or it must equal the
// server name exactly. Substring matches are not supported.
func linkMatch(mask, name string) bool {
	if mask == "" || mask == "*" {
		return true
	}
	return mask == name
}

// handleSquit implements SQUIT (RFC 2812 §3.4.6).
//
//	SQUIT <server> <comment>
//
// Operator-only. Tears down the named federation link via the same
// HandleSquit recovery flow that fires when a peer disappears
// involuntarily. The comment is logged and broadcast to other
// peers. Returns ERR_NOSUCHSERVER if the named peer is not in the
// link registry.
func (c *Conn) handleSquit(m *protocol.Message) {
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
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("SQUIT")
		return
	}
	target := m.Params[0]
	comment := m.Params[1]

	// Verify the peer is known before kicking off the recovery.
	found := false
	for _, row := range c.server.FederationSnapshot() {
		if row.Peer() == target {
			found = true
			break
		}
	}
	if !found {
		c.send(protocol.NumericReply(srv, c.user.Nick, protocol.ERR_NOSUCHSERVER,
			target, "No such server"))
		return
	}
	c.logger.Warn("operator squit", "operator", c.user.Nick, "peer", target, "comment", comment)
	c.server.HandleSquit(target, comment)
}

// handleConnect implements CONNECT (RFC 2812 §3.4.7).
//
//	CONNECT <target server> [ <port> [ <remote server> ] ]
//
// Operator-only. Asks the host process to dial a new federation
// link via the wired Connector. The remote-server parameter (which
// would route the request to a different node to do the dial on
// our behalf) is accepted but ignored — CONNECT only acts on the
// local node.
//
// If no Connector is wired (e.g. the test harness, or a build
// without runtime federation dialing) the command emits a NOTICE
// explaining the no-op rather than crashing.
func (c *Conn) handleConnect(m *protocol.Message) {
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
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("CONNECT")
		return
	}
	target := m.Params[0]
	port := 0
	if len(m.Params) >= 2 {
		if p, err := strconv.Atoi(m.Params[1]); err == nil {
			port = p
		}
	}
	if c.server.connector == nil {
		c.send(&protocol.Message{
			Prefix:  srv,
			Command: "NOTICE",
			Params:  []string{c.user.Nick, "CONNECT unavailable: no connector wired"},
		})
		return
	}
	c.logger.Warn("operator connect", "operator", c.user.Nick, "target", target, "port", port)
	go func(target string, port int, opNick string) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.server.connector.Connect(ctx, target, port); err != nil {
			c.send(&protocol.Message{
				Prefix:  srv,
				Command: "NOTICE",
				Params:  []string{opNick, "CONNECT to " + target + " failed: " + err.Error()},
			})
		}
	}(target, port, c.user.Nick)
}
