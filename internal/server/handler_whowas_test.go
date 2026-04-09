package server

import (
	"strings"
	"testing"
	"time"
)

func TestWhowas_AfterDisconnect(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice connects, then disconnects.
	cAlice, rAlice := dialClient(t, addr)
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice McCoy\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("QUIT :bye\r\n"))
	cAlice.Close()

	// bob connects and asks WHOWAS alice.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// Give the disconnect path a moment to record.
	time.Sleep(50 * time.Millisecond)

	cBob.Write([]byte("WHOWAS alice\r\n"))
	line := expectNumeric(t, cBob, rBob, "314", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "alice") || !strings.Contains(line, "Alice McCoy") {
		t.Errorf("314 missing nick or realname: %q", line)
	}
	expectNumeric(t, cBob, rBob, "312", time.Now().Add(2*time.Second))
	expectNumeric(t, cBob, rBob, "369", time.Now().Add(2*time.Second))
}

func TestWhowas_NoSuchNick(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("WHOWAS ghostling\r\n"))
	expectNumeric(t, c, r, "406", time.Now().Add(2*time.Second))
	expectNumeric(t, c, r, "369", time.Now().Add(2*time.Second))
}

func TestWhowas_AfterRename(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// Rename alice → alice2 — old nick should land in whowas.
	cAlice.Write([]byte("NICK alice2\r\n"))
	// Drain the NICK echo.
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "NICK") && strings.Contains(line, "alice2") {
			break
		}
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cBob.Write([]byte("WHOWAS alice\r\n"))
	line := expectNumeric(t, cBob, rBob, "314", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "alice") {
		t.Errorf("314 missing old nick: %q", line)
	}
	expectNumeric(t, cBob, rBob, "312", time.Now().Add(2*time.Second))
	expectNumeric(t, cBob, rBob, "369", time.Now().Add(2*time.Second))
}

func TestWhowas_NoNicknameGiven(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("WHOWAS\r\n"))
	expectNumeric(t, c, r, "431", time.Now().Add(2*time.Second))
}
