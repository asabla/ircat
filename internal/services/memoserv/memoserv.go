// Package memoserv implements the MemoServ pseudo-user service for
// offline memo delivery between registered accounts. Users interact
// with MemoServ via PRIVMSG or SQUERY commands: SEND, LIST, READ,
// and DELETE.
package memoserv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// MemoServ is the service handler for MemoServ PRIVMSG commands.
type MemoServ struct {
	memos    storage.MemoStore
	accounts storage.AccountStore
	logger   *slog.Logger
}

// New returns a MemoServ backed by the given stores.
func New(memos storage.MemoStore, accounts storage.AccountStore, logger *slog.Logger) *MemoServ {
	return &MemoServ{memos: memos, accounts: accounts, logger: logger}
}

// HandleMessage dispatches a MemoServ command and returns the
// response text to send back to the user.
func (ms *MemoServ) HandleMessage(ctx context.Context, senderNick, senderAccount, text string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "Unknown command. Available: SEND, LIST, READ, DELETE, PURGE"
	}
	switch strings.ToUpper(parts[0]) {
	case "SEND":
		return ms.handleSend(ctx, senderNick, senderAccount, parts[1:])
	case "LIST":
		return ms.handleList(ctx, senderAccount)
	case "READ":
		return ms.handleRead(ctx, senderAccount, parts[1:])
	case "DELETE":
		return ms.handleDelete(ctx, senderAccount, parts[1:])
	case "PURGE":
		return ms.handlePurge(ctx, senderAccount)
	default:
		return fmt.Sprintf("Unknown command %q. Available: SEND, LIST, READ, DELETE, PURGE", parts[0])
	}
}

// handleSend creates a memo to a recipient identified by nick.
// Usage: SEND <nick> <message>
func (ms *MemoServ) handleSend(ctx context.Context, senderNick, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to send memos."
	}
	if len(args) < 2 {
		return "Usage: SEND <nick> <message>"
	}
	targetNick := args[0]
	body := strings.Join(args[1:], " ")

	// Look up recipient account by nick (nick == username in our model).
	recipientAcct, err := ms.accounts.Get(ctx, targetNick)
	if err != nil {
		return fmt.Sprintf("Account %q not found.", targetNick)
	}

	// Look up sender account to get the ID.
	senderAcct, err := ms.accounts.Get(ctx, senderAccount)
	if err != nil {
		ms.logger.Warn("MemoServ SEND: sender account lookup failed",
			"sender", senderAccount, "error", err)
		return "Internal error: your account was not found."
	}

	memo := &storage.Memo{
		ID:          generateID(),
		SenderID:    senderAcct.ID,
		RecipientID: recipientAcct.ID,
		Body:        body,
		CreatedAt:   time.Now().UTC(),
	}
	if err := ms.memos.Send(ctx, memo); err != nil {
		ms.logger.Warn("MemoServ SEND failed", "error", err)
		return "Failed to send memo."
	}
	return fmt.Sprintf("Memo sent to %s.", targetNick)
}

// handleList shows all memos for the caller's account.
func (ms *MemoServ) handleList(ctx context.Context, senderAccount string) string {
	if senderAccount == "" {
		return "You must be identified to list memos."
	}
	acct, err := ms.accounts.Get(ctx, senderAccount)
	if err != nil {
		return "Internal error: your account was not found."
	}
	memos, err := ms.memos.ListForRecipient(ctx, acct.ID)
	if err != nil {
		ms.logger.Warn("MemoServ LIST failed", "error", err)
		return "Failed to list memos."
	}
	if len(memos) == 0 {
		return "You have no memos."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You have %d memo(s):", len(memos)))
	for _, m := range memos {
		readMark := " "
		if m.Read {
			readMark = "R"
		}
		// Look up sender username for display.
		senderName := m.SenderID
		if sa, err := ms.accounts.GetByID(ctx, m.SenderID); err == nil {
			senderName = sa.Username
		}
		sb.WriteString(fmt.Sprintf(" | [%s] %s from %s (%s)",
			readMark, m.ID, senderName, m.CreatedAt.Format("2006-01-02 15:04")))
	}
	return sb.String()
}

// handleRead shows a memo's body and marks it as read.
// Usage: READ <id>
func (ms *MemoServ) handleRead(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to read memos."
	}
	if len(args) < 1 {
		return "Usage: READ <id>"
	}
	id := args[0]

	acct, err := ms.accounts.Get(ctx, senderAccount)
	if err != nil {
		return "Internal error: your account was not found."
	}

	memo, err := ms.memos.Get(ctx, id)
	if err != nil {
		return "Memo not found."
	}
	if memo.RecipientID != acct.ID {
		return "Memo not found."
	}

	// Mark as read.
	_ = ms.memos.MarkRead(ctx, id)

	senderName := memo.SenderID
	if sa, err := ms.accounts.GetByID(ctx, memo.SenderID); err == nil {
		senderName = sa.Username
	}

	return fmt.Sprintf("Memo from %s (%s): %s",
		senderName, memo.CreatedAt.Format("2006-01-02 15:04"), memo.Body)
}

// handleDelete removes a memo.
// Usage: DELETE <id>
func (ms *MemoServ) handleDelete(ctx context.Context, senderAccount string, args []string) string {
	if senderAccount == "" {
		return "You must be identified to delete memos."
	}
	if len(args) < 1 {
		return "Usage: DELETE <id>"
	}
	id := args[0]

	acct, err := ms.accounts.Get(ctx, senderAccount)
	if err != nil {
		return "Internal error: your account was not found."
	}

	memo, err := ms.memos.Get(ctx, id)
	if err != nil {
		return "Memo not found."
	}
	if memo.RecipientID != acct.ID {
		return "Memo not found."
	}

	if err := ms.memos.Delete(ctx, id); err != nil {
		ms.logger.Warn("MemoServ DELETE failed", "error", err)
		return "Failed to delete memo."
	}
	return "Memo deleted."
}

// handlePurge manually triggers a memo purge for the caller's account.
// Usage: PURGE
func (ms *MemoServ) handlePurge(ctx context.Context, senderAccount string) string {
	if senderAccount == "" {
		return "You must be identified to purge memos."
	}
	acct, err := ms.accounts.Get(ctx, senderAccount)
	if err != nil {
		return "Internal error: your account was not found."
	}
	memos, err := ms.memos.ListForRecipient(ctx, acct.ID)
	if err != nil {
		ms.logger.Warn("MemoServ PURGE list failed", "error", err)
		return "Failed to purge memos."
	}
	var count int
	for _, m := range memos {
		if m.Read {
			if err := ms.memos.Delete(ctx, m.ID); err == nil {
				count++
			}
		}
	}
	return fmt.Sprintf("Purged %d read memo(s).", count)
}

// generateID produces a simple unique ID for memos. Uses a
// timestamp-based approach for simplicity; a ULID library could
// replace this later.
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
