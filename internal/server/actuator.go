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

// ----- bot IRC actuator surface ---------------------------------
//
// These methods implement [internal/bots.IRCActuator], the
// interface the Lua bot supervisor uses to produce IRC-side side
// effects on behalf of a bot. They mirror the code paths the
// normal command handlers take but use the bot's virtual state.User
// as the source.

// BotJoin makes the bot identified by botID a member of
// channelName and broadcasts JOIN to existing members.
func (s *Server) BotJoin(botID state.UserID, channelName string) error {
	u := s.world.FindByID(botID)
	if u == nil {
		return ErrUserNotFound
	}
	ch, _, added, err := s.world.JoinChannel(botID, channelName)
	if err != nil {
		return err
	}
	if !added {
		return nil
	}
	msg := &protocol.Message{
		Prefix:  u.Hostmask(),
		Command: "JOIN",
		Params:  []string{ch.Name()},
	}
	s.broadcastToChannel(ch, msg, 0, true)
	return nil
}

// BotPart removes the bot from a channel and broadcasts PART.
func (s *Server) BotPart(botID state.UserID, channelName, reason string) error {
	u := s.world.FindByID(botID)
	if u == nil {
		return ErrUserNotFound
	}
	ch := s.world.FindChannel(channelName)
	if ch == nil || !ch.IsMember(botID) {
		return nil
	}
	params := []string{ch.Name()}
	if reason != "" {
		params = append(params, reason)
	}
	msg := &protocol.Message{
		Prefix:  u.Hostmask(),
		Command: "PART",
		Params:  params,
	}
	s.broadcastToChannel(ch, msg, 0, true)
	_, _, _ = s.world.PartChannel(botID, ch.Name())
	return nil
}

// BotPrivmsg delivers a PRIVMSG from the bot to target.
func (s *Server) BotPrivmsg(botID state.UserID, target, text string) error {
	return s.botDeliverMessage(botID, target, text, "PRIVMSG")
}

// BotNotice delivers a NOTICE from the bot to target.
func (s *Server) BotNotice(botID state.UserID, target, text string) error {
	return s.botDeliverMessage(botID, target, text, "NOTICE")
}

func (s *Server) botDeliverMessage(botID state.UserID, target, text, command string) error {
	u := s.world.FindByID(botID)
	if u == nil {
		return ErrUserNotFound
	}
	msg := &protocol.Message{
		Prefix:  u.Hostmask(),
		Command: command,
		Params:  []string{target, text},
	}
	if len(target) > 0 && (target[0] == '#' || target[0] == '&') {
		ch := s.world.FindChannel(target)
		if ch == nil {
			return ErrUserNotFound
		}
		// Exclude the bot itself from the broadcast. Delivering
		// back to the same session from within the session's
		// dispatch goroutine would deadlock on the inbox.
		s.broadcastToChannel(ch, msg, botID, false)
		return nil
	}
	dest := s.world.FindByNick(target)
	if dest == nil {
		return ErrUserNotFound
	}
	if c := s.connFor(dest.ID); c != nil {
		c.send(msg)
		return nil
	}
	if b := s.botFor(dest.ID); b != nil {
		b.Deliver(msg)
	}
	return nil
}
