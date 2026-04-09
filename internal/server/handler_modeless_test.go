package server

import (
	"strings"
	"testing"
	"time"
)

func TestModelessChannel_JoinWorksNoOpGranted(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("JOIN +modeless\r\n"))
	expectNumeric(t, c, r, "366", time.Now().Add(2*time.Second))

	// alice is the first joiner of a +channel — she must NOT
	// be opped per RFC 2811 §4.2.1.
	ch := srv.world.FindChannel("+modeless")
	if ch == nil {
		t.Fatal("channel not created")
	}
	u := srv.world.FindByNick("alice")
	if u == nil {
		t.Fatal("alice not registered")
	}
	if ch.Membership(u.ID).IsOp() {
		t.Errorf("first joiner of a + channel should not be opped")
	}
}

func TestModelessChannel_RejectsModeMutation(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("JOIN +noflags\r\n"))
	expectNumeric(t, c, r, "366", time.Now().Add(2*time.Second))

	c.Write([]byte("MODE +noflags +t\r\n"))
	line := expectNumeric(t, c, r, "482", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "+noflags") {
		t.Errorf("482 should mention the channel name: %q", line)
	}
}

func TestModelessChannel_PrivmsgDeliversToMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN +chat\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN +chat\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG +chat :hello modeless\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			t.Fatal("bob never received the privmsg")
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello modeless") {
			return
		}
	}
}
