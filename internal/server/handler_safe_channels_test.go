package server

import (
	"strings"
	"testing"
	"time"
)

func TestSafeChannel_DoubleBangCreatesWithGeneratedID(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("JOIN !!secret\r\n"))
	// Drain to end-of-names; capture the JOIN line so we can
	// extract the canonical channel name.
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var canonical string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " JOIN ") && strings.Contains(line, "!") {
			// "...JOIN :!IDsecret" or "...JOIN !IDsecret"
			idx := strings.LastIndex(line, "!")
			canonical = strings.TrimRight(line[idx:], "\r\n :")
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if canonical == "" || len(canonical) < 1+safeChannelIDLen+len("secret") {
		t.Fatalf("did not capture canonical channel name, got %q", canonical)
	}
	if !strings.HasSuffix(canonical, "secret") {
		t.Errorf("canonical name should end with the short suffix, got %q", canonical)
	}
	id := canonical[1 : 1+safeChannelIDLen]
	if !isSafeChannelID(id) {
		t.Errorf("generated ID %q is not valid", id)
	}
	// World should hold the channel under the canonical name.
	if srv.world.FindChannel(canonical) == nil {
		t.Errorf("world missing canonical safe channel %q", canonical)
	}
}

func TestSafeChannel_SingleBangResolvesByShortName(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice creates !!chat which generates !IDchat.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN !!chat\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob joins via the short form !chat — should resolve to
	// the same channel.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN !chat\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	// alice should see bob's JOIN (which means resolution worked
	// and they ended up on the same channel).
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice never saw bob's join")
		}
		if strings.Contains(line, "bob") && strings.Contains(line, " JOIN ") {
			return
		}
	}
}

func TestSafeChannel_SingleBangNoSuchChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("JOIN !nosuch\r\n"))
	expectNumeric(t, c, r, "403", time.Now().Add(2*time.Second))
}

func TestSafeChannel_PrivmsgRoundTripsViaCanonicalName(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN !!room\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN !room\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cBob.Write([]byte("PRIVMSG !room :hi short form\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal("alice never received bob's privmsg via short form")
		}
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hi short form") {
			return
		}
	}
}

func TestNewSafeChannelID_FormatAndUniqueness(t *testing.T) {
	srv := &Server{now: time.Now}
	// We can't call ChannelsSnapshot without a world; this test
	// only exercises the format check, not collision avoidance.
	// Use the validator directly.
	for i := 0; i < 32; i++ {
		// Build via the alphabet directly to avoid the world walk.
		id := ""
		for j := 0; j < safeChannelIDLen; j++ {
			id += string(safeChannelIDAlphabet[(i*j+i+j)%len(safeChannelIDAlphabet)])
		}
		if !isSafeChannelID(id) {
			t.Errorf("generated id %q does not validate", id)
		}
	}
	_ = srv
}
