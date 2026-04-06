package server

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// register dials the test server, sends NICK + USER, and reads
// through the welcome burst (001 .. 422). Returns the connection,
// reader, and the chosen nick. Used by every channel test.
func register(t *testing.T, addr, nick string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, r := dialClient(t, addr)
	if _, err := c.Write([]byte("NICK " + nick + "\r\nUSER " + nick + " 0 * :" + nick + "\r\n")); err != nil {
		t.Fatal(err)
	}
	expectNumeric(t, r, "422", time.Now().Add(2*time.Second))
	return c, r
}

// readUntil reads lines from r until match returns true or the
// deadline passes. Returns the matching line and a trace of every
// line read (so test failures can show what the server actually
// sent). Uses the underlying conn's read deadline so there is no
// goroutine leak.
func readUntil(t *testing.T, c net.Conn, r *bufio.Reader, deadline time.Time, match func(string) bool) (string, []string) {
	t.Helper()
	var trace []string
	for {
		_ = c.SetReadDeadline(deadline)
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("readUntil: %v\n  trace: %v", err, trace)
		}
		line = strings.TrimRight(line, "\r\n")
		trace = append(trace, line)
		if match(line) {
			return line, trace
		}
	}
}

func TestJoin_FirstUserIsOpped(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	if _, err := c.Write([]byte("JOIN #test\r\n")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	// Expect a JOIN echo with alice as prefix.
	readUntil(t, c, r, deadline, func(line string) bool {
		return strings.HasPrefix(line, ":alice!") && strings.Contains(line, " JOIN ")
	})
	// Expect RPL_NOTOPIC (331) since the channel is fresh.
	readUntil(t, c, r, deadline, func(line string) bool {
		return extractNumeric(line) == "331"
	})
	// Expect RPL_NAMREPLY (353) with @alice.
	line, _ := readUntil(t, c, r, deadline, func(line string) bool {
		return extractNumeric(line) == "353"
	})
	if !strings.Contains(line, "@alice") {
		t.Errorf("alice should be opped in 353: %q", line)
	}
	// Expect RPL_ENDOFNAMES (366).
	readUntil(t, c, r, deadline, func(line string) bool {
		return extractNumeric(line) == "366"
	})
}

func TestJoin_SecondUserSeesExistingMemberAndIsBroadcast(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("JOIN #test\r\n"))
	// Drain alice's join + names burst.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("JOIN #test\r\n"))

	// Bob should see his own JOIN echoed and a 353 listing alice.
	deadline := time.Now().Add(2 * time.Second)
	readUntil(t, cBob, rBob, deadline, func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ")
	})
	line, _ := readUntil(t, cBob, rBob, deadline, func(line string) bool {
		return extractNumeric(line) == "353"
	})
	if !strings.Contains(line, "@alice") || !strings.Contains(line, "bob") {
		t.Errorf("353 should list both: %q", line)
	}

	// Alice should also see bob's JOIN.
	readUntil(t, cAlice, rAlice, deadline, func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ")
	})
}

func TestJoin_InvalidChannelName(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN nothash\r\n"))
	expectNumeric(t, r, "403", time.Now().Add(2*time.Second))
}

func TestPart_BroadcastsAndRemoves(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	// Drain bob's JOIN echo on alice's stream.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cBob.Write([]byte("PART #test :bye\r\n"))
	// Bob should see his own PART echoed.
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " PART ")
	})
	// Alice should see bob's PART too.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " PART ")
	})
}

func TestPart_NotOnChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	cBob.Write([]byte("JOIN #test\r\n"))
	// alice attempts to part #test without joining.
	c.Write([]byte("PART #test\r\n"))
	expectNumeric(t, r, "442", time.Now().Add(2*time.Second))
}

func TestJoin_ZeroPartsAll(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #a\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("JOIN #b\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("JOIN 0\r\n"))
	// Expect a PART for both channels.
	gotA, gotB := false, false
	deadline := time.Now().Add(2 * time.Second)
	for !(gotA && gotB) {
		_ = c.SetReadDeadline(deadline)
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (gotA=%v gotB=%v)", err, gotA, gotB)
		}
		if strings.Contains(line, " PART #a") {
			gotA = true
		}
		if strings.Contains(line, " PART #b") {
			gotB = true
		}
	}
}
