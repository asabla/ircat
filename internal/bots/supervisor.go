package bots

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// IRCActuator is the interface the supervisor calls into for the
// IRC-side side effects a bot can produce (say, notice, join, part).
// It is implemented by internal/server.Server so the bot runtime
// stays decoupled from the wire format.
type IRCActuator interface {
	// BotPrivmsg delivers a PRIVMSG to target with the bot's hostmask
	// as prefix.
	BotPrivmsg(botID state.UserID, target, text string) error
	// BotNotice delivers a NOTICE similarly.
	BotNotice(botID state.UserID, target, text string) error
	// BotJoin registers the bot as a channel member and broadcasts
	// JOIN to existing members.
	BotJoin(botID state.UserID, channelName string) error
	// BotPart removes the bot from the channel and broadcasts PART.
	BotPart(botID state.UserID, channelName, reason string) error
}

// Options configures the [Supervisor].
type Options struct {
	Store       storage.Store
	World       *state.World
	IRCActuator IRCActuator
	Logger      *slog.Logger

	// OnBotStart is called every time a bot is promoted to a
	// running state (initial load, CreateBot, UpdateBot into
	// enabled). Used by cmd/ircat to register the session as a
	// server.BotDeliverer so channel broadcasts reach the bot.
	// Called BEFORE init() runs so any ctx:join side effect in
	// init() lands on the newly-registered session inbox.
	// Optional.
	OnBotStart func(userID state.UserID, session *Session)
	// OnBotStop mirrors OnBotStart for the teardown side.
	OnBotStop func(userID state.UserID)
}

// Supervisor owns the set of running Lua bots. It loads them from
// the configured BotStore on [Supervisor.Start] and provides
// Create/Update/Delete methods that hot-reload individual bots.
type Supervisor struct {
	opts   Options
	logger *slog.Logger

	mu      sync.Mutex
	running map[string]*botHandle // keyed by bot ID
}

// botHandle is the per-bot state the supervisor tracks.
type botHandle struct {
	id      string
	userID  state.UserID
	session *Session
	runtime *Runtime
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New constructs a Supervisor. Call Start to load and boot the
// configured bots.
func New(opts Options) *Supervisor {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{
		opts:    opts,
		logger:  logger,
		running: make(map[string]*botHandle),
	}
}

// Start loads every enabled bot from the store and boots them.
// Returns on the first fatal error. A bot with a compile error is
// logged and skipped; it does not block the other bots.
func (s *Supervisor) Start(ctx context.Context) error {
	if s.opts.Store == nil {
		s.logger.Info("bot supervisor disabled", "reason", "no store")
		return nil
	}
	bots, err := s.opts.Store.Bots().List(ctx)
	if err != nil {
		return fmt.Errorf("bots: list: %w", err)
	}
	for _, b := range bots {
		if !b.Enabled {
			continue
		}
		if err := s.startBot(ctx, b); err != nil {
			s.logger.Warn("bot start failed", "id", b.ID, "name", b.Name, "error", err)
		}
	}
	return nil
}

// Stop cancels every running bot and waits for them to drain.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	handles := make([]*botHandle, 0, len(s.running))
	for _, h := range s.running {
		handles = append(handles, h)
	}
	s.running = make(map[string]*botHandle)
	s.mu.Unlock()

	for _, h := range handles {
		h.cancel()
	}
	for _, h := range handles {
		h.wg.Wait()
		_ = h.runtime.DispatchShutdown(context.Background())
		h.runtime.Close()
		if s.opts.World != nil {
			s.opts.World.RemoveUser(h.userID)
		}
	}
}

// CreateBot persists a new bot and starts it if enabled. Returns
// the supplied Bot record with ID and timestamps filled in.
func (s *Supervisor) CreateBot(ctx context.Context, bot *storage.Bot) error {
	if bot.ID == "" {
		bot.ID = newBotID()
	}
	if err := s.opts.Store.Bots().Create(ctx, bot); err != nil {
		return err
	}
	if bot.Enabled {
		return s.startBot(ctx, *bot)
	}
	return nil
}

// UpdateBot persists changes and hot-reloads the bot. A change from
// enabled to disabled stops the running instance; a change in the
// other direction starts it.
func (s *Supervisor) UpdateBot(ctx context.Context, bot *storage.Bot) error {
	if err := s.opts.Store.Bots().Update(ctx, bot); err != nil {
		return err
	}
	s.stopBot(bot.ID)
	if bot.Enabled {
		return s.startBot(ctx, *bot)
	}
	return nil
}

// DeleteBot stops the running instance and deletes the stored record.
func (s *Supervisor) DeleteBot(ctx context.Context, id string) error {
	s.stopBot(id)
	return s.opts.Store.Bots().Delete(ctx, id)
}

