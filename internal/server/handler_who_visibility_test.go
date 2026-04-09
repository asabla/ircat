package server

import (
	"strings"
	"testing"
	"time"
)

func TestWho_HidesSecretChannelMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice owns a +s secret channel.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #hidden\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("MODE #hidden +s\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "MODE") && strings.Contains(line, "+s")) {
			break
		}
	}

	// bob is not a member.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cBob.Write([]byte("WHO #hidden\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw352 := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 352 ") {
			saw352 = true
		}
		if strings.Contains(line, " 315 ") {
			break
		}
	}
	if saw352 {
		t.Errorf("non-member saw 352 RPL_WHOREPLY for +s channel")
	}
}

func TestWho_FlagsGoneAndOper(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice goes away and gets +o.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, cAlice, rAlice, "306", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cBob.Write([]byte("WHO alice\r\n"))
	line := expectNumeric(t, cBob, rBob, "352", time.Now().Add(2*time.Second))
	// flags param is the 7th token; check it contains G and *.
	parts := strings.Fields(line)
	if len(parts) < 9 {
		t.Fatalf("malformed 352: %q", line)
	}
	flags := parts[8]
	if !strings.Contains(flags, "G") {
		t.Errorf("expected G (gone) in flags, got %q (%q)", flags, line)
	}
	if !strings.Contains(flags, "*") {
		t.Errorf("expected * (ircop) in flags, got %q (%q)", flags, line)
	}
}

func TestNames_HidesSecretChannelMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #hidden\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("MODE #hidden +s\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "MODE") && strings.Contains(line, "+s")) {
			break
		}
	}

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cBob.Write([]byte("NAMES #hidden\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw353 := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") {
			saw353 = true
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if saw353 {
		t.Errorf("non-member saw 353 RPL_NAMREPLY for +s channel")
	}
}
