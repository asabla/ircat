package server

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLinks_AlwaysIncludesLocalNode(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("LINKS\r\n"))
	line := expectNumeric(t, c, r, "364", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "irc.test") {
		t.Errorf("364 missing local server name: %q", line)
	}
	expectNumeric(t, c, r, "365", time.Now().Add(2*time.Second))
}

func TestSquit_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("SQUIT some.peer :reason\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestSquit_NoSuchServer(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.Write([]byte("SQUIT ghost.example :gone\r\n"))
	expectNumeric(t, c, r, "402", time.Now().Add(2*time.Second))
}

func TestConnect_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("CONNECT peer.example 6667\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

type fakeConnector struct {
	calls atomic.Int32
	last  string
	err   error
}

func (f *fakeConnector) Connect(_ context.Context, target string, _ int) error {
	f.last = target
	f.calls.Add(1)
	return f.err
}

func TestConnect_DialsViaConnector(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	conn := &fakeConnector{}
	WithConnector(conn)(srv)

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("CONNECT peer.example 6667\r\n"))
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn.calls.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if conn.calls.Load() != 1 {
		t.Fatal("connector never called")
	}
	if conn.last != "peer.example" {
		t.Errorf("expected peer.example, got %q", conn.last)
	}
}

func TestConnect_NoConnectorEmitsNotice(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("CONNECT peer.example 6667\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "NOTICE") && strings.Contains(line, "CONNECT") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected NOTICE about missing connector")
	}
}

var _ = errors.New
