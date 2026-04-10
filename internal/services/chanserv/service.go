package chanserv

import (
	"context"
	"log/slog"
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// ReplySender is the interface ChanServ uses to send NOTICE replies,
// manipulate channel modes, and set channel topics.
type ReplySender interface {
	SendNoticeToNick(from, target, text string)
	// SetChannelMode sets a mode on a channel. The modeStr is e.g.
	// "+o" or "-o" and target is the nick affected.
	SetChannelMode(from, channel, modeStr, target string)
	// BroadcastChannelTopic broadcasts a topic change to a channel.
	// Used by KEEPTOPIC to restore a persisted topic when the
	// channel re-creates.
	BroadcastChannelTopic(from, channel, topic string)
}

// Service wraps a ChanServ handler with the delivery plumbing
// needed to run as an in-process IRC service.
type Service struct {
	cs              *ChanServ
	user            *state.User
	world           *state.World
	sender          ReplySender
	logger          *slog.Logger
	persistChannels storage.PersistentChannelStore
}

const chanservMask = "ChanServ!service@services."

// Start registers ChanServ as a service user in the world and
// returns a Service that implements BotDeliverer (via Deliver).
func Start(
	ctx context.Context,
	channels storage.RegisteredChannelStore,
	accounts storage.AccountStore,
	persistChannels storage.PersistentChannelStore,
	world *state.World,
	sender ReplySender,
	logger *slog.Logger,
) (*Service, error) {
	cs := New(channels, accounts, logger)

	user := &state.User{
		Nick:                "ChanServ",
		User:                "service",
		Host:                "services.",
		Realname:            "Channel services",
		Registered:          true,
		Service:             true,
		ServiceType:         "ChanServ",
		ServiceDistribution: "*",
	}
	if _, err := world.AddUser(user); err != nil {
		return nil, err
	}
	logger.Info("ChanServ registered", "nick", user.Nick, "id", user.ID)

	return &Service{
		cs:              cs,
		user:            user,
		world:           world,
		sender:          sender,
		logger:          logger,
		persistChannels: persistChannels,
	}, nil
}

// User returns the state.User backing ChanServ.
func (s *Service) User() *state.User { return s.user }

// Deliver implements the BotDeliverer interface. Called when a
// PRIVMSG or SQUERY is aimed at ChanServ.
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
	reply := s.cs.HandleMessage(context.Background(), senderNick, senderAccount, text)

	// Check for directive replies (OP/DEOP commands that need
	// the service layer to manipulate channel state).
	if strings.HasPrefix(reply, "\x01OP ") {
		parts := strings.Fields(reply[1:]) // skip \x01
		if len(parts) == 3 {
			channel := parts[1]
			targetNick := parts[2]
			s.doOp(channel, targetNick, senderNick)
		}
		return
	}
	if strings.HasPrefix(reply, "\x01DEOP ") {
		parts := strings.Fields(reply[1:])
		if len(parts) == 3 {
			channel := parts[1]
			targetNick := parts[2]
			s.doDeop(channel, targetNick, senderNick)
		}
		return
	}

	s.sender.SendNoticeToNick(chanservMask, senderNick, reply)
}

// doOp grants +o to targetNick on channel.
func (s *Service) doOp(channel, targetNick, requestor string) {
	target := s.world.FindByNick(targetNick)
	if target == nil {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			"No such nick: "+targetNick)
		return
	}
	ch := s.world.FindChannel(channel)
	if ch == nil {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			"No such channel: "+channel)
		return
	}
	if !ch.IsMember(target.ID) {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			targetNick+" is not in "+channel)
		return
	}
	ch.AddMembership(target.ID, state.MemberOp)
	s.sender.SetChannelMode(chanservMask, channel, "+o", targetNick)
	s.sender.SendNoticeToNick(chanservMask, requestor,
		targetNick+" has been opped in "+channel)
}

// doDeop removes +o from targetNick on channel.
func (s *Service) doDeop(channel, targetNick, requestor string) {
	target := s.world.FindByNick(targetNick)
	if target == nil {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			"No such nick: "+targetNick)
		return
	}
	ch := s.world.FindChannel(channel)
	if ch == nil {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			"No such channel: "+channel)
		return
	}
	if !ch.IsMember(target.ID) {
		s.sender.SendNoticeToNick(chanservMask, requestor,
			targetNick+" is not in "+channel)
		return
	}
	ch.RemoveMembership(target.ID, state.MemberOp)
	s.sender.SetChannelMode(chanservMask, channel, "-o", targetNick)
	s.sender.SendNoticeToNick(chanservMask, requestor,
		targetNick+" has been deopped in "+channel)
}

// CheckJoin is called by the server when a user joins a channel.
// If the channel is registered and the user has the "o" access
// flag (or is the founder with guard enabled), ChanServ
// automatically ops them.
func (s *Service) CheckJoin(nick, account, channel string) {
	if account == "" {
		return
	}

	rc, err := s.cs.channels.Get(context.Background(), channel)
	if err != nil {
		return // channel not registered
	}

	// Check if founder with guard enabled.
	if rc.FounderID == account && rc.Guard {
		s.autoOp(nick, channel)
		return
	}

	// Check access list for "o" flag.
	ca, err := s.cs.channels.GetAccess(context.Background(), channel, account)
	if err == nil && strings.Contains(ca.Flags, "o") {
		s.autoOp(nick, channel)
	}

	// KEEPTOPIC: if the channel was just re-created (only one
	// member) and the registered channel has KEEPTOPIC enabled,
	// restore the topic from persistent storage.
	if rc.KeepTopic {
		s.maybeRestoreTopic(channel)
	}
}

// maybeRestoreTopic restores a persisted topic to a channel that
// was just re-created (has exactly one member and no topic set).
func (s *Service) maybeRestoreTopic(channel string) {
	ch := s.world.FindChannel(channel)
	if ch == nil {
		return
	}
	// Only restore when the channel was just created (one member)
	// and the current topic is empty.
	topic, _, _ := ch.Topic()
	if ch.MemberCount() != 1 || topic != "" {
		return
	}
	if s.persistChannels == nil {
		return
	}
	rec, err := s.persistChannels.Get(context.Background(), channel)
	if err != nil || rec.Topic == "" {
		return
	}
	ch.SetTopic(rec.Topic, rec.TopicSetBy, rec.TopicSetAt)
	s.sender.BroadcastChannelTopic(chanservMask, channel, rec.Topic)
}

// autoOp grants +o to nick on channel silently.
func (s *Service) autoOp(nick, channel string) {
	target := s.world.FindByNick(nick)
	if target == nil {
		return
	}
	ch := s.world.FindChannel(channel)
	if ch == nil {
		return
	}
	if !ch.IsMember(target.ID) {
		return
	}
	ch.AddMembership(target.ID, state.MemberOp)
	s.sender.SetChannelMode(chanservMask, channel, "+o", nick)
}
