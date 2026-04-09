package server

import (
	"strings"
	"testing"
	"time"
)

func TestQuiet_BlocksMatchingUserSpeech(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #q\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	var bobMask string
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			bobMask = u.Hostmask()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bobMask == "" {
		t.Fatal("bob never registered")
	}

	cBob.Write([]byte("JOIN #q\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	// alice quiets bob's exact hostmask.
	cAlice.Write([]byte("MODE #q +q " + bobMask + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || strings.Contains(line, "+q") {
			break
		}
	}

	// bob tries to speak — should get 404 ERR_CANNOTSENDTOCHAN.
	cBob.Write([]byte("PRIVMSG #q :hello\r\n"))
	expectNumeric(t, cBob, rBob, "404", time.Now().Add(2*time.Second))

	// alice should NOT see bob's message.
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello") {
			t.Errorf("quieted user's message reached the channel: %q", line)
		}
	}
}

func TestQuiet_BareListQueryReturns728(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("JOIN #ql\r\n"))
	expectNumeric(t, c, r, "366", time.Now().Add(2*time.Second))

	c.Write([]byte("MODE #ql +q *!*@spam.example\r\n"))
	c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := r.ReadString('\n')
		if err != nil || strings.Contains(line, "+q") {
			break
		}
	}

	c.Write([]byte("MODE #ql +q\r\n"))
	line := expectNumeric(t, c, r, "728", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "spam.example") {
		t.Errorf("728 missing quiet mask: %q", line)
	}
	expectNumeric(t, c, r, "729", time.Now().Add(2*time.Second))
}

func TestQuiet_VoiceOverridesQuiet(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #qv\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #qv\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	var bobMask string
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			bobMask = u.Hostmask()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bobMask == "" {
		t.Fatal("bob never registered")
	}

	// quiet then voice
	cAlice.Write([]byte("MODE #qv +q " + bobMask + "\r\n"))
	cAlice.Write([]byte("MODE #qv +v bob\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "+v") && strings.Contains(line, "bob")) {
			break
		}
	}

	// bob speaks despite the quiet because he is voiced.
	cBob.Write([]byte("PRIVMSG #qv :i can speak\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice never received voiced quieted user's message")
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "i can speak") {
			return
		}
	}
}
