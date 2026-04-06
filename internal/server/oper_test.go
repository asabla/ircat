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

// startTestServerWithOperators is like startTestServer but wires
// in a real SQLite Store and pre-creates the supplied operators.
// Each operator's password is hashed via internal/auth.
func startTestServerWithOperators(t *testing.T, ops []storage.Operator, plaintextPasswords map[string]string) (string, func()) {
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
	for i := range ops {
		op := ops[i]
		if pw, ok := plaintextPasswords[op.Name]; ok {
			hash, err := auth.Hash(auth.AlgorithmArgon2id, pw, auth.Argon2idParams{})
			if err != nil {
				t.Fatal(err)
			}
			op.PasswordHash = hash
		}
		if err := store.Operators().Create(context.Background(), &op); err != nil {
			t.Fatal(err)
		}
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

func TestOper_Success(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t,
		[]storage.Operator{{Name: "alice", HostMask: ""}}, // empty mask = match anything
		map[string]string{"alice": "secret"},
	)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("OPER alice secret\r\n"))
	expectNumeric(t, c, r, "381", time.Now().Add(2*time.Second))

	// Should also see a MODE +o on alice.
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, " MODE alice ") && strings.Contains(line, "o")
	})
}

func TestOper_BadPassword(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t,
		[]storage.Operator{{Name: "alice"}},
		map[string]string{"alice": "secret"},
	)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("OPER alice wrong-password\r\n"))
	expectNumeric(t, c, r, "464", time.Now().Add(2*time.Second))
}

func TestOper_UnknownOperator(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t, nil, nil)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("OPER ghost whatever\r\n"))
	// Returns 491 to avoid leaking which operators exist.
	expectNumeric(t, c, r, "491", time.Now().Add(2*time.Second))
}

func TestOper_HostMaskMismatch(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t,
		[]storage.Operator{{Name: "alice", HostMask: "*@10.0.0.*"}},
		map[string]string{"alice": "secret"},
	)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	// Connection is from 127.0.0.1, doesn't match 10.0.0.* mask.
	c.Write([]byte("OPER alice secret\r\n"))
	expectNumeric(t, c, r, "491", time.Now().Add(2*time.Second))
}

func TestOper_NeedMoreParams(t *testing.T) {
	addr, teardown := startTestServerWithOperators(t, nil, nil)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("OPER\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}
