package nickserv

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// ReplySender is the interface NickServ uses to send NOTICE replies
// back to users and force nick changes.
type ReplySender interface {
	SendNoticeToNick(from, target, text string)
	// ForceNickChange renames a user on the server. Used by
	// enforcement to guest-rename unidentified users.
	ForceNickChange(oldNick, newNick string) bool
}

// Service wraps a NickServ handler with the delivery plumbing
// needed to run as an in-process IRC service.
type Service struct {
	ns     *NickServ
	user   *state.User
	world  *state.World
	sender ReplySender
	logger *slog.Logger

	// enforceTimeout is how long a user has to identify before
	// being renamed to a guest nick. Zero disables enforcement.
	enforceTimeout time.Duration

	// pending tracks users who are on a registered nick but have
	// not yet identified. The timer fires the guest rename.
	mu      sync.Mutex
	pending map[string]*time.Timer // nick -> timer
}

const nickservMask = "NickServ!service@services."

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
	if _, err := world.AddUser(user); err != nil {
		return nil, err
	}
	logger.Info("NickServ registered", "nick", user.Nick, "id", user.ID)

	return &Service{
		ns:             ns,
		user:           user,
		world:          world,
		sender:         sender,
		logger:         logger,
		enforceTimeout: 60 * time.Second,
		pending:        make(map[string]*time.Timer),
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
	senderNick := msg.Prefix
	if idx := strings.IndexByte(senderNick, '!'); idx >= 0 {
		senderNick = senderNick[:idx]
	}

	text := msg.Params[1]
	reply := s.ns.HandleMessage(context.Background(), senderNick, text)

	// If IDENTIFY succeeded, set the user's Account field and
	// cancel any pending enforcement timer.
	if strings.HasPrefix(reply, "You are now identified as ") {
		acctName := strings.TrimPrefix(reply, "You are now identified as ")
		acctName = strings.TrimSuffix(acctName, ".")
		if u := s.world.FindByNick(senderNick); u != nil {
			u.Account = acctName
		}
		s.cancelEnforcement(senderNick)
	}

	s.sender.SendNoticeToNick(nickservMask, senderNick, reply)
}

// CheckNick is called by the server when a user registers or
// changes nick. If the nick belongs to a registered account and
// the user is not identified, NickServ warns them and starts an
// enforcement timer.
func (s *Service) CheckNick(nick string, alreadyIdentified bool) {
	if alreadyIdentified {
		s.cancelEnforcement(nick)
		return
	}
	// Check if the nick is registered.
	if _, err := s.ns.accounts.Get(context.Background(), nick); err != nil {
		// Nick is not registered — nothing to enforce.
		return
	}

	s.sender.SendNoticeToNick(nickservMask, nick,
		fmt.Sprintf("This nickname is registered. You have %d seconds to identify via /msg NickServ IDENTIFY <password>.",
			int(s.enforceTimeout.Seconds())))

	if s.enforceTimeout <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Cancel any existing timer for this nick.
	if t, ok := s.pending[nick]; ok {
		t.Stop()
	}
	s.pending[nick] = time.AfterFunc(s.enforceTimeout, func() {
		s.enforceGuestRename(nick)
	})
}

// cancelEnforcement stops any pending enforcement timer for nick.
func (s *Service) cancelEnforcement(nick string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.pending[nick]; ok {
		t.Stop()
		delete(s.pending, nick)
	}
}

// enforceGuestRename forces a user to a guest nick if they still
// haven't identified.
func (s *Service) enforceGuestRename(nick string) {
	s.mu.Lock()
	delete(s.pending, nick)
	s.mu.Unlock()

	u := s.world.FindByNick(nick)
	if u == nil {
		return // user disconnected
	}
	if u.Account != "" {
		return // identified in the meantime
	}

	guest := fmt.Sprintf("Guest%d", rand.Intn(99999))
	s.sender.SendNoticeToNick(nickservMask, nick,
		fmt.Sprintf("Your nick has been changed to %s because you did not identify in time.", guest))
	s.sender.ForceNickChange(nick, guest)
	s.logger.Info("NickServ enforced guest rename", "from", nick, "to", guest)
}
