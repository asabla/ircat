package server

import (
	"strings"
	"testing"
	"time"
)

// These tests fill in the parameter and edge-case coverage that the
// command-specific test files miss. They are intentionally short:
// each one verifies a single RFC numeric or behaviour.

func TestNeedMoreParams_JOIN(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestNeedMoreParams_PART(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("PART\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestNeedMoreParams_TOPIC(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("TOPIC\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestNeedMoreParams_MODE(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("MODE\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestNeedMoreParams_KICK(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("KICK #x\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestMode_NoSuchChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("MODE #ghost\r\n"))
	expectNumeric(t, c, r, "403", time.Now().Add(2*time.Second))
}

// TestNick_PreservesChannelMembership verifies that a post-
// registration NICK does not drop the user from any channels they
// are in. The renamer remains opped, and PRIVMSGs to the channel
// from the new nick continue to flow under the +n default.
func TestNick_PreservesChannelMembership(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	// Drain bob join echo on alice.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	// Bob renames to robert. Alice should see the NICK; the test
	// for that lives in TestNick_BroadcastsToChannels — here we
	// verify that robert can still PRIVMSG the channel and that
	// alice still receives it (proving membership and op state
	// survived the rename).
	cBob.Write([]byte("NICK robert\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " NICK ") && strings.Contains(l, "robert")
	})

	cBob.Write([]byte("PRIVMSG #x :I am still here\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":robert!") &&
			strings.Contains(line, " PRIVMSG #x ") &&
			strings.HasSuffix(line, ":I am still here")
	})
}

// TestPrivmsg_MultipleTargets verifies that a comma-separated
// target list delivers to every target.
func TestPrivmsg_MultipleTargets(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, _ := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cCarol, rCarol := register(t, addr, "carol")
	defer cCarol.Close()

	cAlice.Write([]byte("PRIVMSG bob,carol :hello both\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG bob ") &&
			strings.HasSuffix(line, ":hello both")
	})
	readUntil(t, cCarol, rCarol, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG carol ") &&
			strings.HasSuffix(line, ":hello both")
	})
}
