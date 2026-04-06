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

// startTestServer brings up a Server bound to a random localhost port
// and returns the address plus a teardown function. Each test gets
// its own World and Server.
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
				NickLength:          30,
				ChannelLength:       50,
				TopicLength:         390,
				AwayLength:          255,
				KickReasonLength:    255,
				PingIntervalSeconds: 1,
				PingTimeoutSeconds:  4,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()

	// Bind a real listener so the test can dial it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Listeners[0].Address = ln.Addr().String()
	// Hand the already-bound listener to the server by injecting it
	// after construction. We close ln immediately because Server.Run
	// will rebind on the same address; on Linux that's fine because
	// the kernel releases the port instantly.
	addr = ln.Addr().String()
	_ = ln.Close()

	srv := New(cfg, world, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()

	// Wait briefly for the server to actually be listening.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start listening on %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
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
// the requested numeric, then returns it. Lines that do not match
// (e.g. CAP banter) are discarded but logged. Times out after the
// configured deadline.
func expectNumeric(t *testing.T, r *bufio.Reader, code string, deadline time.Time) string {
	t.Helper()
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for numeric %s", code)
		}
		_ = r.Buffered() // touch to allow short reads to flush
		line, err := readLineWithTimeout(r, time.Until(deadline))
		if err != nil {
			t.Fatalf("read while waiting for %s: %v", code, err)
		}
		if extractNumeric(line) == code {
			return line
		}
	}
}

func readLineWithTimeout(r *bufio.Reader, d time.Duration) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{strings.TrimRight(line, "\r\n"), err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(d):
		return "", io.ErrUnexpectedEOF
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
		line := expectNumeric(t, r, code, deadline)
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
	expectNumeric(t, r1, "001", time.Now().Add(2*time.Second))

	c2, r2 := dialClient(t, addr)
	defer c2.Close()
	c2.Write([]byte("NICK ALICE\r\n")) // colliding under rfc1459 fold
	c2.Write([]byte("USER alice 0 * :Alice2\r\n"))
	line := expectNumeric(t, r2, "433", time.Now().Add(2*time.Second))
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
	expectNumeric(t, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("PING :token123\r\n"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no PONG seen")
		}
		line, err := readLineWithTimeout(r, time.Until(deadline))
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
	expectNumeric(t, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("QUIT :bye\r\n"))
	// Server should send ERROR and close. Read until EOF.
	deadline := time.Now().Add(2 * time.Second)
	sawError := false
	for {
		if time.Now().After(deadline) {
			t.Fatal("connection not closed after QUIT")
		}
		line, err := readLineWithTimeout(r, time.Until(deadline))
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
	expectNumeric(t, r, "451", time.Now().Add(2*time.Second))
}

func TestUnknownCommand_AfterRegistration(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\n"))
	c.Write([]byte("USER alice 0 * :Alice\r\n"))
	expectNumeric(t, r, "001", time.Now().Add(2*time.Second))

	c.Write([]byte("FROBNICATE\r\n"))
	expectNumeric(t, r, "421", time.Now().Add(2*time.Second))
}
