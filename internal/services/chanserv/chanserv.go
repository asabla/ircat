// Package chanserv implements the ChanServ pseudo-user service for
// channel registration, access control, and auto-op. Registered
// channels are persisted in storage; the founder and access list
// determine who receives automatic operator or voice status on join.
package chanserv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/asabla/ircat/internal/storage"
)

// ChanServ is the service handler for ChanServ PRIVMSG commands.
type ChanServ struct {
	channels storage.RegisteredChannelStore
	accounts storage.AccountStore
	logger   *slog.Logger
}

// New returns a ChanServ backed by the given stores.
func New(channels storage.RegisteredChannelStore, accounts storage.AccountStore, logger *slog.Logger) *ChanServ {
	return &ChanServ{channels: channels, accounts: accounts, logger: logger}
}

// HandleMessage dispatches a ChanServ command and returns the
// response text to send back to the user.
func (cs *ChanServ) HandleMessage(ctx context.Context, senderNick, senderAccount string, text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "Unknown command. Available: REGISTER, DROP, OP, DEOP, INFO, SET"
	}
	switch strings.ToUpper(parts[0]) {
	case "REGISTER":
		return cs.handleRegister(ctx, senderNick, senderAccount, parts[1:])
	case "DROP":
		return cs.handleDrop(ctx, senderAccount, parts[1:])
	case "OP":
		return cs.handleOp(ctx, senderAccount, parts[1:])
	case "DEOP":
		return cs.handleDeop(ctx, senderAccount, parts[1:])
	case "INFO":
		return cs.handleInfo(ctx, parts[1:])
	case "SET":
		return cs.handleSet(ctx, senderAccount, parts[1:])
	default:
		return fmt.Sprintf("Unknown command %q. Available: REGISTER, DROP, OP, DEOP, INFO, SET", parts[0])
	}
}

// handleRegister registers a channel to the sender's account.
// Usage: REGISTER <#channel>
func (cs *ChanServ) handleRegister(ctx context.Context, senderNick, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to an account to register a channel."
	}
	if len(args) < 1 {
		return "Usage: REGISTER <#channel>"
	}
	channel := args[0]
	if !strings.HasPrefix(channel, "#") && !strings.HasPrefix(channel, "&") {
		return "Invalid channel name."
	}

	rc := &storage.RegisteredChannel{
		Channel:   channel,
		FounderID: senderAccount,
		Guard:     true,
	}
	if err := cs.channels.Create(ctx, rc); err != nil {
		if err == storage.ErrConflict {
			return fmt.Sprintf("Channel %s is already registered.", channel)
		}
		cs.logger.Warn("ChanServ REGISTER failed", "error", err)
		return "Internal error during registration."
	}

	return fmt.Sprintf("Channel %s has been registered to account %s.", channel, senderAccount)
}

// handleDrop unregisters a channel. Founder only.
// Usage: DROP <#channel>
func (cs *ChanServ) handleDrop(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to an account to drop a channel."
	}
	if len(args) < 1 {
		return "Usage: DROP <#channel>"
	}
	channel := args[0]

	rc, err := cs.channels.Get(ctx, channel)
	if err != nil {
		return fmt.Sprintf("Channel %s is not registered.", channel)
	}
	if rc.FounderID != senderAccount {
		return "Only the channel founder can drop a registration."
	}

	if err := cs.channels.Delete(ctx, channel); err != nil {
		cs.logger.Warn("ChanServ DROP failed", "error", err)
		return "Internal error during drop."
	}
	return fmt.Sprintf("Channel %s has been dropped.", channel)
}

// handleOp grants +o on a channel. Founder or anyone with "o" flag.
// Usage: OP <#channel> <nick>
func (cs *ChanServ) handleOp(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to use OP."
	}
	if len(args) < 2 {
		return "Usage: OP <#channel> <nick>"
	}
	channel := args[0]
	targetNick := args[1]

	if !cs.hasAccess(ctx, channel, senderAccount, "o") {
		return "You do not have access to OP on this channel."
	}

	// Return a directive the service layer interprets to set +o.
	return fmt.Sprintf("\x01OP %s %s", channel, targetNick)
}

