package server

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// tryCompleteRegistration checks whether all the prerequisites for
// promoting this connection to a fully-registered state.User are
// satisfied, and if so does the promotion and sends the welcome
// burst.
//
// The prerequisites are:
//   - NICK has arrived (pending.nickSet)
//   - USER has arrived (pending.userSet)
//   - If CAP negotiation was opened (pending.capNegotiating), CAP
//     END has been received (pending.capEnded). Modern IRCv3 clients
//     send CAP LS, then NICK/USER, then CAP END; the welcome burst
//     must wait for CAP END or the client will think it missed an
//     ACK and the connection state will desync.
//
// It is called from handleNick, handleUser, and handleCap("END") —
// whichever event satisfies the last prerequisite triggers
// completion. Doing the world.AddUser at completion (rather than at
// NICK time) avoids the lost-nick race that historically plagued
// ircds where a client could send NICK then disconnect before USER,
// briefly squatting on a name.
func (c *Conn) tryCompleteRegistration() {
	if c.user != nil && c.user.Registered {
		return
	}
	if !c.pending.nickSet || !c.pending.userSet {
		return
	}
	if c.pending.capNegotiating && !c.pending.capEnded {
		return
	}

	user := &state.User{
		Nick:       c.pending.nick,
		User:       c.pending.user,
		Host:       c.remoteHost,
		Realname:   c.pending.realname,
		Registered: true,
	}
	if _, err := c.server.world.AddUser(user); err != nil {
		if errors.Is(err, state.ErrNickInUse) {
			c.send(protocol.NumericReply(c.server.cfg.Server.Name, "*",
				protocol.ERR_NICKNAMEINUSE, c.pending.nick, "Nickname is already in use"))
			// Reset the nick half of the registration so the client
			// can retry with a different nick without re-sending USER.
			c.pending.nick = ""
			c.pending.nickSet = false
			return
		}
		c.logger.Warn("AddUser failed", "error", err)
		c.sendError("Internal server error")
		c.cancel(err)
		return
	}
	c.user = user
	c.server.registerConn(c)
	c.logger.Info("registered", "nick", user.Nick, "user", user.User)
	c.server.announceUserToFederation(user)
	c.sendWelcomeBurst()
}

// sendWelcomeBurst emits the standard 001-005 welcome numerics plus
// the MOTD or its absence reply.
func (c *Conn) sendWelcomeBurst() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_WELCOME,
		fmt.Sprintf("Welcome to the %s Network, %s", c.server.cfg.Server.Network, c.user.Hostmask())))

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_YOURHOST,
		fmt.Sprintf("Your host is %s, running version ircat-0.0.1", srv)))

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_CREATED,
		fmt.Sprintf("This server was created %s", c.server.createdAt.UTC().Format("2006-01-02 15:04:05 UTC"))))

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_MYINFO,
		srv, "ircat-0.0.1", "iow", "biklmnopstv"))

	for _, line := range c.server.buildISupport() {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ISUPPORT,
			append(line, "are supported by this server")...))
	}

	c.sendMOTD()
}

// buildISupport assembles the RPL_ISUPPORT (005) tokens advertised
// to clients. Each returned slice is one numeric line worth of
// tokens (RFC says max 13 per line, we keep ours small for clarity).
//
// The :are supported by this server trailer is appended by the
// caller via NumericReply.
func (s *Server) buildISupport() [][]string {
	limits := s.cfg.Server.Limits
	tokens := []string{
		"NETWORK=" + sanitizeISupport(s.cfg.Server.Network),
		"CASEMAPPING=" + s.world.CaseMapping().String(),
		fmt.Sprintf("NICKLEN=%d", limits.NickLength),
		fmt.Sprintf("CHANNELLEN=%d", limits.ChannelLength),
		fmt.Sprintf("TOPICLEN=%d", limits.TopicLength),
		fmt.Sprintf("AWAYLEN=%d", limits.AwayLength),
		fmt.Sprintf("KICKLEN=%d", limits.KickReasonLength),
		fmt.Sprintf("CHANTYPES=#&"),
		"PREFIX=(ov)@+",
		"MODES=4",
	}
	return [][]string{tokens}
}

// sendMOTD streams the loaded MOTD lines to the client, framed by
// the standard MOTDSTART/ENDOFMOTD numerics. If no MOTD is configured
// we send ERR_NOMOTD instead.
func (c *Conn) sendMOTD() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	if len(c.server.motd) == 0 {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOMOTD, "MOTD File is missing"))
		return
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_MOTDSTART,
		fmt.Sprintf("- %s Message of the day -", srv)))
	for _, line := range c.server.motd {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_MOTD, "- "+line))
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFMOTD, "End of MOTD command"))
}

// loadMOTD reads the configured MOTD file at startup. A missing file
// or empty path returns nil and the welcome burst sends ERR_NOMOTD.
// We log a warning for unexpected errors but never fail startup.
func loadMOTD(path string, logger *slog.Logger) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		logger.Warn("could not read motd", "path", path, "error", err)
		return nil
	}
	out := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i, line := range out {
		out[i] = strings.TrimRight(line, "\r")
	}
	return out
}

// sanitizeISupport replaces SPACE in an ISUPPORT token's value with
// underscore so we never break the wire format. Networks with spaces
// in their name are mildly cursed but they exist.
func sanitizeISupport(s string) string {
	if !strings.ContainsAny(s, " \r\n\x00") {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\r', '\n', 0:
			return '_'
		}
		return r
	}, s)
}
