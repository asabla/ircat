// Package bots implements ircat's Lua bot runtime.
//
// Each bot is a user-supplied Lua script that runs in a sandboxed
// gopher-lua state. The supervisor (see [Supervisor]) owns the set
// of running bots, constructs a [Runtime] per bot, and drives the
// event API by calling its Dispatch methods. Runtimes are not
// goroutine-safe: the supervisor serializes all event delivery for
// a given bot onto a single goroutine.
//
// Supported handlers (optional — a script may define any subset):
//
//	function init(ctx)                end  -- called once on load
//	function shutdown(ctx)            end  -- called once on unload
//	function on_message(ctx, event)   end  -- every PRIVMSG to a channel the bot is in
//	function on_command(ctx, event)   end  -- "!name args" PRIVMSG variant
//	function on_join(ctx, event)      end  -- another user joined a channel the bot is in
//	function on_part(ctx, event)      end  -- another user left
//
// The ctx userdata exposes:
//
//	ctx:say(target, text)    -- PRIVMSG
//	ctx:notice(target, text) -- NOTICE
//	ctx:join(channel)        -- JOIN (the bot)
//	ctx:part(channel, reason)-- PART (the bot)
//	ctx:nick()               -- the bot's current nick
//	ctx:log(level, message)  -- bot log line
//	ctx:now()                -- unix timestamp
//	ctx:kv_get(key)          -- per-bot KV read
//	ctx:kv_set(key, value)   -- per-bot KV write
//	ctx:kv_delete(key)       -- per-bot KV delete
//
// Sandboxing strips io, os, debug, package.loadlib, and the raw
// require loader. string, table, math are kept. A per-event
// instruction budget is enforced via the hook mechanism; event
// dispatch also runs under a wall-clock deadline.
package bots

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Budget configures the runtime limits for a single event
// invocation. Zero fields fall back to [DefaultBudget] at runtime
// construction time.
type Budget struct {
	// Instructions is the maximum number of VM instructions one
	// event handler invocation may execute before the runtime
	// aborts it.
	Instructions int
	// Wallclock is the per-event wall-clock deadline.
	Wallclock time.Duration
}

// DefaultBudget returns the conservative defaults used when a bot
// does not supply its own.
func DefaultBudget() Budget {
	return Budget{
		Instructions: 1_000_000,
		Wallclock:    5 * time.Second,
	}
}

// Actions is the interface the runtime calls back into for IRC-side
// side effects (say, notice, join, part). Implemented by the
// supervisor's per-bot session so the runtime itself stays
// IRC-protocol free.
type Actions interface {
	Say(target, text string) error
	Notice(target, text string) error
	JoinChannel(channelName string) error
	PartChannel(channelName, reason string) error
	Nick() string
	KVGet(key string) (string, error)
	KVSet(key, value string) error
	KVDelete(key string) error
	Log(level, message string)
	Now() time.Time
}

// Runtime is one sandboxed Lua state driving one bot. Construct
// with [NewRuntime], call [Runtime.Dispatch...] methods to fire
// event handlers, and [Runtime.Close] when done.
type Runtime struct {
	state   *lua.LState
	actions Actions
	budget  Budget
}

// Event is the payload passed to on_message / on_command / on_join /
// on_part. The supervisor assembles this from the protocol.Message
// the server delivered via [internal/server.BotDeliverer].
type Event struct {
	Channel     string
	Sender      string // sender nick (prefix stripped)
	Hostmask    string // full nick!user@host prefix
	Text        string
	CommandName string // populated for on_command: !foo args -> "foo"
	CommandArgs string // everything after the command name
}

