package federation

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// fakeHost is a minimal Host implementation for the unit tests.
// It wraps a state.World and captures every DeliverLocal call so
// tests can assert on the deliveries.
type fakeHost struct {
	name  string
	world *state.World

	mu        sync.Mutex
	delivered []*protocol.Message
	squits    []string
	dropped   []string
	subs      map[string]map[string]bool
}

func newFakeHost(name string) *fakeHost {
	return &fakeHost{
		name:  name,
		world: state.NewWorld(),
	}
}

func (h *fakeHost) LocalServerName() string  { return h.name }
func (h *fakeHost) WorldState() *state.World { return h.world }
func (h *fakeHost) DeliverLocal(msg *protocol.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delivered = append(h.delivered, msg)
}
func (h *fakeHost) HandleSquit(peerName, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.squits = append(h.squits, peerName)
}
func (h *fakeHost) DropLocalUser(nick, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropped = append(h.dropped, nick)
}
func (h *fakeHost) SubscribePeerToChannel(peerName, channelName string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = make(map[string]map[string]bool)
	}
	if h.subs[peerName] == nil {
		h.subs[peerName] = make(map[string]bool)
	}
	h.subs[peerName][channelName] = true
}
func (h *fakeHost) deliveriesFor(command string) []*protocol.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*protocol.Message
	for _, m := range h.delivered {
		if m.Command == command {
			out = append(out, m)
		}
	}
	return out
}

// linkPair wires two Links together in-memory: every message sent
// by one lands on the other's dispatch path via a channel. Writer
// functions are no-ops because the message never touches a socket.
type linkPair struct {
	a, b   *Link
	aReads chan *protocol.Message
	bReads chan *protocol.Message
	aHost  *fakeHost
	bHost  *fakeHost
	cancel context.CancelFunc
	doneA  chan error
	doneB  chan error
}

func newLinkPair(t *testing.T) *linkPair {
	t.Helper()
	aHost := newFakeHost("node-a")
	bHost := newFakeHost("node-b")
	cfgA := LinkConfig{
		PeerName:    "node-b",
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Description: "node A -> B",
		Version:     "ircat-test",
	}
	cfgB := LinkConfig{
		PeerName:    "node-a",
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Description: "node B -> A",
		Version:     "ircat-test",
	}
	logger := slog.Default()
	a := New(aHost, cfgA, logger)
	b := New(bHost, cfgB, logger)

	aReads := make(chan *protocol.Message, 256)
	bReads := make(chan *protocol.Message, 256)

	// Writer for A shoves messages onto B's read channel, and
	// vice versa. The writers are called from within each Link's
	// own writer goroutine, so blocking there would deadlock —
	// but the buffered channels make it non-blocking for test
	// workloads.
	writeFromA := func(msg *protocol.Message) error {
		bReads <- msg
		return nil
	}
	writeFromB := func(msg *protocol.Message) error {
		aReads <- msg
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	lp := &linkPair{
		a: a, b: b,
		aReads: aReads, bReads: bReads,
		aHost: aHost, bHost: bHost,
		cancel: cancel,
		doneA:  make(chan error, 1),
		doneB:  make(chan error, 1),
	}
	go func() { lp.doneA <- a.Run(ctx, aReads, writeFromA) }()
	go func() { lp.doneB <- b.Run(ctx, bReads, writeFromB) }()
	return lp
}

func (lp *linkPair) close(t *testing.T) {
	t.Helper()
	lp.cancel()
	_ = lp.a.Close()
	_ = lp.b.Close()
	select {
	case <-lp.doneA:
	case <-time.After(2 * time.Second):
		t.Error("link A did not exit")
	}
	select {
	case <-lp.doneB:
	case <-time.After(2 * time.Second):
		t.Error("link B did not exit")
	}
}

func waitFor(t *testing.T, deadline time.Duration, cond func() bool) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestLink_Handshake(t *testing.T) {
	lp := newLinkPair(t)
	defer lp.close(t)

	// Both sides open the handshake simultaneously — each sends
	// PASS + SERVER, and the dispatch path on the other side
	// transitions through LinkBursting into LinkActive.
	if err := lp.a.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if err := lp.b.OpenOutbound(); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 2*time.Second, func() bool {
		return lp.a.State() == LinkActive && lp.b.State() == LinkActive
	}) {
		t.Fatalf("links did not become active: A=%s B=%s", lp.a.State(), lp.b.State())
	}
	if lp.a.PeerName() != "node-b" || lp.b.PeerName() != "node-a" {
		t.Errorf("peer names = %q / %q", lp.a.PeerName(), lp.b.PeerName())
	}
}

func TestLink_UserBurst(t *testing.T) {
	lp := newLinkPair(t)
	defer lp.close(t)

	// Pre-populate node A with a local user so the burst carries
	// it over to node B.
	aHost := lp.aHost
	if _, err := aHost.world.AddUser(&state.User{
		Nick:       "alice",
		User:       "alice",
		Host:       "host",
		Realname:   "Alice",
		Registered: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := lp.a.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if err := lp.b.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return lp.a.State() == LinkActive && lp.b.State() == LinkActive
	}) {
		t.Fatalf("handshake stalled")
	}

	// After burst, node B should know about alice as a remote
	// user with HomeServer=node-a.
	if !waitFor(t, 2*time.Second, func() bool {
		u := lp.bHost.world.FindByNick("alice")
		return u != nil && u.IsRemote() && u.HomeServer == "node-a"
	}) {
		u := lp.bHost.world.FindByNick("alice")
		t.Fatalf("alice not found on node B: %+v", u)
	}
}

func TestLink_PrivmsgPropagation(t *testing.T) {
	lp := newLinkPair(t)
	defer lp.close(t)

	if err := lp.a.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if err := lp.b.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return lp.a.State() == LinkActive
	}) {
		t.Fatal("handshake stalled")
	}

	// Send a PRIVMSG from A to B across the link.
	lp.a.Send(&protocol.Message{
		Prefix:  "alice!alice@host",
		Command: "PRIVMSG",
		Params:  []string{"#fed", "hello from A"},
	})

	if !waitFor(t, 2*time.Second, func() bool {
		return len(lp.bHost.deliveriesFor("PRIVMSG")) == 1
	}) {
		t.Fatalf("B did not receive PRIVMSG, have %d deliveries", len(lp.bHost.delivered))
	}
	got := lp.bHost.deliveriesFor("PRIVMSG")[0]
	if got.Params[0] != "#fed" || got.Params[1] != "hello from A" {
		t.Errorf("delivered = %+v", got)
	}
}

func TestSenderFromPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alice!alice@host", "alice"},
		{"alice", "alice"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := senderFromPrefix(tc.in); got != tc.want {
			t.Errorf("senderFromPrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

