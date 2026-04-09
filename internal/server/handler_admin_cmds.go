package server

import (
	"context"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/protocol"
)

// handleRehash implements REHASH (RFC 2812 §4.2). Operator-only.
// Triggers a config-file re-read via the wired Reloader, the same
// path SIGHUP and the admin API use.
//
// Replies with 382 RPL_REHASHING on success and a NOTICE describing
// any failure. The numeric is not in protocol/numeric.go yet because
// REHASH is the only caller; we inline the code to avoid forcing a
// const update for a single use.
func (c *Conn) handleRehash(m *protocol.Message) {
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
	if c.server.reloader == nil {
		c.send(&protocol.Message{
			Prefix:  srv,
			Command: "NOTICE",
			Params:  []string{c.user.Nick, "REHASH unavailable: no reloader wired"},
		})
		return
	}
	// 382 RPL_REHASHING: "<config file> :Rehashing"
	c.send(&protocol.Message{
		Prefix:  srv,
		Command: "382",
		Params:  []string{c.user.Nick, "config", "Rehashing"},
	})
	go func(nick string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.server.reloader.Reload(ctx); err != nil {
			c.logger.Warn("rehash reload failed", "error", err, "operator", nick)
			c.send(&protocol.Message{
				Prefix:  srv,
				Command: "NOTICE",
				Params:  []string{nick, "REHASH failed: " + err.Error()},
			})
			return
		}
		c.logger.Info("rehash applied", "operator", nick)
	}(c.user.Nick)
}

// handleDie implements DIE (RFC 2812 §4.3). Operator-only. Asks the
// host process to exit cleanly via the wired shutdown callback. The
// reason carried in the optional trailing param is logged.
func (c *Conn) handleDie(m *protocol.Message) {
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
	reason := "DIE by " + c.user.Nick
	if t, ok := m.Trailing(); ok && t != "" {
		reason = "DIE by " + c.user.Nick + ": " + t
	}
	if c.server.shutdown == nil {
		c.send(&protocol.Message{
			Prefix:  srv,
			Command: "NOTICE",
			Params:  []string{c.user.Nick, "DIE unavailable: no shutdown hook wired"},
		})
		return
	}
	c.logger.Warn("die requested", "operator", c.user.Nick, "reason", reason)
	c.server.shutdown(reason)
}

// handleRestart implements RESTART (RFC 2812 §4.4). Operator-only.
// We do not exec(2) ourselves — instead we ask the host to exit
// cleanly with a "restart" reason and let the supervisor (systemd,
// docker, etc.) bring us back. The behaviour is identical to DIE
// from the daemon's perspective; the distinction is preserved so a
// supervisor can interpret the reason.
func (c *Conn) handleRestart(m *protocol.Message) {
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
	reason := "RESTART by " + c.user.Nick
	if c.server.shutdown == nil {
		c.send(&protocol.Message{
			Prefix:  srv,
			Command: "NOTICE",
			Params:  []string{c.user.Nick, "RESTART unavailable: no shutdown hook wired"},
		})
		return
	}
	c.logger.Warn("restart requested", "operator", c.user.Nick)
	c.server.shutdown(reason)
}
