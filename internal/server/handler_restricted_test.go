package server

import (
	"strings"
	"testing"
	"time"
)

func TestRestricted_NickChangeBlocked(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "r"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("NICK alice2\r\n"))
	expectNumeric(t, c, r, "484", time.Now().Add(2*time.Second))

	// Verify the nick did not actually change.
	if srv.world.FindByNick("alice") == nil {
		t.Errorf("alice should still exist after blocked rename")
	}
	if srv.world.FindByNick("alice2") != nil {
		t.Errorf("alice2 should not exist; restricted rename should be a no-op")
	}
}

func TestRestricted_OperBlocked(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "r"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("OPER alice secret\r\n"))
	expectNumeric(t, c, r, "484", time.Now().Add(2*time.Second))
}

func TestRestricted_UserCannotSetOrUnsetR(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("MODE alice +r\r\n"))
	c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " MODE ") && strings.Contains(line, "+r") {
			t.Errorf("self-set +r should be silently ignored: %q", line)
		}
		if strings.Contains(line, " 484 ") {
			break
		}
	}
}
