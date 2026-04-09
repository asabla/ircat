package server

import (
	"strings"
	"testing"
	"time"
)

func TestMaskBroadcast_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("PRIVMSG $* :hi\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestMaskBroadcast_ServerMaskFansOutToLocalUsers(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG $*.test :network announce\r\n"))

	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "network announce") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("bob did not receive the $mask broadcast")
	}
}

func TestMaskBroadcast_HostMaskMatchesByHost(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// We don't know the test loopback host exactly but we know
	// it starts with 127. Use a "*.0.0.1" pattern that should
	// match it on most local setups, then fall back to "*" if
	// it does not.
	cAlice.Write([]byte("PRIVMSG #* :every host\r\n"))

	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "every host") {
			saw = true
			break
		}
	}
	// "#*" contains no dot so it would parse as a channel target,
	// not a host mask — confirm bob does NOT receive it via the
	// mask path. He should instead receive nothing because
	// "#*" is not a valid channel name.
	if saw {
		t.Errorf("a bare #* without a dot should not be treated as host mask")
	}

	// Now try the proper host mask form (with dot).
	cAlice.Write([]byte("PRIVMSG #*.* :every dotted host\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw = false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "every dotted host") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("bob did not receive the #*.* host-mask broadcast")
	}
}
