package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/storage"
)

// handleAuthenticate implements the IRCv3 SASL PLAIN authentication
// exchange. The flow is:
//
//  1. Client sends "AUTHENTICATE PLAIN".
//  2. Server responds with "AUTHENTICATE +" (ready for payload).
//  3. Client sends "AUTHENTICATE <base64>" where the decoded payload
//     is: authzid \x00 authcid \x00 password.
//  4. Server verifies the credentials against the AccountStore and
//     replies with 900 + 903 (success) or 904 (failure).
//
// Only the PLAIN mechanism is supported; any other mechanism name
// gets a 904 ERR_SASLFAIL.
func (c *Conn) handleAuthenticate(m *protocol.Message) {
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("AUTHENTICATE")
		return
	}

	// Already registered — SASL is a pre-registration mechanism.
	if c.user != nil && c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.user.Nick,
			protocol.ERR_SASLABORTED, "SASL authentication aborted"))
		return
	}

	arg := m.Params[0]

	// Step 1: mechanism selection.
	if arg == "PLAIN" {
		c.send(&protocol.Message{
			Command: "AUTHENTICATE",
			Params:  []string{"+"},
		})
		return
	}

	// Reject other mechanism names (anything alphabetic that isn't
	// a base64 payload). A real mechanism name is all uppercase
	// ASCII letters/digits/hyphens — base64 payloads will contain
	// lowercase, +, /, or =. We use a simple heuristic: if the
	// argument is all uppercase and doesn't contain + or /, it is
	// a mechanism name we don't support.
	if arg == "*" {
		// Client aborted the exchange.
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLABORTED, "SASL authentication aborted"))
		return
	}

	// Step 3: base64-encoded payload.
	if len(arg) > 400 {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLTOOLONG, "SASL message too long"))
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(arg)
	if err != nil {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLFAIL, "SASL authentication failed"))
		return
	}

	// PLAIN format: authzid \x00 authcid \x00 password
	parts := bytes.SplitN(decoded, []byte{0}, 3)
	if len(parts) != 3 {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLFAIL, "SASL authentication failed"))
		return
	}

	authcid := string(parts[1])
	password := string(parts[2])

	if authcid == "" || password == "" {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLFAIL, "SASL authentication failed"))
		return
	}

	acct, err := c.server.store.Accounts().Get(c.ctx, authcid)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
				protocol.ERR_SASLFAIL, "SASL authentication failed"))
			return
		}
		c.logger.Warn("SASL account lookup failed", "error", err)
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLFAIL, "SASL authentication failed"))
		return
	}

	ok, err := auth.Verify(acct.PasswordHash, password)
	if err != nil || !ok {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_SASLFAIL, "SASL authentication failed"))
		return
	}

	// Success — record the account and inform the client.
	c.pending.account = authcid

	nick := c.starOrNick()
	hostmask := nick + "!" + nick + "@" + c.remoteHost
	c.send(protocol.NumericReply(c.server.cfg.Server.Name, nick,
		protocol.RPL_LOGGEDIN,
		hostmask, authcid,
		fmt.Sprintf("You are now logged in as %s", authcid)))
	c.send(protocol.NumericReply(c.server.cfg.Server.Name, nick,
		protocol.RPL_SASLSUCCESS, "SASL authentication successful"))
}