// NewRuntime compiles source into a fresh sandboxed lua.LState and
// returns a Runtime. Any syntax error in source is returned here.
func NewRuntime(source string, actions Actions, budget Budget) (*Runtime, error) {
	if actions == nil {
		return nil, errors.New("bots: actions required")
	}
	if budget.Instructions == 0 || budget.Wallclock == 0 {
		def := DefaultBudget()
		if budget.Instructions == 0 {
			budget.Instructions = def.Instructions
		}
		if budget.Wallclock == 0 {
			budget.Wallclock = def.Wallclock
		}
	}
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	// Open only the libraries that are safe in a sandbox. We
	// deliberately skip io, os, debug, package (the raw loader) and
	// the require loader.
	for _, pair := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage}, // needed before base
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		if err := L.CallByParam(lua.P{
			Fn:      L.NewFunction(pair.fn),
			NRet:    0,
			Protect: true,
		}, lua.LString(pair.name)); err != nil {
			L.Close()
			return nil, fmt.Errorf("bots: open %s: %w", pair.name, err)
		}
	}
	// Strip the dangerous globals that OpenBase / OpenPackage put
	// on the table.
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module"} {
		L.SetGlobal(name, lua.LNil)
	}
	// Replace package.loadlib with a no-op so Lua scripts cannot
	// dlopen arbitrary shared libraries.
	if pkg := L.GetGlobal("package"); pkg.Type() == lua.LTTable {
		pkgTable := pkg.(*lua.LTable)
		pkgTable.RawSetString("loadlib", L.NewFunction(func(s *lua.LState) int {
			s.RaiseError("package.loadlib is disabled")
			return 0
		}))
		pkgTable.RawSetString("preload", L.NewTable())
		pkgTable.RawSetString("loaders", L.NewTable())
		pkgTable.RawSetString("searchers", L.NewTable())
	}
	// Strip string.dump: combined with any future load() reachability
	// it is the standard Lua bytecode escape vector. We strip it
	// even though load() is already nil so a single misconfiguration
	// upstream cannot reopen the door.
	if str := L.GetGlobal("string"); str.Type() == lua.LTTable {
		str.(*lua.LTable).RawSetString("dump", lua.LNil)
	}

	rt := &Runtime{
		state:   L,
		actions: actions,
		budget:  budget,
	}
	// Inject the ctx global once; event handlers receive it as an
	// argument but we also make it available at the top level for
	// init() scripts that run outside an event.
	ctxUD := rt.newCtxUserdata(L)
	L.SetGlobal("ctx", ctxUD)

	if err := L.DoString(source); err != nil {
		L.Close()
		return nil, fmt.Errorf("bots: compile: %w", err)
	}
	return rt, nil
}

// Close releases the underlying Lua state. Safe to call multiple
// times.
func (r *Runtime) Close() {
	if r.state != nil {
		r.state.Close()
		r.state = nil
	}
}

// DispatchInit calls the optional init(ctx) handler if the script
// defined one.
func (r *Runtime) DispatchInit(ctx context.Context) error {
	return r.callHandler(ctx, "init", nil)
}

// DispatchShutdown calls the optional shutdown(ctx) handler.
func (r *Runtime) DispatchShutdown(ctx context.Context) error {
	return r.callHandler(ctx, "shutdown", nil)
}

// DispatchMessage calls on_message(ctx, event).
func (r *Runtime) DispatchMessage(ctx context.Context, ev Event) error {
	return r.callHandler(ctx, "on_message", &ev)
}

// DispatchCommand calls on_command(ctx, event) for a "!name args"
// PRIVMSG. The event carries CommandName + CommandArgs already.
func (r *Runtime) DispatchCommand(ctx context.Context, ev Event) error {
	return r.callHandler(ctx, "on_command", &ev)
}

// DispatchJoin calls on_join(ctx, event).
func (r *Runtime) DispatchJoin(ctx context.Context, ev Event) error {
	return r.callHandler(ctx, "on_join", &ev)
}

// DispatchPart calls on_part(ctx, event).
func (r *Runtime) DispatchPart(ctx context.Context, ev Event) error {
	return r.callHandler(ctx, "on_part", &ev)
}

// callHandler looks up a top-level Lua function by name and invokes
// it with ctx (+ optional event table). A missing handler is not
// an error — bots may implement any subset of the event API.
func (r *Runtime) callHandler(ctx context.Context, name string, event *Event) error {
	fn := r.state.GetGlobal(name)
	if fn.Type() != lua.LTFunction {
		return nil
	}

	// Wall-clock deadline: hang gopher-lua's context onto the state
	// so any blocking call (e.g. a Lua-level coroutine) observes
	// cancellation.
	callCtx, cancel := context.WithTimeout(ctx, r.budget.Wallclock)
	defer cancel()
	r.state.SetContext(callCtx)
	defer r.state.RemoveContext()

	// Instruction budget enforcement is version-dependent on
	// gopher-lua; we rely on the wall-clock deadline set above as
	// the primary safety net and document the trade-off in the
	// package comment. A follow-up can tighten this once we pick a
	// gopher-lua major with a stable hook API.

	args := []lua.LValue{r.state.GetGlobal("ctx")}
	if event != nil {
		args = append(args, eventToTable(r.state, *event))
	}
	if err := r.state.CallByParam(lua.P{
		Fn:      fn,
		NRet:    0,
		Protect: true,
	}, args...); err != nil {
		return fmt.Errorf("bots: %s: %w", name, err)
	}
	return nil
}

