package server

import (
	"strings"
	"testing"
	"time"
)

func TestChghost_OperatorCanChangeHost(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice negotiates chghost.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP LS\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cAlice.Write([]byte("CAP REQ :chghost\r\n"))
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	cAlice.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))

	// bob registers normally (no chghost cap).
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	// Both join a channel.
	cAlice.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " 366 ")
	})
	cBob.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " 366 ")
	})
	// Drain bob join echo on alice.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	// Grant alice +o so she can use CHGHOST.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes = "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// alice changes bob's host.
	cAlice.Write([]byte("CHGHOST bob new.host.example\r\n"))

	// alice (with chghost cap) should see the CHGHOST notification.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "CHGHOST") && strings.Contains(line, "new.host.example")
	})

	// bob (without chghost cap) should NOT see the notification.
	// Send a PING probe to flush.
	cBob.Write([]byte("PING :probe\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.Contains(line, "CHGHOST") {
			t.Errorf("bob without chghost cap should not see CHGHOST: %q", line)
		}
		return strings.Contains(line, "PONG") && strings.Contains(line, "probe")
	})

	// Verify the host actually changed.
	u := srv.world.FindByNick("bob")
	if u == nil {
		t.Fatal("bob not found")
	}
	if u.Host != "new.host.example" {
		t.Errorf("expected host new.host.example, got %q", u.Host)
	}
}

func TestChghost_NonOperatorRejected(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("CHGHOST bob newhost\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestChghost_NoSuchNick(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes = "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("CHGHOST ghost newhost\r\n"))
	expectNumeric(t, c, r, "401", time.Now().Add(2*time.Second))
}
