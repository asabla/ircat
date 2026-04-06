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
	case "PRIVMSG":
		c.handlePrivmsg(m)
	case "NOTICE":
		c.handleNotice(m)
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

	// Post-registration NICK is a state mutation in the world.
	if c.user != nil && c.user.Registered {
		if err := c.server.world.RenameUser(c.user.ID, requested); err != nil {
			if errors.Is(err, state.ErrNickInUse) {
				c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
					protocol.ERR_NICKNAMEINUSE, requested, "Nickname is already in use"))
				return
			}
			c.logger.Warn("rename failed", "error", err)
			return
		}
		// Echo the change back to the client. Channel propagation is
		// M2 work; for now only the user themselves sees it.
		c.send(&protocol.Message{
			Prefix:  c.user.Hostmask(),
			Command: "NICK",
			Params:  []string{requested},
		})
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
