package server

import (
	"context"
	"errors"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// ErrUserNotFound is returned by [Server.KickUser] when the supplied
// nick does not match any registered connection. The api package
// translates it to a 404.
var ErrUserNotFound = errors.New("server: user not found")

// KickUser disconnects the user identified by nick. The reason is
// stamped onto the QUIT broadcast and the ERROR line. Implements
// the Actuator interface in internal/api.
//
// The implementation walks the conn registry, finds the matching
// Conn, sets its quit reason, and cancels its context. The normal
// close path then runs broadcastQuit so channel peers see the QUIT
// with the supplied reason.
func (s *Server) KickUser(ctx context.Context, nick, reason string) error {
	u := s.world.FindByNick(nick)
	if u == nil {
		return ErrUserNotFound
	}
	c := s.connFor(u.ID)
	if c == nil {
		return ErrUserNotFound
	}
	if reason == "" {
		reason = "Kicked by admin"
	}
	c.quitReason = reason
	c.sendError(reason)
	c.cancel(errors.New("api kick: " + reason))
	s.emitAudit(ctx, AuditTypeAdminAction, "api", nick, map[string]any{
		"action": "kick_user",
		"reason": reason,
	})
	return nil
}

// ListenerAddresses returns the IRC listener bind addresses as
// strings. Implements [internal/api.Actuator].
func (s *Server) ListenerAddresses() []string {
	addrs := s.ListenerAddrs()
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

// SnapshotUsers returns a copy of every registered user. Implements
// [internal/api.Actuator].
func (s *Server) SnapshotUsers() []state.User {
	return s.world.Snapshot()
}

// SnapshotChannels returns a copy of the channel pointers the world
// is currently tracking. Implements [internal/api.Actuator].
func (s *Server) SnapshotChannels() []*state.Channel {
	return s.world.ChannelsSnapshot()
}

// ServerName returns the configured IRC server name. Implements
// [internal/api.ServerInfoSource].
func (s *Server) ServerName() string { return s.cfg.Server.Name }

// NetworkName returns the configured network name. Implements
// [internal/api.ServerInfoSource].
func (s *Server) NetworkName() string { return s.cfg.Server.Network }

// Version returns the running ircat version string. Hard-coded for
// now; M8 wires it to the ldflags-stamped value.
func (s *Server) Version() string { return "ircat-0.0.1" }

// StartedAt returns the server creation timestamp.
func (s *Server) StartedAt() time.Time { return s.createdAt }

// silence unused-import warnings on platforms where the test build
// removes the protocol import path. The package itself uses
// protocol elsewhere.
var _ = protocol.RPL_WELCOME
