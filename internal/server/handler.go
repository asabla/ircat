package server

import (
	"errors"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// dispatch routes one inbound message to its handler.
//
// The dispatch table is intentionally a switch rather than a map of
// function pointers: at M1 the surface is small, the switch is
// faster, and the call sites are easier to read in a stack trace.
// We can pull it out into a table when M2 adds JOIN/PART/PRIVMSG and
// the switch starts to feel like a tax.
func (c *Conn) dispatch(m *protocol.Message) {
	switch m.Command {
	case "PASS":
		c.handlePass(m)
	case "NICK":
		c.handleNick(m)
	case "USER":
		c.handleUser(m)
	case "JOIN":
		c.handleJoin(m)
	case "PART":
		c.handlePart(m)
	case "KICK":
		c.handleKick(m)
	case "INVITE":
		c.handleInvite(m)
	case "OPER":
		c.handleOper(m)
	case "KILL":
		c.handleKill(m)
	case "PRIVMSG":
		c.handlePrivmsg(m)
	case "NOTICE":
		c.handleNotice(m)
	case "TOPIC":
		c.handleTopic(m)
	case "NAMES":
		c.handleNames(m)
	case "LIST":
		c.handleList(m)
	case "WHO":
		c.handleWho(m)
	case "WHOIS":
		c.handleWhois(m)
	case "WHOWAS":
		c.handleWhowas(m)
	case "MODE":
		c.handleMode(m)
	case "VERSION":
		c.handleVersion(m)
	case "TIME":
		c.handleTime(m)
	case "ADMIN":
		c.handleAdmin(m)
	case "INFO":
		c.handleInfo(m)
	case "MOTD":
		c.handleMotdCommand(m)
	case "LUSERS":
		c.handleLusers(m)
	case "AWAY":
		c.handleAway(m)
	case "WALLOPS":
		c.handleWallops(m)
	case "STATS":
		c.handleStats(m)
	case "USERHOST":
		c.handleUserhost(m)
	case "ISON":
		c.handleIson(m)
	case "PING":
		c.handlePing(m)
	case "PONG":
		// PONG just resets the activity clock, which the read loop
		// already did. Nothing to do here.
	case "QUIT":
		c.handleQuit(m)
	case "CAP":
		c.handleCap(m)
	default:
		// For unknown commands during registration we MUST send
		// ERR_NOTREGISTERED, not ERR_UNKNOWNCOMMAND, so the client
		// knows the next step is to register, not to fix a typo.
		if c.user == nil || !c.user.Registered {
			c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
				protocol.ERR_NOTREGISTERED, "You have not registered"))
			return
		}
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
			protocol.ERR_UNKNOWNCOMMAND, m.Command, "Unknown command"))
	}
}

// starOrNick returns the registered nick if available, otherwise the
// pre-registration "*" placeholder used by RFC 2812 §2.4.
func (c *Conn) starOrNick() string {
	if c.user != nil && c.user.Nick != "" {
		return c.user.Nick
	}
	if c.pending.nick != "" {
		return c.pending.nick
	}
	return "*"
}

// handlePass stores the supplied password for the registration step.
// In M1 we accept it but never compare against anything — server-
// side client passwords are a configurable feature on the M3+
// roadmap. We still validate the parameter count so a malformed
// PASS surfaces 461.
func (c *Conn) handlePass(m *protocol.Message) {
	if c.user != nil && c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
			protocol.ERR_ALREADYREGISTRED, "You may not reregister"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("PASS")
		return
	}
	c.pending.password = m.Params[0]
}

func (c *Conn) handleNick(m *protocol.Message) {
	if len(m.Params) < 1 || strings.TrimSpace(m.Params[0]) == "" {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NONICKNAMEGIVEN, "No nickname given"))
		return
	}
	requested := m.Params[0]
	if !validNickname(requested, c.server.cfg.Server.Limits.NickLength) {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_ERRONEUSNICKNAME, requested, "Erroneous nickname"))
		return
	}

	// Post-registration NICK is a state mutation in the world. The
	// NICK message must be broadcast to every channel the user is
	// currently in (so other clients can update their nick lists)
	// and echoed back to the user themselves. The broadcast carries
	// the *old* hostmask as the prefix so receivers can match the
	// announcement to whatever they had on file.
	if c.user != nil && c.user.Registered {
		oldMask := c.user.Hostmask()
		// Snapshot the pre-rename identity for WHOWAS so a later
		// lookup of the old nick still resolves to who they were.
		oldSnapshot := *c.user
		if err := c.server.world.RenameUser(c.user.ID, requested); err != nil {
			if errors.Is(err, state.ErrNickInUse) {
				c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
					protocol.ERR_NICKNAMEINUSE, requested, "Nickname is already in use"))
				return
			}
			c.logger.Warn("rename failed", "error", err)
			return
		}
		c.server.recordWhowas(&oldSnapshot)
		nickMsg := &protocol.Message{
			Prefix:  oldMask,
			Command: "NICK",
			Params:  []string{requested},
		}
		// Walk every channel the user is in and broadcast the NICK
		// to its members. We use a set to avoid sending the message
		// twice to a user who shares two channels with the renamer.
		seen := map[state.UserID]bool{c.user.ID: true}
		for _, ch := range c.server.world.UserChannels(c.user.ID) {
			for id := range ch.MemberIDs() {
				if seen[id] {
					continue
				}
				seen[id] = true
				if peer := c.server.connFor(id); peer != nil {
					peer.send(nickMsg)
				}
			}
		}
		// Always echo to the renamer themselves last so they see
		// the confirmation after any peers process it.
		c.send(nickMsg)
		// Forward the NICK change to every federation peer so
		// remote nodes can rename their copy of the user.
		c.server.forwardToAllLinks(nickMsg)
		return
	}

	// Pre-registration: stash the proposed nick. Collision check
	// happens at registration completion (when both NICK and USER
	// have arrived) so a slow client that sends NICK before USER
	// does not race with another client.
	c.pending.nick = requested
	c.pending.nickSet = true
	c.tryCompleteRegistration()
}

