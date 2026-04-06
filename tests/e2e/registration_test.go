// Package e2e holds black-box tests that build the ircat binary,
// run it as a subprocess against a temp config, and drive it over
// real TCP. They are slower than the per-package unit tests; the
// trade-off is that they exercise the binary the same way an
// operator would, including config loading, listener bind, signal
// handling, and shutdown.
//
// The build of cmd/ircat happens once in TestMain and is reused by
// every test, so the per-test overhead is just "spawn + dial".
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

	"github.com/asabla/ircat/tests/e2e/ircclient"
)

var binaryPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ircat-e2e-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	binaryPath = filepath.Join(tmp, "ircat")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binaryPath, "../../cmd/ircat")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "go build cmd/ircat:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// startServer launches the binary against a fresh config bound to a
// random localhost port and returns the addr plus a teardown that
// SIGTERMs the process and waits for it.
func startServer(t *testing.T) (addr string, teardown func()) {
	t.Helper()
	port := pickFreePort(t)
	addr = fmt.Sprintf("127.0.0.1:%d", port)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ircat.yaml")
	cfg := fmt.Sprintf(`version: 1
server:
  name: irc.test
  network: TestNet
  description: e2e test
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
    path: %s/ircat.db
dashboard:
  enabled: false
  address: "127.0.0.1:0"
logging:
  level: debug
  format: text
`, addr, dir)
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath, "--config", configPath)
	cmd.Stdout = &testWriter{t: t, prefix: "ircat-stdout"}
	cmd.Stderr = &testWriter{t: t, prefix: "ircat-stderr"}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start ircat: %v", err)
	}

	// Wait until the process accepts connections.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatalf("ircat did not bind %s in time", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	teardown = func() {
		// SIGTERM via cancel triggers signal.NotifyContext in main.
		cancel()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("ircat did not stop within 5s of SIGTERM")
		}
	}
	return addr, teardown
}

// pickFreePort asks the kernel for a free port by binding to :0 and
// reading back the assigned port. The race window between Close and
// the server's bind is short and unproblematic in practice.
func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// testWriter forwards subprocess output into the test log so failed
// e2e runs include the server's slog stream.
type testWriter struct {
	t      *testing.T
	prefix string
}

func (w *testWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		w.t.Logf("[%s] %s", w.prefix, line)
	}
	return len(p), nil
}

func TestE2E_RegistrationWelcomeBurst(t *testing.T) {
	addr, teardown := startServer(t)
	defer teardown()

	c, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.Send("NICK alice"); err != nil {
		t.Fatal(err)
	}
	if err := c.Send("USER alice 0 * :Alice Example"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for _, want := range []string{"001", "002", "003", "004", "005"} {
		line, _, err := c.ExpectNumeric(want, deadline)
		if err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		if !strings.Contains(line, "alice") {
			t.Errorf("numeric %s missing nick: %q", want, line)
		}
	}
	// MOTD: 422 because no motd_file is configured.
	if _, _, err := c.ExpectNumeric("422", deadline); err != nil {
		t.Fatalf("waiting for 422: %v", err)
	}
}

func TestE2E_NickInUse(t *testing.T) {
	addr, teardown := startServer(t)
	defer teardown()

	c1, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c1.Send("NICK alice")
	c1.Send("USER alice 0 * :Alice")
	if _, _, err := c1.ExpectNumeric("001", time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	c2, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.Send("NICK Alice") // collides under rfc1459 fold
	c2.Send("USER alice 0 * :Alice2")
	line, trace, err := c2.ExpectNumeric("433", time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("expected 433, got: %v\n trace: %v", err, trace)
	}
	if !strings.Contains(line, "Alice") {
		t.Errorf("433 should mention requested nick: %q", line)
	}
}

func TestE2E_PingPongAndQuit(t *testing.T) {
	addr, teardown := startServer(t)
	defer teardown()

	c, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Send("NICK alice")
	c.Send("USER alice 0 * :Alice")
	if _, _, err := c.ExpectNumeric("001", time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	c.Send("PING :token-xyz")
	deadline := time.Now().Add(2 * time.Second)
	if _, _, err := c.Expect(deadline, func(line string) bool {
		return strings.Contains(line, "PONG") && strings.Contains(line, "token-xyz")
	}); err != nil {
		t.Fatalf("waiting for PONG: %v", err)
	}

	c.Send("QUIT :bye e2e")
	// Read until ERROR or EOF.
	deadline = time.Now().Add(2 * time.Second)
	sawError := false
	for {
		line, err := c.ReadLine(deadline)
		if err != nil {
			if !sawError {
				t.Errorf("connection closed without ERROR (last err: %v)", err)
			}
			return
		}
		if strings.HasPrefix(line, "ERROR ") {
			sawError = true
		}
	}
}
