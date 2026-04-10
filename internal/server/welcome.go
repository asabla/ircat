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

	// RFC 2812 §3.1.1 server password gate. When the operator
	// has set Server.ClientPassword in the config, every client
	// connection must have sent a matching PASS before reaching
	// this point. We compare verbatim — there is no per-user
	// account system at this layer, just a single network-wide
	// gate. Empty config means "no password required".
	if want := c.server.cfg.Server.ClientPassword; want != "" {
		if c.pending.password != want {
			c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
				protocol.ERR_PASSWDMISMATCH, "Password incorrect"))
			c.sendError("Bad password")
			c.cancel(errors.New("bad password"))
			return
		}
	}

	user := &state.User{
		Nick:       c.pending.nick,
		User:       c.pending.user,
		Host:       c.remoteHost,
		Realname:   c.pending.realname,
		Registered: true,
		TS:         c.server.now().UnixNano(),
		Account:    c.pending.account,
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
	// Notify NickServ so it can check whether this nick is
	// registered and start enforcement if needed.
	c.server.notifyNickServ(user.Nick, user.Account)
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
		// Build a fresh slice rather than appending in-place: the
		// sub-slices returned by buildISupport share a backing
		// array, so an append-with-room would overwrite the
		// first byte of the next line.
		params := make([]string, 0, len(line)+1)
		params = append(params, line...)
		params = append(params, "are supported by this server")
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ISUPPORT, params...))
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
	// Tokens are split into chunks small enough to fit under the
	// RFC 2812 §2.3.1 15-parameter cap once the target nick and
	// the ":are supported by this server" trailing are added by
	// NumericReply. We pack 12 tokens per line to leave headroom.
	all := []string{
		"NETWORK=" + sanitizeISupport(s.cfg.Server.Network),
		"CASEMAPPING=" + s.world.CaseMapping().String(),
		fmt.Sprintf("NICKLEN=%d", limits.NickLength),
		fmt.Sprintf("CHANNELLEN=%d", limits.ChannelLength),
		fmt.Sprintf("TOPICLEN=%d", limits.TopicLength),
		fmt.Sprintf("AWAYLEN=%d", limits.AwayLength),
		fmt.Sprintf("KICKLEN=%d", limits.KickReasonLength),
		"CHANTYPES=#&+!",
		"PREFIX=(Oov)!@+",
		"CHANMODES=beIq,k,l,aimnpst",
		"MODES=4",
		"EXCEPTS=e",
		"INVEX=I",
		// TARGMAX advertises the per-command target list cap so
		// clients know how many comma-separated targets are
		// safe to pack into one PRIVMSG / NOTICE / JOIN / KICK
		// before the server starts replying with 407
		// ERR_TOOMANYTARGETS. The values mirror what production
		// ircds advertise.
		"TARGMAX=PRIVMSG:4,NOTICE:4,JOIN:10,PART:10,KICK:4",
	}
	const perLine = 12
	var out [][]string
	for i := 0; i < len(all); i += perLine {
		end := i + perLine
		if end > len(all) {
			end = len(all)
		}
		out = append(out, all[i:end])
	}
	return out
}

// sendMOTD streams the loaded MOTD lines to the client, framed by
// the standard MOTDSTART/ENDOFMOTD numerics. If no MOTD is configured
// we send ERR_NOMOTD instead.
func (c *Conn) sendMOTD() {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	lines := c.server.MOTDLines()
	if len(lines) == 0 {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOMOTD, "MOTD File is missing"))
		return
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_MOTDSTART,
		fmt.Sprintf("- %s Message of the day -", srv)))
	for _, line := range lines {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_MOTD, "- "+line))
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFMOTD, "End of MOTD command"))
}

// MOTDLines returns a snapshot of the current MOTD content
// under the read lock so the welcome path does not race a
// concurrent ReloadMOTD. Exported so the reload tests in
// cmd/ircat can assert on the value.
func (s *Server) MOTDLines() []string {
	s.motdMu.RLock()
	defer s.motdMu.RUnlock()
	return s.motd
}

// ReloadMOTD re-reads the configured motd_file and replaces the
// in-memory copy. Called by the SIGHUP / config-reload path in
// cmd/ircat. Safe to call from any goroutine.
func (s *Server) ReloadMOTD() {
	s.motdMu.RLock()
	path := s.cfg.Server.MOTDFile
	s.motdMu.RUnlock()
	lines := loadMOTD(path, s.logger)
	s.motdMu.Lock()
	s.motd = lines
	s.motdMu.Unlock()
}

// UpdateMOTDFile updates the configured motd_file path on the
// server's config copy. Used by the reload path so a new path
// from a fresh config snapshot takes effect on the next
// ReloadMOTD call. Locks under motdMu since the welcome path
// reads from the same struct.
func (s *Server) UpdateMOTDFile(path string) {
	s.motdMu.Lock()
	defer s.motdMu.Unlock()
	s.cfg.Server.MOTDFile = path
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
