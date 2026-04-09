package server

import (
	"strings"
	"testing"
	"time"
)

func TestEchoMessage_ChannelEchoesSender(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice negotiates echo-message before NICK/USER.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP REQ :echo-message\r\nCAP END\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #echo\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG #echo :hello self\r\n"))

	// alice should receive her own PRIVMSG back.
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice did not receive her own echoed message")
		}
		if strings.Contains(line, "PRIVMSG #echo") && strings.Contains(line, "hello self") {
			return
		}
	}
}

func TestEchoMessage_ChannelDoesNotEchoWithoutCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #ne\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG #ne :no echo please\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "PRIVMSG #ne") && strings.Contains(line, "no echo please") {
			t.Errorf("PRIVMSG echoed without echo-message: %q", line)
		}
	}
}

func TestEchoMessage_DirectEchoesSender(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP REQ :echo-message\r\nCAP END\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG bob :direct hello\r\n"))

	// alice should see her own message echoed back.
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice did not see her own direct privmsg")
		}
		if strings.Contains(line, "PRIVMSG bob") && strings.Contains(line, "direct hello") {
			return
		}
	}
}