func (c *Conn) handleUser(m *protocol.Message) {
	if c.user != nil && c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
			protocol.ERR_ALREADYREGISTRED, "You may not reregister"))
		return
	}
	if len(m.Params) < 4 {
		c.sendNeedMoreParams("USER")
		return
	}
	c.pending.user = m.Params[0]
	// m.Params[1] (mode) and m.Params[2] (unused) are ignored on RFC
	// 2812 servers; we follow suit.
	c.pending.realname = m.Params[3]
	c.pending.userSet = true
	c.tryCompleteRegistration()
}

func (c *Conn) handlePing(m *protocol.Message) {
	// PING from a client is answered with PONG. The token (the only
	// param) is echoed verbatim if present, otherwise we use our
	// own server name.
	token := c.server.cfg.Server.Name
	if len(m.Params) > 0 {
		token = m.Params[0]
	}
	c.send(&protocol.Message{
		Prefix:  c.server.cfg.Server.Name,
		Command: "PONG",
		Params:  []string{c.server.cfg.Server.Name, token},
	})
}

func (c *Conn) handleQuit(m *protocol.Message) {
	reason := "Client Quit"
	if t, ok := m.Trailing(); ok && t != "" {
		reason = t
	}
	// Stash the reason so close() picks it up for the QUIT broadcast
	// to channel peers. We do not call broadcastQuit here directly
	// because the close() path already runs it; doing it twice
	// would duplicate the message on every peer.
	c.quitReason = reason
	c.sendError(reason)
	c.cancel(errors.New("quit: " + reason))
}

// handleCap implements just enough of the IRCv3 capability
// negotiation handshake that modern clients can complete connection.
// We do not advertise any capabilities in M1, but we still need to
// honour the negotiation lifecycle — specifically, when the client
// has opted into CAP, registration must wait for CAP END before the
// welcome burst goes out.
func (c *Conn) handleCap(m *protocol.Message) {
	if len(m.Params) == 0 {
		return
	}
	subcommand := strings.ToUpper(m.Params[0])
	switch subcommand {
	case "LS":
		// "We support nothing right now."
		c.pending.capNegotiating = true
		c.send(&protocol.Message{
			Prefix:  c.server.cfg.Server.Name,
			Command: "CAP",
			Params:  []string{c.starOrNick(), "LS", ""},
		})
	case "LIST":
		c.send(&protocol.Message{
			Prefix:  c.server.cfg.Server.Name,
			Command: "CAP",
			Params:  []string{c.starOrNick(), "LIST", ""},
		})
	case "REQ":
		// Refuse all requests by NAKing them. M1 supports no caps.
		c.pending.capNegotiating = true
		req := ""
		if len(m.Params) > 1 {
			req = m.Params[1]
		}
		c.send(&protocol.Message{
			Prefix:  c.server.cfg.Server.Name,
			Command: "CAP",
			Params:  []string{c.starOrNick(), "NAK", req},
		})
	case "END":
		// Negotiation finished — if NICK and USER have already
		// arrived, this is the trigger that releases the welcome
		// burst. Otherwise it's recorded for whichever of NICK/USER
		// arrives last.
		c.pending.capEnded = true
		c.tryCompleteRegistration()
	}
}

func (c *Conn) sendNeedMoreParams(cmd string) {
	c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
		protocol.ERR_NEEDMOREPARAMS, cmd, "Not enough parameters"))
}

// validNickname checks the (very loose) RFC 2812 §2.3.1 nickname
// grammar. We do not require the first character to be a letter
// strictly per RFC because most modern networks allow nicks starting
// with the special punctuation; instead we forbid only digits and
// '-' as the first byte.
func validNickname(s string, maxLen int) bool {
	if maxLen <= 0 {
		maxLen = 30
	}
	if s == "" || len(s) > maxLen {
		return false
	}
	first := s[0]
	if (first >= '0' && first <= '9') || first == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isNickByte(s[i]) {
			return false
		}
	}
	return true
}

func isNickByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
	case c >= 'a' && c <= 'z':
	case c >= '0' && c <= '9':
	case c == '-' || c == '_' || c == '[' || c == ']' || c == '\\' ||
		c == '`' || c == '^' || c == '{' || c == '}' || c == '|' || c == '~':
	default:
		return false
	}
	return true
}
