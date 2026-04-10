package server

import (
	"strings"
	"testing"
	"time"
)

func TestMemoServ_AppearsInServlist(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("SERVLIST\r\n"))
	found := false
	deadline := time.Now().Add(2 * time.Second)
	for {
		line, _ := readUntil(t, c, r, deadline, func(l string) bool {
			return strings.Contains(l, " 234 ") || strings.Contains(l, " 235 ")
		})
		if strings.Contains(line, " 234 ") && strings.Contains(line, "MemoServ") {
			found = true
		}
		if strings.Contains(line, " 235 ") {
			break
		}
	}
	if !found {
		t.Errorf("MemoServ not found in SERVLIST")
	}
}

func TestMemoServ_SendListReadDelete(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	// Register and identify alice.
	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("PRIVMSG NickServ :REGISTER alicepass\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "registered successfully")
	})
	cAlice.Write([]byte("PRIVMSG NickServ :IDENTIFY alicepass\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "identified as alice")
	})

	// Register and identify bob.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("PRIVMSG NickServ :REGISTER bobpass\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "registered successfully")
	})
	cBob.Write([]byte("PRIVMSG NickServ :IDENTIFY bobpass\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "identified as bob")
	})

	// Alice sends a memo to bob.
	cAlice.Write([]byte("PRIVMSG MemoServ :SEND bob Hello from Alice!\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "Memo sent to bob")
	})

	// Bob lists memos.
	cBob.Write([]byte("PRIVMSG MemoServ :LIST\r\n"))
	listLine, _ := readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "NOTICE") && strings.Contains(l, "1 memo(s)")
	})
	if !strings.Contains(listLine, "alice") {
		t.Errorf("LIST should show sender alice, got: %s", listLine)
	}

	// Extract the memo ID from the list line. Format: [R] <id> from <sender>
	// Find the memo ID between "] " and " from".
	idx := strings.Index(listLine, "] ")
	if idx < 0 {
		t.Fatalf("could not find memo ID in LIST output: %s", listLine)
	}
	rest := listLine[idx+2:]
	spaceIdx := strings.Index(rest, " ")
	if spaceIdx < 0 {
		t.Fatalf("could not parse memo ID from LIST output: %s", listLine)
	}
	memoID := rest[:spaceIdx]

	// Bob reads the memo.
	cBob.Write([]byte("PRIVMSG MemoServ :READ " + memoID + "\r\n"))
	readLine, _ := readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "NOTICE") && strings.Contains(l, "Hello from Alice!")
	})
	if !strings.Contains(readLine, "alice") {
		t.Errorf("READ should show sender alice, got: %s", readLine)
	}

	// Bob deletes the memo.
	cBob.Write([]byte("PRIVMSG MemoServ :DELETE " + memoID + "\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "NOTICE") && strings.Contains(l, "Memo deleted")
	})

	// Verify LIST is now empty.
	cBob.Write([]byte("PRIVMSG MemoServ :LIST\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "NOTICE") && strings.Contains(l, "no memos")
	})
}

func TestMemoServ_UnreadNotificationOnIdentify(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	// Register and identify alice.
	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("PRIVMSG NickServ :REGISTER alicepass\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "registered successfully")
	})
	cAlice.Write([]byte("PRIVMSG NickServ :IDENTIFY alicepass\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "identified as alice")
	})

	// Register and identify bob, then send a memo to alice.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("PRIVMSG NickServ :REGISTER bobpass\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "registered successfully")
	})
	cBob.Write([]byte("PRIVMSG NickServ :IDENTIFY bobpass\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "identified as bob")
	})
	cBob.Write([]byte("PRIVMSG MemoServ :SEND alice Hey Alice, you have a memo!\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "Memo sent to alice")
	})

	// Close alice's first connection and reconnect with the same
	// nick so we can test IDENTIFY notification on a fresh session.
	cAlice.Close()
	// Give the server a moment to process the disconnect.
	time.Sleep(100 * time.Millisecond)

	cAlice2, rAlice2 := register(t, addr, "alice")
	defer cAlice2.Close()

	// Send IDENTIFY immediately — there may be a NickServ enforcement
	// warning arriving too, but we don't need to drain it first.
	cAlice2.Write([]byte("PRIVMSG NickServ :IDENTIFY alicepass\r\n"))

	// Read lines until we find both the identify confirmation and
	// the MemoServ unread notification.
	foundIdentify := false
	foundMemo := false
	deadline := time.Now().Add(3 * time.Second)
	cAlice2.SetReadDeadline(deadline)
	for !(foundIdentify && foundMemo) {
		line, err := rAlice2.ReadString('\n')
		if err != nil {
			t.Fatalf("read error: %v (foundIdentify=%v, foundMemo=%v)", err, foundIdentify, foundMemo)
		}
		if strings.Contains(line, "identified as alice") {
			foundIdentify = true
		}
		if strings.Contains(line, "MemoServ") && strings.Contains(line, "unread memo") {
			foundMemo = true
		}
	}
}

func TestMemoServ_RequiresIdentify(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	// Try to send a memo without being identified.
	c.Write([]byte("PRIVMSG MemoServ :SEND bob hello\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "NOTICE") && strings.Contains(l, "must be identified")
	})
}
