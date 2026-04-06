package bots

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeActions captures every IRC side effect for the tests.
type fakeActions struct {
	mu       sync.Mutex
	says     []sayCall
	joins    []string
	parts    []string
	logs     []string
	kv       map[string]string
	nickName string
	now      time.Time
}

type sayCall struct {
	Target, Text string
}

func newFakeActions(nick string) *fakeActions {
	return &fakeActions{
		nickName: nick,
		kv:       make(map[string]string),
		now:      time.Unix(1_700_000_000, 0),
	}
}

func (f *fakeActions) Say(target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.says = append(f.says, sayCall{Target: target, Text: text})
	return nil
}
func (f *fakeActions) Notice(target, text string) error {
	return f.Say(target, text)
}
func (f *fakeActions) JoinChannel(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joins = append(f.joins, name)
	return nil
}
func (f *fakeActions) PartChannel(name, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parts = append(f.parts, name)
	return nil
}
func (f *fakeActions) Nick() string { return f.nickName }
func (f *fakeActions) KVGet(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.kv[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (f *fakeActions) KVSet(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kv[key] = value
	return nil
}
func (f *fakeActions) KVDelete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kv, key)
	return nil
}
func (f *fakeActions) Log(level, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, level+": "+message)
}
func (f *fakeActions) Now() time.Time { return f.now }

func TestRuntime_OnCommandPingPong(t *testing.T) {
	act := newFakeActions("botty")
	source := `
function on_command(ctx, event)
  if event.name == "ping" then
    ctx:say(event.channel, "pong")
  end
end
`
	rt, err := NewRuntime(source, act, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	err = rt.DispatchCommand(context.Background(), Event{
		Channel:     "#test",
		Sender:      "alice",
		Text:        "!ping",
		CommandName: "ping",
	})
	if err != nil {
		t.Fatal(err)
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	if len(act.says) != 1 {
		t.Fatalf("says = %d", len(act.says))
	}
	if act.says[0].Target != "#test" || act.says[0].Text != "pong" {
		t.Errorf("say = %+v", act.says[0])
	}
}

func TestRuntime_InitJoinsChannel(t *testing.T) {
	act := newFakeActions("botty")
	source := `
function init(ctx)
  ctx:join("#welcome")
end
`
	rt, err := NewRuntime(source, act, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	if err := rt.DispatchInit(context.Background()); err != nil {
		t.Fatal(err)
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	if len(act.joins) != 1 || act.joins[0] != "#welcome" {
		t.Errorf("joins = %v", act.joins)
	}
}

func TestRuntime_KVRoundTrip(t *testing.T) {
	act := newFakeActions("botty")
	source := `
function init(ctx)
  ctx:kv_set("counter", "1")
  local v = ctx:kv_get("counter")
  ctx:log("info", "counter=" .. v)
end
`
	rt, err := NewRuntime(source, act, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if err := rt.DispatchInit(context.Background()); err != nil {
		t.Fatal(err)
	}
	act.mu.Lock()
	defer act.mu.Unlock()
	if act.kv["counter"] != "1" {
		t.Errorf("kv = %v", act.kv)
	}
	if len(act.logs) == 0 || !strings.Contains(act.logs[0], "counter=1") {
		t.Errorf("log = %v", act.logs)
	}
}

func TestRuntime_SandboxBlocksOS(t *testing.T) {
	act := newFakeActions("botty")
	source := `
function init(ctx)
  os.execute("ls")
end
`
	_, err := NewRuntime(source, act, Budget{})
	// The compile step runs the top-level file body in modern
	// gopher-lua only when DoString is called. Our NewRuntime does
	// that, and os is nil, so the DoString fails with "attempt to
	// index a nil value". Either outcome (compile error or runtime
	// error at init dispatch) is acceptable — both prove the
	// sandbox.
	if err == nil {
		rt, _ := NewRuntime(source, act, Budget{})
		defer rt.Close()
		initErr := rt.DispatchInit(context.Background())
		if initErr == nil {
			t.Error("os.execute did not fail under the sandbox")
		}
	}
}

func TestRuntime_MissingHandlerIsNoop(t *testing.T) {
	act := newFakeActions("botty")
	source := `-- no handlers at all`
	rt, err := NewRuntime(source, act, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// DispatchInit on a script with no init() handler should be
	// silent, not an error.
	if err := rt.DispatchInit(context.Background()); err != nil {
		t.Errorf("init: %v", err)
	}
	if err := rt.DispatchMessage(context.Background(), Event{}); err != nil {
		t.Errorf("on_message: %v", err)
	}
}

func TestRuntime_CompileError(t *testing.T) {
	act := newFakeActions("botty")
	_, err := NewRuntime("this is not valid lua {{{", act, Budget{})
	if err == nil {
		t.Fatal("expected compile error")
	}
}

func TestIsChannelTarget(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"#chan", true},
		{"&local", true},
		{"alice", false},
		{"", false},
		{"!nope", false},
	}
	for _, tc := range cases {
		if got := isChannelTarget(tc.in); got != tc.want {
			t.Errorf("isChannelTarget(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	if parseLogLevel("debug").Level() != -4 { // slog.LevelDebug
		t.Errorf("debug")
	}
	if parseLogLevel("warn").String() != "WARN" {
		t.Errorf("warn")
	}
	if parseLogLevel("error").String() != "ERROR" {
		t.Errorf("error")
	}
	if parseLogLevel("nonsense").String() != "INFO" {
		t.Errorf("default")
	}
}

func TestExtractCommand(t *testing.T) {
	cases := []struct {
		in      string
		wantN   string
		wantArg string
	}{
		{"!ping", "ping", ""},
		{"!ping args here", "ping", "args here"},
		{"no bang", "", ""},
		{"!", "", ""},
	}
	for _, tc := range cases {
		n, a := ExtractCommand(tc.in)
		if n != tc.wantN || a != tc.wantArg {
			t.Errorf("ExtractCommand(%q) = (%q, %q), want (%q, %q)", tc.in, n, a, tc.wantN, tc.wantArg)
		}
	}
}
