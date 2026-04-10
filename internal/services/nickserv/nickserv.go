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
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
)

// NickServ is the service handler for NickServ PRIVMSG commands.
type NickServ struct {
	accounts   storage.AccountStore
	nickOwners storage.NickOwnerStore
	logger     *slog.Logger
}

// New returns a NickServ backed by the given stores.
func New(accounts storage.AccountStore, nickOwners storage.NickOwnerStore, logger *slog.Logger) *NickServ {
	return &NickServ{accounts: accounts, nickOwners: nickOwners, logger: logger}
}

// HandleMessage dispatches a NickServ command and returns the
// response text to send back to the user. The account parameter
// is the caller's currently-identified account name (empty if not
// identified).
func (ns *NickServ) HandleMessage(ctx context.Context, nick, account, text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "Unknown command. Available: REGISTER, IDENTIFY, DROP, INFO, GHOST, RELEASE, GROUP"
	}
	switch strings.ToUpper(parts[0]) {
	case "REGISTER":
		return ns.handleRegister(ctx, nick, parts[1:])
	case "IDENTIFY":
		return ns.handleIdentify(ctx, nick, parts[1:])
	case "DROP":
		return ns.handleDrop(ctx, nick, account, parts[1:])
	case "INFO":
		return ns.handleInfo(ctx, parts[1:])
	case "GHOST":
		return ns.handleGhost(ctx, nick, account, parts[1:])
	case "RELEASE":
		return ns.handleRelease(ctx, nick, account, parts[1:])
	case "GROUP":
		return ns.handleGroup(ctx, nick, account)
	default:
		return fmt.Sprintf("Unknown command %q. Available: REGISTER, IDENTIFY, DROP, INFO, GHOST, RELEASE, GROUP", parts[0])
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

	// Create a primary nick_owners entry for the registering nick.
	no := &storage.NickOwner{
		Nick:      nick,
		AccountID: acct.ID,
		Primary:   true,
		CreatedAt: time.Now().UTC(),
	}
	if err := ns.nickOwners.Create(ctx, no); err != nil {
		ns.logger.Warn("NickServ REGISTER nick_owners create failed", "error", err)
		// Account was created successfully; the primary nick link
		// failing is non-fatal — the user can GROUP it later.
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

// handleGroup links the caller's current nick to their identified
// account. Usage: GROUP (no arguments — uses the current nick)
func (ns *NickServ) handleGroup(ctx context.Context, nick, account string) string {
	if account == "" {
		return "You must be identified to use GROUP."
	}

	acct, err := ns.accounts.Get(ctx, account)
	if err != nil {
		return "Internal error: your account was not found."
	}

	// Check if nick is already owned.
	existing, err := ns.nickOwners.Get(ctx, nick)
	if err == nil {
		if existing.AccountID == acct.ID {
			return fmt.Sprintf("Nick %q is already grouped to your account.", nick)
		}
		return fmt.Sprintf("Nick %q is owned by another account.", nick)
	}

	no := &storage.NickOwner{
		Nick:      nick,
		AccountID: acct.ID,
		Primary:   false,
		CreatedAt: time.Now().UTC(),
	}
	if err := ns.nickOwners.Create(ctx, no); err != nil {
		if err == storage.ErrConflict {
			return fmt.Sprintf("Nick %q is already owned.", nick)
		}
		ns.logger.Warn("NickServ GROUP failed", "error", err)
		return "Internal error during GROUP."
	}

	return fmt.Sprintf("Nick %q has been grouped to account %s.", nick, account)
}

// handleDrop removes a nick from the caller's account. If the nick
// is the primary nick for the account, the entire account is deleted.
// Usage: DROP [<nick>]
func (ns *NickServ) handleDrop(ctx context.Context, nick, account string, args []string) string {
	if account == "" {
		return "You must be identified to use DROP."
	}

	targetNick := nick
	if len(args) >= 1 {
		targetNick = args[0]
	}

	acct, err := ns.accounts.Get(ctx, account)
	if err != nil {
		return "Internal error: your account was not found."
	}

	// Verify the caller owns this nick.
	owner, err := ns.nickOwners.Get(ctx, targetNick)
	if err != nil {
		return fmt.Sprintf("Nick %q is not registered or not owned by you.", targetNick)
	}
	if owner.AccountID != acct.ID {
		return fmt.Sprintf("Nick %q is not owned by your account.", targetNick)
	}

	if owner.Primary {
		// Dropping the primary nick deletes the entire account.
		// The ON DELETE CASCADE in the schema handles nick_owners.
		if err := ns.accounts.Delete(ctx, account); err != nil {
			ns.logger.Warn("NickServ DROP account delete failed", "error", err)
			return "Internal error during DROP."
		}
		return fmt.Sprintf("Account %q and all grouped nicks have been dropped.", account)
	}

	// Dropping a grouped (non-primary) nick.
	if err := ns.nickOwners.Delete(ctx, targetNick); err != nil {
		ns.logger.Warn("NickServ DROP nick_owners delete failed", "error", err)
		return "Internal error during DROP."
	}

	return fmt.Sprintf("Nick %q has been dropped from your account.", targetNick)
}

// handleInfo shows registration information about a nick.
// Usage: INFO <nick>
func (ns *NickServ) handleInfo(ctx context.Context, args []string) string {
	if len(args) < 1 {
		return "Usage: INFO <nick>"
	}
	targetNick := args[0]

	owner, err := ns.nickOwners.Get(ctx, targetNick)
	if err != nil {
		// Fall back to checking if the nick matches an account
		// username directly (pre-GROUP era registrations).
		acct, err2 := ns.accounts.Get(ctx, targetNick)
		if err2 != nil {
			return fmt.Sprintf("Nick %q is not registered.", targetNick)
		}
		return fmt.Sprintf("Nick: %s | Account: %s | Registered: %s",
			targetNick, acct.Username, acct.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	}

	acct, err := ns.accounts.GetByID(ctx, owner.AccountID)
	if err != nil {
		return fmt.Sprintf("Nick %q is registered but the account could not be found.", targetNick)
	}

	// List all grouped nicks.
	grouped, _ := ns.nickOwners.ListByAccount(ctx, owner.AccountID)
	var nicks []string
	for _, g := range grouped {
		label := g.Nick
		if g.Primary {
			label += " (primary)"
		}
		nicks = append(nicks, label)
	}

	primaryStr := "no"
	if owner.Primary {
		primaryStr = "yes"
	}

	return fmt.Sprintf("Nick: %s | Account: %s | Primary: %s | Registered: %s | Grouped nicks: %s",
		targetNick, acct.Username, primaryStr,
		acct.CreatedAt.Format("2006-01-02 15:04:05 UTC"),
		strings.Join(nicks, ", "))
}

// handleGhost disconnects a user holding a nick the caller owns.
// Returns a \x01GHOST directive that the service layer interprets.
// Usage: GHOST <nick> [<password>]
func (ns *NickServ) handleGhost(ctx context.Context, callerNick, account string, args []string) string {
	if len(args) < 1 {
		return "Usage: GHOST <nick> [<password>]"
	}
	targetNick := args[0]

	if targetNick == callerNick {
		return "You cannot GHOST yourself."
	}

	// Two auth paths: already identified to the account that owns
	// the nick, or providing the password.
	if account != "" {
		acct, err := ns.accounts.Get(ctx, account)
		if err == nil {
			// Check if the caller's account owns the target nick.
			owner, err := ns.nickOwners.Get(ctx, targetNick)
			if err == nil && owner.AccountID == acct.ID {
				return fmt.Sprintf("\x01GHOST %s", targetNick)
			}
			// Also accept if the target nick matches the account username.
			if targetNick == acct.Username {
				return fmt.Sprintf("\x01GHOST %s", targetNick)
			}
		}
	}

	// Password-based auth.
	if len(args) < 2 {
		return "You do not own that nick. Usage: GHOST <nick> <password>"
	}
	password := args[1]

	// The target nick's account is the one we authenticate against.
	owner, err := ns.nickOwners.Get(ctx, targetNick)
	if err != nil {
		// Try direct account lookup.
		acct, err := ns.accounts.Get(ctx, targetNick)
		if err != nil {
			return fmt.Sprintf("Nick %q is not registered.", targetNick)
		}
		ok, err := auth.Verify(acct.PasswordHash, password)
		if err != nil || !ok {
			return "Invalid credentials."
		}
		return fmt.Sprintf("\x01GHOST %s", targetNick)
	}

	acct, err := ns.accounts.GetByID(ctx, owner.AccountID)
	if err != nil {
		return "Internal error."
	}
	ok, err := auth.Verify(acct.PasswordHash, password)
	if err != nil || !ok {
		return "Invalid credentials."
	}
	return fmt.Sprintf("\x01GHOST %s", targetNick)
}

// handleRelease releases a held nick so it can be used again. In our
// implementation, GHOST already disconnects the user so the nick
// becomes available. RELEASE cancels any pending enforcement hold.
// Returns a \x01RELEASE directive that the service layer interprets.
// Usage: RELEASE <nick> [<password>]
func (ns *NickServ) handleRelease(ctx context.Context, callerNick, account string, args []string) string {
	if len(args) < 1 {
		return "Usage: RELEASE <nick> [<password>]"
	}
	targetNick := args[0]

	// Same auth logic as GHOST.
	if account != "" {
		acct, err := ns.accounts.Get(ctx, account)
		if err == nil {
			owner, err := ns.nickOwners.Get(ctx, targetNick)
			if err == nil && owner.AccountID == acct.ID {
				return fmt.Sprintf("\x01RELEASE %s", targetNick)
			}
			if targetNick == acct.Username {
				return fmt.Sprintf("\x01RELEASE %s", targetNick)
			}
		}
	}

	if len(args) < 2 {
		return "You do not own that nick. Usage: RELEASE <nick> <password>"
	}
	password := args[1]

	owner, err := ns.nickOwners.Get(ctx, targetNick)
	if err != nil {
		acct, err := ns.accounts.Get(ctx, targetNick)
		if err != nil {
			return fmt.Sprintf("Nick %q is not registered.", targetNick)
		}
		ok, err := auth.Verify(acct.PasswordHash, password)
		if err != nil || !ok {
			return "Invalid credentials."
		}
		return fmt.Sprintf("\x01RELEASE %s", targetNick)
	}

	acct, err := ns.accounts.GetByID(ctx, owner.AccountID)
	if err != nil {
		return "Internal error."
	}
	ok, err := auth.Verify(acct.PasswordHash, password)
	if err != nil || !ok {
		return "Invalid credentials."
	}
	return fmt.Sprintf("\x01RELEASE %s", targetNick)
}
