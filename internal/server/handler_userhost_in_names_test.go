package server

import (
	"strings"
	"testing"
	"time"
)

func TestUserhostInNames_RendersFullHostmask(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice creates the channel without the cap.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #uhin\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob negotiates userhost-in-names and joins.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP REQ :userhost-in-names\r\nCAP END\r\nNICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))

	cBob.Write([]byte("NAMES #uhin\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") && strings.Contains(line, "#uhin") {
			body := line[strings.LastIndex(line, ":")+1:]
			body = strings.TrimRight(body, "\r\n")
			// With the cap, alice should appear as alice!alice@host
			if strings.Contains(body, "alice!alice@") {
				saw = true
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if !saw {
		t.Errorf("userhost-in-names did not render full hostmask")
	}
}

func TestUserhostInNames_NotRenderedWithoutCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #plain\r\n"))

	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") && strings.Contains(line, "#plain") {
			body := line[strings.LastIndex(line, ":")+1:]
			// The "nick!user@host" form contains "!" — the
			// status prefix "@" alone (op marker) does not.
			if strings.Contains(body, "!") {
				t.Errorf("non-userhost-in-names client got hostmask form: %q", body)
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
}
