package server

import (
	"strings"
	"testing"
	"time"
)

func TestAwayNotify_BroadcastsToCapableSharedChannelMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice joins #an.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #an\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob negotiates away-notify and joins.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP REQ :away-notify\r\nCAP END\r\nNICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #an\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	// alice goes away.
	cAlice.Write([]byte("AWAY :brb coffee\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	// bob should see ":alice!alice@... AWAY :brb coffee".
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "alice") && strings.Contains(line, " AWAY ") &&
			strings.Contains(line, "brb coffee") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("bob did not receive AWAY notification with reason")
	}

	// alice comes back.
	cAlice.Write([]byte("AWAY\r\n"))
	expectNumeric(t, cAlice, rAlice, "305", time.Now().Add(2*time.Second))

	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotBack := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		// The bare "AWAY" form has no trailing param.
		if strings.Contains(line, "alice") && strings.HasSuffix(strings.TrimRight(line, "\r\n"), "AWAY") {
			gotBack = true
			break
		}
	}
	if !gotBack {
		t.Errorf("bob did not receive bare AWAY back-notification")
	}
}

func TestAwayNotify_NotSentWithoutCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #ann\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #ann\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	// Without the cap bob must NOT receive an AWAY line.
	cBob.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " AWAY ") || strings.HasSuffix(strings.TrimRight(line, "\r\n"), "AWAY") {
			t.Errorf("non-away-notify client got AWAY line: %q", line)
		}
	}
}

func TestAwayNotify_DedupesAcrossSharedChannels(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #x,#y\r\n"))
	// Drain joins
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 366 ") && strings.Contains(line, "#y") {
			break
		}
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP REQ :away-notify\r\nCAP END\r\nNICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #x,#y\r\n"))
	cBob.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 366 ") && strings.Contains(line, "#y") {
			break
		}
	}

	cAlice.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	count := 0
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "alice") && strings.Contains(line, " AWAY ") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("bob should receive exactly one AWAY notification across two shared channels, got %d", count)
	}
}