// startBot compiles the Lua source, promotes the bot to a virtual
// state.User, registers it as a BotDeliverer on the server, and
// spawns the inbox-drain goroutine. Only called with no existing
// handle for the ID.
func (s *Supervisor) startBot(ctx context.Context, bot storage.Bot) error {
	if s.opts.World == nil {
		return errors.New("bots: no world")
	}
	user := &state.User{
		Nick:       bot.Name,
		User:       "bot",
		Host:       "ircat.local",
		Realname:   bot.Name,
		Modes:      "B",
		Registered: true,
	}
	id, err := s.opts.World.AddUser(user)
	if err != nil {
		return fmt.Errorf("add user: %w", err)
	}

	session := &Session{
		userID:   id,
		nickName: bot.Name,
		logger:   s.logger.With("bot", bot.Name),
		actuator: s.opts.IRCActuator,
		store:    s.opts.Store,
		botID:    bot.ID,
		inbox:    make(chan *protocol.Message, 64),
		now:      time.Now,
		logs:     newBotLogRing(DefaultBotLogCapacity),
	}

	rt, err := NewRuntime(bot.Source, session, DefaultBudget())
	if err != nil {
		s.opts.World.RemoveUser(id)
		return fmt.Errorf("compile: %w", err)
	}
	session.runtime = rt

	botCtx, cancel := context.WithCancel(ctx)
	h := &botHandle{
		id:      bot.ID,
		userID:  id,
		session: session,
		runtime: rt,
		ctx:     botCtx,
		cancel:  cancel,
	}

	s.mu.Lock()
	if prev, exists := s.running[bot.ID]; exists {
		s.mu.Unlock()
		prev.cancel()
		return errors.New("bots: already running")
	}
	s.running[bot.ID] = h
	s.mu.Unlock()

	// Register the session with the server's BotDeliverer registry
	// BEFORE init() runs, so any broadcast caused by init()'s
	// ctx:join lands on the newly-registered session inbox.
	if s.opts.OnBotStart != nil {
		s.opts.OnBotStart(id, session)
	}

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		_ = rt.DispatchInit(botCtx)
		session.runDispatchLoop(botCtx, rt)
	}()
	s.logger.Info("bot started", "id", bot.ID, "name", bot.Name, "user_id", id)
	return nil
}

// stopBot cancels the named bot's context and waits for it to drain.
// Safe to call on a bot that isn't running.
func (s *Supervisor) stopBot(id string) {
	s.mu.Lock()
	h, ok := s.running[id]
	if ok {
		delete(s.running, id)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if s.opts.OnBotStop != nil {
		s.opts.OnBotStop(h.userID)
	}
	h.cancel()
	h.wg.Wait()
	_ = h.runtime.DispatchShutdown(context.Background())
	h.runtime.Close()
	if s.opts.World != nil {
		s.opts.World.RemoveUser(h.userID)
	}
	s.logger.Info("bot stopped", "id", id)
}

// BotLogsSince returns the tail of log lines the named bot has
// emitted since the given sequence number, in chronological
// order. Returns nil if no bot with that id is currently
// running. Used by the dashboard SSE log-pane handler and the
// matching htmx target on the bot detail page.
func (s *Supervisor) BotLogsSince(id string, seq uint64) []BotLogEntry {
	s.mu.Lock()
	h, ok := s.running[id]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return h.session.LogsSince(seq)
}

// Sessions returns a snapshot of every running bot's (userID,
// session). Used by cmd/ircat to register the sessions as bot
// deliverers on the server after the supervisor has started them.
func (s *Supervisor) Sessions() map[state.UserID]*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[state.UserID]*Session, len(s.running))
	for _, h := range s.running {
		out[h.userID] = h.session
	}
	return out
}

// newBotID returns a new ULID-ish id. For M5 we just use a
// timestamp prefix plus a short random suffix; real ULIDs can
// follow in a later commit.
func newBotID() string {
	return fmt.Sprintf("bot_%x", time.Now().UnixNano())
}

// Session is the per-bot adapter. It implements:
//   - bots.Actions (the Lua ctx forwards into this)
//   - internal/server.BotDeliverer (inbox queue for broadcast
//     messages the server wants the bot to receive)
//
// The supervisor creates one per running bot. Safe for concurrent
// calls from the server's broadcast path (Deliver) and the bot's
// own dispatch goroutine (Say, JoinChannel, etc.) because the
// underlying runtime work happens on a single goroutine owned by
// the supervisor.
type Session struct {
	userID   state.UserID
	nickName string
	logger   *slog.Logger
	actuator IRCActuator
	store    storage.Store
	botID    string

	inbox chan *protocol.Message
	now   func() time.Time

	runtime *Runtime

	// logs is the per-bot tail the dashboard streams via SSE.
	// Every ctx:log() call the script makes lands here in
	// addition to the global slog sink, so operators watching
	// the bot detail page see lines immediately instead of
	// sifting the server-wide log tail.
	logs *botLogRing
}

// Deliver implements server.BotDeliverer. Non-blocking: a full
// inbox drops the message and logs once.
func (s *Session) Deliver(m *protocol.Message) {
	select {
	case s.inbox <- m:
	default:
		s.logger.Warn("bot inbox full, dropping message")
	}
}

