package memoserv

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// ReplySender is the interface MemoServ uses to send NOTICE replies
// back to users.
type ReplySender interface {
	SendNoticeToNick(from, target, text string)
}

// Service wraps a MemoServ handler with the delivery plumbing
// needed to run as an in-process IRC service.
type Service struct {
	ms     *MemoServ
	user   *state.User
	world  *state.World
	sender ReplySender
	logger *slog.Logger
}

const memoservMask = "MemoServ!service@services."

// Start registers MemoServ as a service user in the world and
// returns a Service that implements BotDeliverer (via Deliver).
// The caller should register the returned Service with
// Server.RegisterBot(user.ID, svc).
func Start(
	ctx context.Context,
	memos storage.MemoStore,
	accounts storage.AccountStore,
	world *state.World,
	sender ReplySender,
	logger *slog.Logger,
) (*Service, error) {
	ms := New(memos, accounts, logger)

	user := &state.User{
		Nick:                "MemoServ",
		User:                "service",
		Host:                "services.",
		Realname:            "Memo services",
		Registered:          true,
		Service:             true,
		ServiceType:         "MemoServ",
		ServiceDistribution: "*",
	}
	if _, err := world.AddUser(user); err != nil {
		return nil, err
	}
	logger.Info("MemoServ registered", "nick", user.Nick, "id", user.ID)

	return &Service{
		ms:     ms,
		user:   user,
		world:  world,
		sender: sender,
		logger: logger,
	}, nil
}

// User returns the state.User backing MemoServ.
func (s *Service) User() *state.User { return s.user }

// Deliver implements the BotDeliverer interface. Called when a
// PRIVMSG or SQUERY is aimed at MemoServ.
func (s *Service) Deliver(msg *protocol.Message) {
	if msg.Command != "PRIVMSG" && msg.Command != "SQUERY" {
		return
	}
	if len(msg.Params) < 2 {
		return
	}
	senderNick := msg.Prefix
	if idx := strings.IndexByte(senderNick, '!'); idx >= 0 {
		senderNick = senderNick[:idx]
	}

	// Look up the sender's account.
	senderAccount := ""
	if u := s.world.FindByNick(senderNick); u != nil {
		senderAccount = u.Account
	}

	text := msg.Params[1]
	reply := s.ms.HandleMessage(context.Background(), senderNick, senderAccount, text)

	s.sender.SendNoticeToNick(memoservMask, senderNick, reply)
}

// NotifyUnread checks for unread memos and sends a NOTICE to the
// user if any exist. Called when a user identifies (SASL or
// NickServ IDENTIFY).
func (s *Service) NotifyUnread(nick, accountID string) {
	count, err := s.ms.memos.CountUnread(context.Background(), accountID)
	if err != nil {
		s.logger.Warn("MemoServ unread count failed", "account", accountID, "error", err)
		return
	}
	if count == 0 {
		return
	}
	s.sender.SendNoticeToNick(memoservMask, nick,
		fmt.Sprintf("You have %d unread memo(s). Use /msg MemoServ LIST to view them.", count))
}
