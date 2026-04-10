package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// startTestServerWithStoreAndNickServ creates a test server with an in-memory
// SQLite store so NickServ can start and accounts can be persisted.
func startTestServerWithStoreAndNickServ(t *testing.T) (addr string, srv *Server, teardown func()) {
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
				PingIntervalSeconds:     1,
				PingTimeoutSeconds:      4,
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
	world := state.NewWorld()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv = New(cfg, world, logger, WithStore(store))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if addrs := srv.ListenerAddrs(); len(addrs) > 0 {
			addr = addrs[0].String()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not bind in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	teardown = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server failed to shut down")
		}
		_ = store.Close()
	}
	return addr, srv, teardown
}

func TestNickServ_AppearsInServlist(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("SERVLIST\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " 234 ") || strings.Contains(l, " 235 ")
	})
	if strings.Contains(line, " 234 ") && strings.Contains(line, "NickServ") {
		// Good — NickServ appears in SERVLIST.
	} else if strings.Contains(line, " 235 ") {
		t.Errorf("SERVLIST returned no services (expected NickServ)")
	}
}

func TestNickServ_RegisterAndIdentify(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	// REGISTER via PRIVMSG to NickServ.
	c.Write([]byte("PRIVMSG NickServ :REGISTER mypassword alice@test.com\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "registered successfully")
	})

	// IDENTIFY via PRIVMSG.
	c.Write([]byte("PRIVMSG NickServ :IDENTIFY mypassword\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "identified as alice")
	})
}

func TestNickServ_IdentifyBadPassword(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("PRIVMSG NickServ :REGISTER goodpass\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "registered")
	})

	c.Write([]byte("PRIVMSG NickServ :IDENTIFY wrongpass\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "Invalid credentials")
	})
}

func TestNickServ_SqueryAlsoWorks(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("SQUERY NickServ :REGISTER sqpass\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "registered")
	})
}