// handleDeop removes +o on a channel. Same access rules as OP.
// Usage: DEOP <#channel> <nick>
func (cs *ChanServ) handleDeop(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to use DEOP."
	}
	if len(args) < 2 {
		return "Usage: DEOP <#channel> <nick>"
	}
	channel := args[0]
	targetNick := args[1]

	if !cs.hasAccess(ctx, channel, senderAccount, "o") {
		return "You do not have access to DEOP on this channel."
	}

	return fmt.Sprintf("\x01DEOP %s %s", channel, targetNick)
}

// handleInfo displays info about a registered channel.
// Usage: INFO <#channel>
func (cs *ChanServ) handleInfo(ctx context.Context, args []string) string {
	if len(args) < 1 {
		return "Usage: INFO <#channel>"
	}
	channel := args[0]

	rc, err := cs.channels.Get(ctx, channel)
	if err != nil {
		return fmt.Sprintf("Channel %s is not registered.", channel)
	}

	guardStr := "ON"
	if !rc.Guard {
		guardStr = "OFF"
	}
	keepTopicStr := "ON"
	if !rc.KeepTopic {
		keepTopicStr = "OFF"
	}
	return fmt.Sprintf("Channel: %s | Founder: %s | Guard: %s | KeepTopic: %s | Registered: %s",
		rc.Channel, rc.FounderID, guardStr, keepTopicStr, rc.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
}

// handleSet handles SET subcommands: GUARD ON/OFF, KEEPTOPIC ON/OFF.
// Usage: SET <#channel> <option> ON|OFF
func (cs *ChanServ) handleSet(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to use SET."
	}
	if len(args) < 3 {
		return "Usage: SET <#channel> <GUARD|KEEPTOPIC> ON|OFF"
	}
	channel := args[0]
	subCmd := strings.ToUpper(args[1])
	value := strings.ToUpper(args[2])

	rc, err := cs.channels.Get(ctx, channel)
	if err != nil {
		return fmt.Sprintf("Channel %s is not registered.", channel)
	}
	if rc.FounderID != senderAccount {
		return "Only the channel founder can change settings."
	}

	switch subCmd {
	case "GUARD":
		switch value {
		case "ON":
			rc.Guard = true
		case "OFF":
			rc.Guard = false
		default:
			return "Usage: SET <#channel> GUARD ON|OFF"
		}
		if err := cs.channels.Update(ctx, rc); err != nil {
			cs.logger.Warn("ChanServ SET GUARD failed", "error", err)
			return "Internal error."
		}
		return fmt.Sprintf("Guard for %s is now %s.", channel, value)

	case "KEEPTOPIC":
		switch value {
		case "ON":
			rc.KeepTopic = true
		case "OFF":
			rc.KeepTopic = false
		default:
			return "Usage: SET <#channel> KEEPTOPIC ON|OFF"
		}
		if err := cs.channels.Update(ctx, rc); err != nil {
			cs.logger.Warn("ChanServ SET KEEPTOPIC failed", "error", err)
			return "Internal error."
		}
		return fmt.Sprintf("KeepTopic for %s is now %s.", channel, value)

	default:
		return "Unknown SET option. Available: GUARD, KEEPTOPIC"
	}
}

// hasAccess checks whether the account is the founder or has the
// specified flag in the access list.
func (cs *ChanServ) hasAccess(ctx context.Context, channel, accountID, flag string) bool {
	rc, err := cs.channels.Get(ctx, channel)
	if err != nil {
		return false
	}
	if rc.FounderID == accountID {
		return true
	}
	ca, err := cs.channels.GetAccess(ctx, channel, accountID)
	if err != nil {
		return false
	}
	return strings.Contains(ca.Flags, flag)
}
