package server

import (
	"strings"
	"testing"
	"time"
)

func TestStats_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("STATS u\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestStats_UEmitsUptime(t *testing.T) {
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

	c.Write([]byte("STATS u\r\n"))
	line := expectNumeric(t, c, r, "242", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "Server Up") {
		t.Errorf("242 missing uptime label: %q", line)
	}
	expectNumeric(t, c, r, "219", time.Now().Add(2*time.Second))
}

func TestStats_MEmitsCommandTotals(t *testing.T) {
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

	c.Write([]byte("STATS m\r\n"))
	expectNumeric(t, c, r, "212", time.Now().Add(2*time.Second))
	expectNumeric(t, c, r, "219", time.Now().Add(2*time.Second))
}

func TestStats_LEmitsEndWithNoLinks(t *testing.T) {
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

	// No fed links configured — STATS l should still terminate cleanly.
	c.Write([]byte("STATS l\r\n"))
	expectNumeric(t, c, r, "219", time.Now().Add(2*time.Second))
}
