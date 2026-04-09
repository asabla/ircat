package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// startTestServerWithStore is a variant of startTestServer that
// uses a real sqlite Store at the given path. Used by the
// persistence tests.
func startTestServerWithStore(t *testing.T, dbPath string) (string, func()) {
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
	store, err := sqlite.Open(dbPath)
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
	return addr, teardown
}

func TestPersistence_TopicAndModeAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Phase 1: bring up the server, create #persist, set a topic and
	// a non-default mode (+k secret), then shut down.
	addr, teardown := startTestServerWithStore(t, dbPath)
	c, r := register(t, addr, "alice")
	c.Write([]byte("JOIN #persist\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("TOPIC #persist :persisted topic\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " TOPIC #persist ") && strings.Contains(l, "persisted topic")
	})
	c.Write([]byte("MODE #persist +k secret\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #persist ") && strings.Contains(l, "+k") && strings.Contains(l, "secret")
	})
	c.Close()
	teardown()

	// Phase 2: bring up a fresh server with the same database file.
	// The channel should be restored from disk.
	addr2, teardown2 := startTestServerWithStore(t, dbPath)
	defer teardown2()

	c2, r2 := register(t, addr2, "bob")
	defer c2.Close()

	// MODE query should return the persisted +ntk + secret.
	c2.Write([]byte("MODE #persist\r\n"))
	line, _ := readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "k") {
		t.Errorf("324 missing +k after restart: %q", line)
	}
	if !strings.Contains(line, "secret") {
		t.Errorf("324 missing key after restart: %q", line)
	}

	// JOIN without key should fail with 475 (key restored).
	c2.Write([]byte("JOIN #persist\r\n"))
	expectNumeric(t, c2, r2, "475", time.Now().Add(2*time.Second))

	// JOIN with key should succeed and the topic should come back.
	c2.Write([]byte("JOIN #persist secret\r\n"))
	topicLine, _ := readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "332"
	})
	if !strings.Contains(topicLine, "persisted topic") {
		t.Errorf("332 missing persisted topic: %q", topicLine)
	}
}

func TestPersistence_ClearedTopicSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Phase 1: set a topic, then clear it.
	addr, teardown := startTestServerWithStore(t, dbPath)
	c, r := register(t, addr, "alice")
	c.Write([]byte("JOIN #t\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("TOPIC #t :temporary\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " TOPIC #t ") && strings.Contains(l, "temporary")
	})
	// Clear the topic with an empty trailing.
	c.Write([]byte("TOPIC #t :\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		// The cleared form ends with " TOPIC #t :".
		return strings.HasSuffix(l, " TOPIC #t :")
	})
	c.Close()
	teardown()

	// Phase 2: restart and verify TOPIC reads back as RPL_NOTOPIC.
	addr2, teardown2 := startTestServerWithStore(t, dbPath)
	defer teardown2()
	c2, r2 := register(t, addr2, "bob")
	defer c2.Close()
	c2.Write([]byte("TOPIC #t\r\n"))
	expectNumeric(t, c2, r2, "331", time.Now().Add(2*time.Second))
}

func TestPersistence_BansSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	addr, teardown := startTestServerWithStore(t, dbPath)
	c, r := register(t, addr, "alice")
	c.Write([]byte("JOIN #b\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("MODE #b +b spammer!*@*\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+b") && strings.Contains(l, "spammer!*@*")
	})
	c.Close()
	teardown()

	addr2, teardown2 := startTestServerWithStore(t, dbPath)
	defer teardown2()
	c2, r2 := register(t, addr2, "alice")
	defer c2.Close()
	c2.Write([]byte("JOIN #b\r\n"))
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c2.Write([]byte("MODE #b +b\r\n"))
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "367" && strings.Contains(l, "spammer!*@*")
	})
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "368"
	})
}

func TestPersistence_ExceptionsAndInvexesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Phase 1: set +e and +I masks, then shut down.
	addr, teardown := startTestServerWithStore(t, dbPath)
	c, r := register(t, addr, "alice")
	c.Write([]byte("JOIN #ei\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("MODE #ei +e *!*@safe.example\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+e") && strings.Contains(l, "safe.example")
	})
	c.Write([]byte("MODE #ei +I *!*@vip.example\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+I") && strings.Contains(l, "vip.example")
	})
	c.Close()
	teardown()

	// Phase 2: restart and query both lists.
	addr2, teardown2 := startTestServerWithStore(t, dbPath)
	defer teardown2()
	c2, r2 := register(t, addr2, "alice")
	defer c2.Close()
	c2.Write([]byte("JOIN #ei\r\n"))
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c2.Write([]byte("MODE #ei +e\r\n"))
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "348" && strings.Contains(l, "safe.example")
	})
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "349"
	})

	c2.Write([]byte("MODE #ei +I\r\n"))
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "346" && strings.Contains(l, "vip.example")
	})
	readUntil(t, c2, r2, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "347"
	})
}
