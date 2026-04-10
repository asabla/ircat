package nickserv

import (
	"context"
	"log/slog"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// ReplySender is the interface NickServ uses to send NOTICE replies
// back to users. The server satisfies this via connFor + send.
type ReplySender interface {
	SendNoticeToNick(from, target, text string)
}

// Service wraps a NickServ handler with the delivery plumbing
// needed to run as an in-process IRC service.
type Service struct {
	ns     *NickServ
	user   *state.User
	world  *state.World
	sender ReplySender
	logger *slog.Logger
}

// Start registers NickServ as a service user in the world and
// returns a Service that implements BotDeliverer (via Deliver).
// The caller should register the returned Service with
// Server.RegisterBot(user.ID, svc).
func Start(
	ctx context.Context,
	accounts storage.AccountStore,
	world *state.World,
	sender ReplySender,
	logger *slog.Logger,
) (*Service, error) {
	ns := New(accounts, logger)

	user := &state.User{
		Nick:                "NickServ",
		User:                "service",
		Host:                "services.",
		Realname:            "Nickname services",
		Registered:          true,
		Service:             true,
		ServiceType:         "NickServ",
		ServiceDistribution: "*",
	}
	id, err := world.AddUser(user)
	if err != nil {
		return nil, err
	}
	_ = id
	logger.Info("NickServ registered", "nick", user.Nick, "id", user.ID)

	return &Service{
		ns:     ns,
		user:   user,
		world:  world,
		sender: sender,
		logger: logger,
	}, nil
}

// User returns the state.User backing NickServ.
func (s *Service) User() *state.User { return s.user }

// Deliver implements the BotDeliverer interface. Called when a
// PRIVMSG or SQUERY is aimed at NickServ.
func (s *Service) Deliver(msg *protocol.Message) {
	if msg.Command != "PRIVMSG" && msg.Command != "SQUERY" {
		return
	}
	if len(msg.Params) < 2 {
		return
	}
	// Extract sender nick from the prefix "nick!user@host".
	senderNick := msg.Prefix
	if idx := strings.IndexByte(senderNick, '!'); idx >= 0 {
		senderNick = senderNick[:idx]
	}

	text := msg.Params[1]
	reply := s.ns.HandleMessage(context.Background(), senderNick, text)

	// If IDENTIFY succeeded, set the user's Account field.
	if strings.HasPrefix(reply, "You are now identified as ") {
		acctName := strings.TrimPrefix(reply, "You are now identified as ")
		acctName = strings.TrimSuffix(acctName, ".")
		if u := s.world.FindByNick(senderNick); u != nil {
			u.Account = acctName
		}
	}

	s.sender.SendNoticeToNick("NickServ!service@services.", senderNick, reply)
}
