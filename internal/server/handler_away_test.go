package server

import (
	"strings"
	"testing"
	"time"
)

func TestAway_SetAndClearConfirmations(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	// Set: expect 306 RPL_NOWAWAY.
	c.Write([]byte("AWAY :brb coffee\r\n"))
	expectNumeric(t, c, r, "306", time.Now().Add(2*time.Second))

	// Clear: expect 305 RPL_UNAWAY.
	c.Write([]byte("AWAY\r\n"))
	expectNumeric(t, c, r, "305", time.Now().Add(2*time.Second))
}

func TestAway_PrivmsgEchoesRplAway(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice connects + goes away.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("AWAY :brb coffee\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	// bob connects and PRIVMSGs alice.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("PRIVMSG alice :hello\r\n"))

	// bob should receive a 301 RPL_AWAY echoing alice's message.
	line := expectNumeric(t, cBob, rBob, "301", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "alice") || !strings.Contains(line, "brb coffee") {
		t.Errorf("301 missing nick or away message: %q", line)
	}

	// And alice should still actually receive the PRIVMSG.
	deadline := time.Now().Add(2 * time.Second)
	cAlice.SetReadDeadline(deadline)
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice never received the privmsg")
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello") {
			break
		}
	}
}

func TestAway_NoticeDoesNotEchoRplAway(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// NOTICE should NOT trigger 301. We check by reading
	// every line for 1 second; if a 301 shows up that is a
	// failure.
	cBob.Write([]byte("NOTICE alice :ping\r\n"))
	cBob.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break // deadline → good, no 301
		}
		if strings.Contains(line, " 301 ") {
			t.Errorf("NOTICE should not trigger 301 RPL_AWAY: %q", line)
		}
	}
}
