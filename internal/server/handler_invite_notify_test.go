package server

import (
	"strings"
	"testing"
	"time"
)

// drainUntil drains lines from r until cond returns true or the
// deadline expires. Returns the matched line or "" on timeout.
func drainUntilLine(t *testing.T, c interface {
	SetReadDeadline(time.Time) error
}, r interface {
	ReadString(byte) (string, error)
}, cond func(string) bool, deadline time.Time) string {
	t.Helper()
	c.SetReadDeadline(deadline)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return ""
		}
		if cond(line) {
			return line
		}
	}
}

func TestInviteNotify_DeliversToCapableOperators(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice creates the channel and is auto-opped.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #invn\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob joins, alice ops bob, bob negotiates invite-notify.
	// To get bob into the channel WITH the cap we have him
	// negotiate before registration, then join.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP REQ :invite-notify\r\nCAP END\r\nNICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #invn\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("MODE #invn +o bob\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || (strings.Contains(line, "MODE") && strings.Contains(line, "+o")) {
			break
		}
	}

	// charlie exists so alice has someone to invite.
	cChar, rChar := dialClient(t, addr)
	defer cChar.Close()
	cChar.Write([]byte("NICK charlie\r\nUSER charlie 0 * :Charlie\r\n"))
	expectNumeric(t, cChar, rChar, "422", time.Now().Add(2*time.Second))

	// Drain anything queued on bob (the +o echo) so we start clean.
	cBob.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "+o") {
			break
		}
	}

	// alice INVITEs charlie. bob (op + cap) should see an
	// INVITE notification.
	cAlice.Write([]byte("INVITE charlie #invn\r\n"))
	line := drainUntilLine(t, cBob, rBob, func(l string) bool {
		return strings.Contains(l, "alice") &&
			strings.Contains(l, " INVITE ") &&
			strings.Contains(l, "charlie") &&
			strings.Contains(l, "#invn")
	}, time.Now().Add(2*time.Second))
	if line == "" {
		t.Errorf("invite-notify did not reach the opped capable peer")
	}
}

func TestInviteNotify_NotSentToNonOps(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #ino\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// bob with the cap but NO op
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP REQ :invite-notify\r\nCAP END\r\nNICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #ino\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cChar, rChar := dialClient(t, addr)
	defer cChar.Close()
	cChar.Write([]byte("NICK charlie\r\nUSER charlie 0 * :Charlie\r\n"))
	expectNumeric(t, cChar, rChar, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("INVITE charlie #ino\r\n"))

	// bob (cap, no op) must NOT receive an INVITE notification.
	cBob.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " INVITE ") {
			t.Errorf("non-op got invite-notify: %q", line)
		}
	}
}

func TestInviteNotify_TargetStillReceivesInvite(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #it\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("INVITE bob #it\r\n"))

	// bob (the target) should still see the regular INVITE
	// regardless of any cap state.
	line := drainUntilLine(t, cBob, rBob, func(l string) bool {
		return strings.Contains(l, " INVITE ") && strings.Contains(l, "#it")
	}, time.Now().Add(2*time.Second))
	if line == "" {
		t.Errorf("target did not receive INVITE")
	}
}
