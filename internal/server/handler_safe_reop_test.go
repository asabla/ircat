package server

import (
	"strings"
	"testing"
	"time"
)

// captureCanonicalSafe brings c into a freshly-created safe channel
// from the !!short form and returns the canonical "!IDshort" name.
func captureCanonicalSafe(t *testing.T, c writeCloser, r reader, short string) string {
	t.Helper()
	c.Write([]byte("JOIN !!" + short + "\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var name string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for canonical name: %v", err)
		}
		if strings.Contains(line, " JOIN ") && strings.Contains(line, "!") {
			idx := strings.LastIndex(line, "!")
			name = strings.TrimRight(line[idx:], "\r\n :")
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if name == "" {
		t.Fatal("did not capture canonical safe channel name")
	}
	return name
}

func TestSafeReop_RModeRejectedOnNonSafeChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("JOIN #plain\r\n"))
	expectNumeric(t, c, r, "366", time.Now().Add(2*time.Second))
	c.Write([]byte("MODE #plain +r\r\n"))
	// Should get 472 ERR_UNKNOWNMODE because +r is rejected on
	// non-safe channels.
	expectNumeric(t, c, r, "472", time.Now().Add(2*time.Second))
}

func TestSafeReop_EmptyChannelSurvivesAndAutoOpsRejoiner(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice creates !!persist and sets +r.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	canonical := captureCanonicalSafe(t, cAlice, rAlice, "persist")

	cAlice.Write([]byte("MODE " + canonical + " +r\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "MODE") && strings.Contains(line, "+r")) {
			break
		}
	}

	// alice parts. The channel should NOT be dropped because of +r.
	cAlice.Write([]byte("PART " + canonical + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || strings.Contains(line, "PART") {
			break
		}
	}
	if srv.world.FindChannel(canonical) == nil {
		t.Fatalf("safe channel %q with +r was dropped after going empty", canonical)
	}

	// bob joins via the short form. Should be auto-opped via reop.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN !persist\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	ch := srv.world.FindChannel(canonical)
	bob := srv.world.FindByNick("bob")
	mem := ch.Membership(bob.ID)
	if !mem.IsOp() {
		t.Errorf("server-reop joiner should be opped, got membership %d", mem)
	}
	if mem.IsCreator() {
		t.Errorf("server-reop joiner should NOT be granted creator status")
	}
}

func TestSafeReop_WithoutRChannelDropsOnEmpty(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	canonical := captureCanonicalSafe(t, cAlice, rAlice, "ephemeral")

	// alice parts; +r is unset, so the channel should be dropped.
	cAlice.Write([]byte("PART " + canonical + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || strings.Contains(line, "PART") {
			break
		}
	}
	if srv.world.FindChannel(canonical) != nil {
		t.Errorf("safe channel without +r should drop on empty")
	}
}
