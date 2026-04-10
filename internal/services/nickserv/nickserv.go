// Package nickserv implements the NickServ pseudo-user service for
// account registration and identification. It is the IRC-side
// complement to SASL PLAIN: clients that do not support IRCv3 SASL
// can REGISTER and IDENTIFY through PRIVMSG to NickServ instead.
package nickserv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
)

// NickServ is the service handler for NickServ PRIVMSG commands.
type NickServ struct {
	accounts storage.AccountStore
	logger   *slog.Logger
}

// New returns a NickServ backed by the given account store.
func New(accounts storage.AccountStore, logger *slog.Logger) *NickServ {
	return &NickServ{accounts: accounts, logger: logger}
}

// HandleMessage dispatches a NickServ command and returns the
// response text to send back to the user.
func (ns *NickServ) HandleMessage(ctx context.Context, nick, text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "Unknown command. Available: REGISTER, IDENTIFY, DROP, INFO, GHOST, RELEASE"
	}
	switch strings.ToUpper(parts[0]) {
	case "REGISTER":
		return ns.handleRegister(ctx, nick, parts[1:])
	case "IDENTIFY":
		return ns.handleIdentify(ctx, nick, parts[1:])
	case "DROP":
		return "Not yet implemented."
	case "INFO":
		return "Not yet implemented."
	case "GHOST":
		return "Not yet implemented."
	case "RELEASE":
		return "Not yet implemented."
	default:
		return fmt.Sprintf("Unknown command %q. Available: REGISTER, IDENTIFY, DROP, INFO, GHOST, RELEASE", parts[0])
	}
}

// handleRegister creates a new account with the caller's nick as
// the username. Usage: REGISTER <password> [<email>]
func (ns *NickServ) handleRegister(ctx context.Context, nick string, args []string) string {
	if len(args) < 1 {
		return "Usage: REGISTER <password> [<email>]"
	}
	password := args[0]
	email := ""
	if len(args) >= 2 {
		email = args[1]
	}

	// Check whether the account already exists.
	if _, err := ns.accounts.Get(ctx, nick); err == nil {
		return fmt.Sprintf("Account %q already exists.", nick)
	}

	hash, err := auth.Hash(auth.AlgorithmArgon2id, password, auth.DefaultArgon2idParams())
	if err != nil {
		ns.logger.Warn("NickServ REGISTER hash failed", "error", err)
		return "Internal error during registration."
	}

	acct := &storage.Account{
		ID:           nick, // simple ID for now; ULID can come later
		Username:     nick,
		PasswordHash: hash,
		Email:        email,
	}
	if err := ns.accounts.Create(ctx, acct); err != nil {
		if err == storage.ErrConflict {
			return fmt.Sprintf("Account %q already exists.", nick)
		}
		ns.logger.Warn("NickServ REGISTER create failed", "error", err)
		return "Internal error during registration."
	}

	return fmt.Sprintf("Account %q registered successfully. You can now IDENTIFY.", nick)
}

// handleIdentify verifies the caller's password against an existing
// account. Usage: IDENTIFY [<username>] <password>
func (ns *NickServ) handleIdentify(ctx context.Context, nick string, args []string) string {
	if len(args) < 1 {
		return "Usage: IDENTIFY [<username>] <password>"
	}

	var username, password string
	if len(args) >= 2 {
		username = args[0]
		password = args[1]
	} else {
		username = nick
		password = args[0]
	}

	acct, err := ns.accounts.Get(ctx, username)
	if err != nil {
		return "Invalid credentials."
	}

	ok, err := auth.Verify(acct.PasswordHash, password)
	if err != nil || !ok {
		return "Invalid credentials."
	}

	return fmt.Sprintf("You are now identified as %s.", username)
}
