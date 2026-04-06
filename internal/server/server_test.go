package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// startTestServer brings up a Server bound to a kernel-assigned
// localhost port and returns the address plus a teardown function.
// The server is asked to bind ":0"; once it has done so, the test
// reads the actual address back via [Server.ListenerAddrs] so there
// is no port-stealing race window.
func startTestServer(t *testing.T) (addr string, teardown func()) {
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

	srv := New(cfg, world, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()

	// Spin until ListenerAddrs reports the kernel-assigned port.
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
	}
	return addr, teardown
}

// dialClient opens a connection to addr and returns it along with a
// reader pre-wrapped in bufio so the test can call ReadLine().
func dialClient(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c, bufio.NewReader(c)
}

// expectNumeric reads lines until it sees one whose command field is
// the requested numeric, then returns it. Uses the underlying conn
// read deadline so there is no goroutine leak when the deadline
// fires (which is what the older bufio-only helper used to do).
func expectNumeric(t *testing.T, c net.Conn, r *bufio.Reader, code string, deadline time.Time) string {
	t.Helper()
	for {
		_ = c.SetReadDeadline(deadline)
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read while waiting for %s: %v", code, err)
		}
		line = strings.TrimRight(line, "\r\n")
		if extractNumeric(line) == code {
			return line
		}
	}
}

func extractNumeric(line string) string {
	// Lines look like ":server NNN target ..." — split on spaces and
	// look for the second field.
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 {
		return ""
	}
	if !strings.HasPrefix(parts[0], ":") {
		return ""
	}
	if len(parts[1]) != 3 {
		return ""
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return parts[1]
}

func TestRegistration_FullWelcomeBurst(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()

	if _, err := c.Write([]byte("NICK alice\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("USER alice 0 * :Alice Example\r\n")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	want := []string{"001", "002", "003", "004", "005", "422"} // 422 = no MOTD configured in test
	for _, code := range want {
		line := expectNumeric(t, c, r, code, deadline)
		if !strings.Contains(line, "alice") && code != "422" {
			t.Errorf("numeric %s missing nick: %q", code, line)
		}
	}
}

func TestRegistration_NickInUse(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c1, r1 := dialClient(t, addr)
	defer c1.Close()
	c1.Write([]byte("NICK alice\r\n"))
	c1.Write([]byte("USER alice 0 * :Alice\r\n"))
	expectNumeric(t, c1, r1, "001", time.Now().Add(2*time.Second))

	c2, r2 := dialClient(t, addr)
	defer c2.Close()
	c2.Write([]byte("NICK ALICE\r\n")) // colliding under rfc1459 fold
	c2.Write([]byte("USER alice 0 * :Alice2\r\n"))
	line := expectNumeric(t, c2, r2, "433", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "ALICE") {
		t.Errorf("433 should mention requested nick: %q", line)
	}
}

func TestPingPong(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\n"))
	c.Write([]byte("USER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("PING :token123\r\n"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		_ = c.SetReadDeadline(deadline)
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(line, "PONG") && strings.Contains(line, "token123") {
			return
		}
	}
}

func TestQuit_GracefulClose(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\n"))
	c.Write([]byte("USER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("QUIT :bye\r\n"))
	// Server should send ERROR and close. Read until EOF.
	deadline := time.Now().Add(2 * time.Second)
	sawError := false
	for {
		_ = c.SetReadDeadline(deadline)
		line, err := r.ReadString('\n')
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if !sawError {
				t.Error("never saw ERROR before close")
			}
			return
		}
		if err != nil {
			// Some platforms surface "connection reset" rather than EOF
			// when the server closes immediately after writing.
			if !sawError {
				t.Errorf("read: %v (no ERROR seen)", err)
			}
			return
		}
		if strings.HasPrefix(line, "ERROR ") {
			sawError = true
		}
	}
}

func TestUnknownCommand_BeforeRegistration(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("WHATEVER foo\r\n"))
	expectNumeric(t, c, r, "451", time.Now().Add(2*time.Second))
}

func TestRegistration_GatedOnCapEnd(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()

	// IRCv3-style registration: open negotiation, send NICK + USER,
	// then expect that the welcome burst does NOT arrive until we
	// send CAP END.
	if _, err := c.Write([]byte("CAP LS 302\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n")); err != nil {
		t.Fatal(err)
	}

	// Read until we see the CAP LS reply, asserting that no welcome
	// numeric (001) arrives in the meantime. We use net.Conn's read
	// deadline rather than the goroutine helper because we need a
	// real timeout *and* no leaked reader goroutine — the helper
	// leaves a goroutine reading the bufio.Reader on timeout, which
	// races with the next call.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	sawCap := false
	for !sawCap {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS reply: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			sawCap = true
			continue
		}
		if extractNumeric(line) == "001" {
			t.Fatalf("got 001 before CAP END: %q", line)
		}
	}

	// Now verify no further data arrives within a short window — the
	// server must hold the welcome burst until CAP END.
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if line, err := r.ReadString('\n'); err == nil {
		t.Fatalf("server sent data while waiting for CAP END: %q", line)
	}

	// Send CAP END and expect the welcome burst.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("CAP END\r\n")); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for 001: %v", err)
		}
		if extractNumeric(strings.TrimRight(line, "\r\n")) == "001" {
			return
		}
	}
}

func TestUnknownCommand_AfterRegistration(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\n"))
	c.Write([]byte("USER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("FROBNICATE\r\n"))
	expectNumeric(t, c, r, "421", time.Now().Add(2*time.Second))
}