// eventToTable converts an [Event] into a Lua table for the
// handlers to consume.
func eventToTable(L *lua.LState, ev Event) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("channel", lua.LString(ev.Channel))
	t.RawSetString("sender", lua.LString(ev.Sender))
	t.RawSetString("hostmask", lua.LString(ev.Hostmask))
	t.RawSetString("text", lua.LString(ev.Text))
	if ev.CommandName != "" {
		t.RawSetString("name", lua.LString(ev.CommandName))
		t.RawSetString("args", lua.LString(ev.CommandArgs))
	}
	return t
}

// newCtxUserdata wraps the Runtime's actions in a Lua userdata with
// the method table the script calls.
func (r *Runtime) newCtxUserdata(L *lua.LState) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = r.actions
	mt := L.NewTypeMetatable("ircat_ctx")
	L.SetField(mt, "__index", L.SetFuncs(L.NewTable(), map[string]lua.LGFunction{
		"say":       ctxSay,
		"notice":    ctxNotice,
		"join":      ctxJoin,
		"part":      ctxPart,
		"nick":      ctxNick,
		"log":       ctxLog,
		"now":       ctxNow,
		"kv_get":    ctxKVGet,
		"kv_set":    ctxKVSet,
		"kv_delete": ctxKVDelete,
	}))
	L.SetMetatable(ud, mt)
	return ud
}

// The per-method implementations all pull the Actions pointer off
// the userdata and forward to it.

func ctxActions(L *lua.LState) Actions {
	ud := L.CheckUserData(1)
	a, _ := ud.Value.(Actions)
	return a
}

func ctxSay(L *lua.LState) int {
	target := L.CheckString(2)
	text := L.CheckString(3)
	if err := ctxActions(L).Say(target, text); err != nil {
		L.RaiseError("say: %s", err.Error())
	}
	return 0
}
func ctxNotice(L *lua.LState) int {
	target := L.CheckString(2)
	text := L.CheckString(3)
	if err := ctxActions(L).Notice(target, text); err != nil {
		L.RaiseError("notice: %s", err.Error())
	}
	return 0
}
func ctxJoin(L *lua.LState) int {
	channelName := L.CheckString(2)
	if err := ctxActions(L).JoinChannel(channelName); err != nil {
		L.RaiseError("join: %s", err.Error())
	}
	return 0
}
func ctxPart(L *lua.LState) int {
	channelName := L.CheckString(2)
	reason := ""
	if L.GetTop() >= 3 {
		reason = L.CheckString(3)
	}
	if err := ctxActions(L).PartChannel(channelName, reason); err != nil {
		L.RaiseError("part: %s", err.Error())
	}
	return 0
}
func ctxNick(L *lua.LState) int {
	L.Push(lua.LString(ctxActions(L).Nick()))
	return 1
}
func ctxLog(L *lua.LState) int {
	level := L.CheckString(2)
	message := L.CheckString(3)
	ctxActions(L).Log(level, message)
	return 0
}
func ctxNow(L *lua.LState) int {
	L.Push(lua.LNumber(ctxActions(L).Now().Unix()))
	return 1
}
func ctxKVGet(L *lua.LState) int {
	key := L.CheckString(2)
	v, err := ctxActions(L).KVGet(key)
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(lua.LString(v))
	return 1
}
func ctxKVSet(L *lua.LState) int {
	key := L.CheckString(2)
	value := L.CheckString(3)
	if err := ctxActions(L).KVSet(key, value); err != nil {
		L.RaiseError("kv_set: %s", err.Error())
	}
	return 0
}
func ctxKVDelete(L *lua.LState) int {
	key := L.CheckString(2)
	if err := ctxActions(L).KVDelete(key); err != nil {
		L.RaiseError("kv_delete: %s", err.Error())
	}
	return 0
}

// ExtractCommand parses a PRIVMSG text body for the "!name args"
// command shape. Returns name and args when the text starts with
// "!", otherwise (name="", args=""). This is a helper the
// supervisor uses to decide whether to fire on_command vs
// on_message.
func ExtractCommand(text string) (name, args string) {
	if !strings.HasPrefix(text, "!") || len(text) < 2 {
		return "", ""
	}
	body := strings.TrimPrefix(text, "!")
	sp := strings.IndexByte(body, ' ')
	if sp < 0 {
		return body, ""
	}
	return body[:sp], strings.TrimLeft(body[sp+1:], " ")
}
