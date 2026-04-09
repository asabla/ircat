package server

import (
	"strings"
	"testing"
	"time"
)

func TestMultiPrefix_NamesShowsAllPrefixes(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice creates #mp and gets +o automatically.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #mp\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob joins; alice voices and ops bob.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #mp\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("MODE #mp +ov bob bob\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "MODE") && strings.Contains(line, "+ov")) {
			break
		}
	}

	// charlie negotiates multi-prefix and asks NAMES.
	cChar, rChar := dialClient(t, addr)
	defer cChar.Close()
	cChar.Write([]byte("CAP REQ :multi-prefix\r\nCAP END\r\nNICK charlie\r\nUSER charlie 0 * :Charlie\r\n"))
	expectNumeric(t, cChar, rChar, "001", time.Now().Add(2*time.Second))
	cChar.Write([]byte("NAMES #mp\r\n"))
	cChar.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rChar.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") {
			body := line[strings.LastIndex(line, ":")+1:]
			body = strings.TrimRight(body, "\r\n")
			// bob has both @ and + — multi-prefix should
			// render "@+bob".
			if strings.Contains(body, "@+bob") {
				saw = true
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if !saw {
		t.Errorf("multi-prefix NAMES did not render @+bob")
	}

	// And without the cap, NAMES should fall back to single prefix.
	cDan, rDan := dialClient(t, addr)
	defer cDan.Close()
	cDan.Write([]byte("NICK dan\r\nUSER dan 0 * :Dan\r\n"))
	expectNumeric(t, cDan, rDan, "422", time.Now().Add(2*time.Second))
	cDan.Write([]byte("NAMES #mp\r\n"))
	cDan.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rDan.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") {
			body := line[strings.LastIndex(line, ":")+1:]
			if strings.Contains(body, "@+bob") {
				t.Errorf("non-multi-prefix client got @+bob: %q", body)
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	_ = srv
}