// runDispatchLoop reads from the inbox and invokes the matching
// runtime event handler. Runs on a single goroutine owned by the
// supervisor so the runtime never sees concurrent dispatches for
// the same bot.
func (s *Session) runDispatchLoop(ctx context.Context, rt *Runtime) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.inbox:
			if !ok {
				return
			}
			s.dispatchOne(ctx, rt, msg)
		}
	}
}

func (s *Session) dispatchOne(ctx context.Context, rt *Runtime, msg *protocol.Message) {
	switch msg.Command {
	case "PRIVMSG":
		if len(msg.Params) < 2 {
			return
		}
		target := msg.Params[0]
		sender := senderFromPrefix(msg.Prefix)
		// Resolve the "reply target" presented to the Lua script.
		// For a channel PRIVMSG the script wants ctx:say(event.channel, ...)
		// to go back to the channel. For a direct message (target is
		// the bot's own nick) the script wants it to go back to the
		// sender. Setting event.channel to the sender for DMs gives
		// scripts one uniform "reply here" slot instead of making
		// every script resolve DM vs channel manually.
		if !isChannelTarget(target) && target == s.nickName {
			target = sender
		}
		ev := Event{
			Channel:  target,
			Sender:   sender,
			Hostmask: msg.Prefix,
			Text:     msg.Params[1],
		}
		if name, args := ExtractCommand(ev.Text); name != "" {
			ev.CommandName = name
			ev.CommandArgs = args
			if err := rt.DispatchCommand(ctx, ev); err != nil {
				s.logger.Warn("on_command failed", "error", err)
			}
			return
		}
		if err := rt.DispatchMessage(ctx, ev); err != nil {
			s.logger.Warn("on_message failed", "error", err)
		}
	case "JOIN":
		if len(msg.Params) < 1 {
			return
		}
		ev := Event{
			Channel:  msg.Params[0],
			Sender:   senderFromPrefix(msg.Prefix),
			Hostmask: msg.Prefix,
		}
		_ = rt.DispatchJoin(ctx, ev)
	case "PART":
		if len(msg.Params) < 1 {
			return
		}
		ev := Event{
			Channel:  msg.Params[0],
			Sender:   senderFromPrefix(msg.Prefix),
			Hostmask: msg.Prefix,
		}
		if len(msg.Params) > 1 {
			ev.Text = msg.Params[1]
		}
		_ = rt.DispatchPart(ctx, ev)
	}
}

func senderFromPrefix(prefix string) string {
	if i := strings.IndexByte(prefix, '!'); i >= 0 {
		return prefix[:i]
	}
	return prefix
}

// isChannelTarget reports whether a PRIVMSG target is a channel
// (starts with '#' or '&') rather than a nickname.
func isChannelTarget(target string) bool {
	return len(target) > 0 && (target[0] == '#' || target[0] == '&')
}

// Actions interface implementation follows.

func (s *Session) Say(target, text string) error {
	if s.actuator == nil {
		return nil
	}
	return s.actuator.BotPrivmsg(s.userID, target, text)
}
func (s *Session) Notice(target, text string) error {
	if s.actuator == nil {
		return nil
	}
	return s.actuator.BotNotice(s.userID, target, text)
}
func (s *Session) JoinChannel(channelName string) error {
	if s.actuator == nil {
		return nil
	}
	return s.actuator.BotJoin(s.userID, channelName)
}
func (s *Session) PartChannel(channelName, reason string) error {
	if s.actuator == nil {
		return nil
	}
	return s.actuator.BotPart(s.userID, channelName, reason)
}
func (s *Session) Nick() string { return s.nickName }
func (s *Session) Log(level, message string) {
	s.logger.Log(context.Background(), parseLogLevel(level), message)
	if s.logs != nil {
		s.logs.append(level, message, s.Now())
	}
}

// LogsSince returns every log entry the bot has emitted since
// the supplied sequence number, in chronological order. The
// zero value asks for the full ring buffer (up to the current
// capacity). Used by the dashboard SSE handler to seed a new
// log pane and then stream fresh lines as they land.
func (s *Session) LogsSince(seq uint64) []BotLogEntry {
	if s.logs == nil {
		return nil
	}
	return s.logs.since(seq)
}

// parseLogLevel maps a Lua-side level string to the matching slog
// level. Unknown strings fall back to Info.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	}
	return slog.LevelInfo
}
func (s *Session) Now() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Session) KVGet(key string) (string, error) {
	if s.store == nil {
		return "", errors.New("kv: no store")
	}
	return s.store.Bots().KV().Get(context.Background(), s.botID, key)
}
func (s *Session) KVSet(key, value string) error {
	if s.store == nil {
		return errors.New("kv: no store")
	}
	return s.store.Bots().KV().Set(context.Background(), s.botID, key, value)
}
func (s *Session) KVDelete(key string) error {
	if s.store == nil {
		return errors.New("kv: no store")
	}
	return s.store.Bots().KV().Delete(context.Background(), s.botID, key)
}

// UserID returns the virtual state.User ID assigned to this bot.
// Used by cmd/ircat to register the session as a BotDeliverer
// against the IRC server.
func (s *Session) UserID() state.UserID { return s.userID }
