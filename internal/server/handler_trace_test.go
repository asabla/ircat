package server

import (
	"strings"
	"testing"
	"time"
)

func TestTrace_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("TRACE\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestTrace_EmitsUserAndEnd(t *testing.T) {
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

	c.Write([]byte("TRACE\r\n"))
	line := expectNumeric(t, c, r, "205", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "alice") {
		t.Errorf("205 missing nick: %q", line)
	}
	// Operator line follows since alice has +o.
	expectNumeric(t, c, r, "204", time.Now().Add(2*time.Second))
	expectNumeric(t, c, r, "262", time.Now().Add(2*time.Second))
}
