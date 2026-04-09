package server

import (
	"strings"
	"testing"
	"time"
)

func TestAway_SetsUserModeA(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, c, r, "306", time.Now().Add(2*time.Second))

	u := srv.world.FindByNick("alice")
	if u == nil {
		t.Fatal("alice not registered")
	}
	if !strings.ContainsRune(u.Modes, 'a') {
		t.Errorf("user mode +a not set after AWAY: modes=%q", u.Modes)
	}

	// Clear AWAY → +a should be removed.
	c.Write([]byte("AWAY\r\n"))
	expectNumeric(t, c, r, "305", time.Now().Add(2*time.Second))

	u = srv.world.FindByNick("alice")
	if strings.ContainsRune(u.Modes, 'a') {
		t.Errorf("user mode +a not cleared after AWAY clear: modes=%q", u.Modes)
	}
}

func TestSafeChannel_FirstJoinerIsCreator(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("JOIN !!secret\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var canonical string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " JOIN ") && strings.Contains(line, "!") {
			idx := strings.LastIndex(line, "!")
			canonical = strings.TrimRight(line[idx:], "\r\n :")
		}
		// 353 should show alice with the "!" creator prefix.
		if strings.Contains(line, " 353 ") {
			body := line[strings.LastIndex(line, ":")+1:]
			body = strings.TrimRight(body, "\r\n")
			if !strings.Contains(body, "!alice") {
				t.Errorf("353 should mark alice with ! creator prefix: %q", body)
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if canonical == "" {
		t.Fatal("did not capture canonical channel name")
	}
	ch := srv.world.FindChannel(canonical)
	if ch == nil {
		t.Fatalf("canonical channel %q missing from world", canonical)
	}
	u := srv.world.FindByNick("alice")
	mem := ch.Membership(u.ID)
	if !mem.IsCreator() {
		t.Errorf("alice should have MemberCreator on safe channel founder")
	}
	if !mem.IsOp() {
		t.Errorf("MemberCreator should imply op privileges")
	}
}

func TestSafeChannel_CreatorCannotBeDeoped(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN !!locked\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	var canonical string
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " JOIN ") && strings.Contains(line, "!") {
			idx := strings.LastIndex(line, "!")
			canonical = strings.TrimRight(line[idx:], "\r\n :")
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if canonical == "" {
		t.Fatal("no canonical name captured")
	}

	// Try to remove +o from alice via MODE -o (alice on herself).
	cAlice.Write([]byte("MODE " + canonical + " -o alice\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		_, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
	}

	// Membership should still report op (creator implies op).
	ch := srv.world.FindChannel(canonical)
	u := srv.world.FindByNick("alice")
	mem := ch.Membership(u.ID)
	if !mem.IsCreator() {
		t.Errorf("MemberCreator was stripped despite RFC 2811 §4.3.5")
	}
	if !mem.IsOp() {
		t.Errorf("creator should still be op after -o attempt")
	}
}
