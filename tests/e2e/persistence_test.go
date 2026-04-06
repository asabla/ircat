package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
	"github.com/asabla/ircat/tests/e2e/ircclient"
)

// startServerWith spawns the binary against a config that points
// at the supplied sqlite db path. The motd file is omitted so the
// welcome burst ends with 422.
func startServerWith(t *testing.T, dir, dbPath string) (string, func()) {
	t.Helper()
	port := pickFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	configPath := filepath.Join(dir, "ircat.yaml")
	cfg := fmt.Sprintf(`version: 1
server:
  name: irc.test
  network: TestNet
  description: e2e persistence
  listeners:
    - address: "%s"
      tls: false
  limits:
    nick_length: 30
    channel_length: 50
    topic_length: 390
    away_length: 255
    kick_reason_length: 255
    ping_interval_seconds: 5
    ping_timeout_seconds: 20
storage:
  driver: sqlite
  sqlite:
    path: %s
dashboard:
  enabled: false
  address: "127.0.0.1:0"
logging:
  level: info
  format: text
`, addr, dbPath)
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath, "--config", configPath)
	cmd.Stdout = &testWriter{t: t, prefix: "ircat-stdout"}
	cmd.Stderr = &testWriter{t: t, prefix: "ircat-stderr"}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatalf("ircat did not bind %s", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	teardown := func() {
		cancel()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("ircat did not stop within 5s")
		}
	}
	return addr, teardown
}

func TestE2E_PersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Pre-create an operator in the database before the first boot.
	// This bootstraps the OPER credentials so the e2e test does not
	// need a separate dashboard or admin API call.
	{
		store, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		hash, err := auth.Hash(auth.AlgorithmArgon2id, "secret", auth.Argon2idParams{})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Operators().Create(context.Background(), &storage.Operator{
			Name:         "alice",
			HostMask:     "",
			PasswordHash: hash,
			Flags:        []string{"all"},
		}); err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
	}

	// Phase 1: spawn the binary, OPER, set channel state, kill.
	addr1, stop1 := startServerWith(t, dir, dbPath)
	c1, err := ircclient.Dial(addr1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c1.Register("alice", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c1.Send("OPER alice secret"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1.ExpectNumeric("381", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("oper: %v", err)
	}
	if err := c1.Send("JOIN #persist"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1.ExpectNumeric("366", time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c1.Send("TOPIC #persist :survives restart"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, " TOPIC #persist ") && strings.Contains(line, "survives restart")
	}); err != nil {
		t.Fatal(err)
	}
	if err := c1.Send("MODE #persist +k pwd"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "+k") && strings.Contains(line, "pwd")
	}); err != nil {
		t.Fatal(err)
	}
	c1.Close()
	stop1()

	// Phase 2: spawn a fresh process against the same DB, verify
	// the operator and the channel state survived.
	addr2, stop2 := startServerWith(t, dir, dbPath)
	defer stop2()

	c2, err := ircclient.Dial(addr2, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Register("alice", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Operator credentials persisted: OPER should succeed.
	if err := c2.Send("OPER alice secret"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c2.ExpectNumeric("381", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("oper after restart: %v", err)
	}

	// MODE query should report the persisted +k.
	if err := c2.Send("MODE #persist"); err != nil {
		t.Fatal(err)
	}
	line, _, err := c2.ExpectNumeric("324", time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "k") || !strings.Contains(line, "pwd") {
		t.Errorf("324 missing persisted state: %q", line)
	}

	// JOIN with the persisted key should succeed and the topic
	// should come back via 332.
	if err := c2.Send("JOIN #persist pwd"); err != nil {
		t.Fatal(err)
	}
	topicLine, _, err := c2.ExpectNumeric("332", time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(topicLine, "survives restart") {
		t.Errorf("332 missing persisted topic: %q", topicLine)
	}
}
