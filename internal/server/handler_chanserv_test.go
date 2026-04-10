package server

import (
	"strings"
	"testing"
	"time"
)

func TestChanServ_AppearsInServlist(t *testing.T) {
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
		if strings.Contains(line, " 234 ") && strings.Contains(line, "ChanServ") {
			found = true
		}
		if strings.Contains(line, " 235 ") {
			break
		}
	}
	if !found {
		t.Error("ChanServ not found in SERVLIST")
	}
}

func TestChanServ_RegisterAndInfo(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	// Register an account first via NickServ.
	c.Write([]byte("PRIVMSG NickServ :REGISTER mypass\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "registered successfully")
	})

	// Identify.
	c.Write([]byte("PRIVMSG NickServ :IDENTIFY mypass\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "identified as alice")
	})

	// Join a channel (makes alice the op as first user).
	c.Write([]byte("JOIN #test\r\n"))
	// Read through the full JOIN burst (JOIN echo, 331, 353, 366).
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, " 366 ") // End of NAMES
	})

	// Register the channel with ChanServ.
	c.Write([]byte("PRIVMSG ChanServ :REGISTER #test\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "has been registered")
	})

	// INFO should show the channel.
	c.Write([]byte("PRIVMSG ChanServ :INFO #test\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "Founder: alice")
	})
	if !strings.Contains(line, "Guard: ON") {
		t.Errorf("expected Guard: ON in INFO, got: %s", line)
	}
}

func TestChanServ_OpGrantsMode(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	// Alice: register, identify, join, register channel.
	alice, ar := register(t, addr, "alice")
	defer alice.Close()

	alice.Write([]byte("PRIVMSG NickServ :REGISTER alicepass\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "registered successfully")
	})
	alice.Write([]byte("PRIVMSG NickServ :IDENTIFY alicepass\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "identified as alice")
	})
	alice.Write([]byte("JOIN #ops\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "JOIN") && strings.Contains(line, "#ops")
	})
	alice.Write([]byte("PRIVMSG ChanServ :REGISTER #ops\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "has been registered")
	})

	// Bob joins.
	bob, br := register(t, addr, "bob")
	defer bob.Close()

	bob.Write([]byte("JOIN #ops\r\n"))
	readUntil(t, bob, br, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "JOIN") && strings.Contains(line, "#ops")
	})
	// Drain alice's side of bob's JOIN.
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "JOIN") && strings.Contains(line, "bob")
	})

	// Alice ops bob via ChanServ.
	alice.Write([]byte("PRIVMSG ChanServ :OP #ops bob\r\n"))

	// Bob should see MODE +o.
	readUntil(t, bob, br, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "MODE") && strings.Contains(line, "+o") && strings.Contains(line, "bob")
	})

	// Alice should also see the confirmation notice.
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "has been opped")
	})
}

func TestChanServ_NonFounderCannotDrop(t *testing.T) {
	addr, _, teardown := startTestServerWithStoreAndNickServ(t)
	defer teardown()

	// Alice registers account and channel.
	alice, ar := register(t, addr, "alice")
	defer alice.Close()

	alice.Write([]byte("PRIVMSG NickServ :REGISTER alicepass\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "registered successfully")
	})
	alice.Write([]byte("PRIVMSG NickServ :IDENTIFY alicepass\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "identified as alice")
	})
	alice.Write([]byte("JOIN #secret\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "JOIN")
	})
	alice.Write([]byte("PRIVMSG ChanServ :REGISTER #secret\r\n"))
	readUntil(t, alice, ar, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "has been registered")
	})

	// Bob registers and identifies under a different account.
	bob, br := register(t, addr, "bob")
	defer bob.Close()

	bob.Write([]byte("PRIVMSG NickServ :REGISTER bobpass\r\n"))
	readUntil(t, bob, br, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "registered successfully")
	})
	bob.Write([]byte("PRIVMSG NickServ :IDENTIFY bobpass\r\n"))
	readUntil(t, bob, br, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "identified as bob")
	})

	// Bob tries to drop alice's channel.
	bob.Write([]byte("PRIVMSG ChanServ :DROP #secret\r\n"))
	line, _ := readUntil(t, bob, br, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "NOTICE") && strings.Contains(line, "ChanServ")
	})
	if !strings.Contains(line, "founder") {
		t.Errorf("expected founder-only error, got: %s", line)
	}
}
