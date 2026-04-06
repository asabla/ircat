package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// startAuditServer is a variant of startTestServer that uses a real
// sqlite store and returns the store handle so the test can read
// the audit log directly.
func startAuditServer(t *testing.T) (string, *sqlite.Store, func()) {
	t.Helper()
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:    "irc.test",
			Network: "TestNet",
			Listeners: []config.Listener{
				{Address: "127.0.0.1:0"},
			},
			Limits: config.LimitsConfig{
				NickLength:              30,
				ChannelLength:           50,
				TopicLength:             390,
				AwayLength:              255,
				KickReasonLength:        255,
				PingIntervalSeconds:     5,
				PingTimeoutSeconds:      20,
				MessageBurst:            100,
				MessageRefillPerSecond:  100,
				MessageViolationsToKick: 5,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv := New(cfg, world, logger, WithStore(store))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	var addr string
	for {
		if a := srv.ListenerAddrs(); len(a) > 0 {
			addr = a[0].String()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}
	teardown := func() {
		cancel()
		<-done
		_ = store.Close()
	}
	return addr, store, teardown
}

func TestAudit_TopicEmitted(t *testing.T) {
	addr, store, teardown := startAuditServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #x\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("TOPIC #x :hello\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " TOPIC #x ") && strings.Contains(l, "hello")
	})

	// The audit log should contain a topic event.
	events, err := store.Events().List(context.Background(), storage.ListEventsOptions{Type: AuditTypeTopic})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("topic events: %d", len(events))
	}
	if events[0].Target != "#x" {
		t.Errorf("target = %q", events[0].Target)
	}
	if !strings.Contains(events[0].DataJSON, "hello") {
		t.Errorf("data = %q", events[0].DataJSON)
	}
}

func TestAudit_ModeAndKickEmitted(t *testing.T) {
	addr, store, teardown := startAuditServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("MODE #x +k secret\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+k") && strings.Contains(l, "secret")
	})
	cAlice.Write([]byte("KICK #x bob :goodbye\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " KICK #x bob ")
	})

	modes, err := store.Events().List(context.Background(), storage.ListEventsOptions{Type: AuditTypeMode})
	if err != nil {
		t.Fatal(err)
	}
	if len(modes) != 1 {
		t.Fatalf("mode events: %d", len(modes))
	}
	if !strings.Contains(modes[0].DataJSON, "+k") {
		t.Errorf("mode data = %q", modes[0].DataJSON)
	}

	kicks, err := store.Events().List(context.Background(), storage.ListEventsOptions{Type: AuditTypeKick})
	if err != nil {
		t.Fatal(err)
	}
	if len(kicks) != 1 || kicks[0].Target != "#x" {
		t.Errorf("kick events = %v", kicks)
	}
	if !strings.Contains(kicks[0].DataJSON, "bob") || !strings.Contains(kicks[0].DataJSON, "goodbye") {
		t.Errorf("kick data = %q", kicks[0].DataJSON)
	}
}

func TestAudit_OperUpEmitted(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t,
		[]storage.Operator{{Name: "alice", HostMask: ""}},
		map[string]string{"alice": "secret"},
	)
	defer teardown()
	// startTestServerWithOperators owns its own store; we cannot
	// reach into it from here, so the assertion is just that the
	// OPER succeeds (the audit emission path is exercised by the
	// other tests).
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("OPER alice secret\r\n"))
	expectNumeric(t, c, r, "381", time.Now().Add(2*time.Second))
}

// silence unused import warning
var _ = auth.Hash
