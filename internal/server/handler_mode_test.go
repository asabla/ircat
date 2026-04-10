package server

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// MODE dispatch edge cases
// ---------------------------------------------------------------------------

func TestMode_NoParams_NeedMoreParams(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("MODE\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestMode_NonexistentChannel_NoSuchChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("MODE #doesnotexist +n\r\n"))
	line := expectNumeric(t, c, r, "403", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "#doesnotexist") {
		t.Errorf("403 should mention channel name: %q", line)
	}
}

func TestMode_UnknownModeChar_472(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #x\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #x +Z\r\n"))
	line := expectNumeric(t, c, r, "472", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "Z") {
		t.Errorf("472 should mention the unknown char: %q", line)
	}
}

// ---------------------------------------------------------------------------
// Batched mode changes
// ---------------------------------------------------------------------------

func TestMode_BatchedOvChanges(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #batch\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #batch\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " JOIN ") && strings.HasPrefix(l, ":bob!")
	})

	cAlice.Write([]byte("MODE #batch +ov bob bob\r\n"))
	// The server may render this as "+ov" or "+o+v" depending on
	// how the flush-direction logic batches the output.
	line, _ := readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #batch ") &&
			strings.Contains(l, "o") && strings.Contains(l, "v") &&
			strings.Contains(l, "bob")
	})
	if !strings.Contains(line, "bob") {
		t.Errorf("batched MODE broadcast missing target nick: %q", line)
	}
}

// ---------------------------------------------------------------------------
// +r server reop -- safe channel only (RFC 2811 section 4.2.5)
// ---------------------------------------------------------------------------

func TestMode_PlusR_RejectedOnNonSafeChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #nosafe\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #nosafe +r\r\n"))
	line := expectNumeric(t, c, r, "472", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "r") {
		t.Errorf("472 should mention 'r': %q", line)
	}
}

// ---------------------------------------------------------------------------
// Voice grant and revoke
// ---------------------------------------------------------------------------

func TestMode_Voice_GrantAndRevoke(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #voice\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #voice\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " JOIN ") && strings.HasPrefix(l, ":bob!")
	})

	cAlice.Write([]byte("MODE #voice +v bob\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #voice ") && strings.Contains(l, "+v") && strings.Contains(l, "bob")
	})
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #voice ") && strings.Contains(l, "+v") && strings.Contains(l, "bob")
	})

	cAlice.Write([]byte("MODE #voice -v bob\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #voice ") && strings.Contains(l, "-v") && strings.Contains(l, "bob")
	})
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #voice ") && strings.Contains(l, "-v") && strings.Contains(l, "bob")
	})
}

// ---------------------------------------------------------------------------
// Boolean channel modes: invite-only, moderated, secret, private
// ---------------------------------------------------------------------------

func TestMode_InviteOnly_SetAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #inv\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #inv +i\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #inv ") && strings.Contains(l, "+i")
	})

	c.Write([]byte("MODE #inv\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "i") {
		t.Errorf("324 should include 'i' after +i: %q", line)
	}
}

func TestMode_Moderated_SetAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #modq\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #modq +m\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #modq ") && strings.Contains(l, "+m")
	})

	c.Write([]byte("MODE #modq\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "m") {
		t.Errorf("324 should include 'm' after +m: %q", line)
	}
}

func TestMode_Secret_SetAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #sec\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #sec +s\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #sec ") && strings.Contains(l, "+s")
	})

	c.Write([]byte("MODE #sec\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "s") {
		t.Errorf("324 should include 's' after +s: %q", line)
	}
}

func TestMode_Private_SetAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #priv2\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #priv2 +p\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #priv2 ") && strings.Contains(l, "+p")
	})

	c.Write([]byte("MODE #priv2\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "p") {
		t.Errorf("324 should include 'p' after +p: %q", line)
	}
}

// ---------------------------------------------------------------------------
// Ban removal
// ---------------------------------------------------------------------------

func TestMode_BanRemoval(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #br\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #br +b evil!*@*\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+b") && strings.Contains(l, "evil!*@*")
	})

	c.Write([]byte("MODE #br -b evil!*@*\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "-b") && strings.Contains(l, "evil!*@*")
	})

	// Ban list should now be empty.
	c.Write([]byte("MODE #br +b\r\n"))
	line := expectNumeric(t, c, r, "368", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "End of") {
		t.Errorf("expected end-of-ban-list after removal: %q", line)
	}
}

// ---------------------------------------------------------------------------
// Quiet list: add, query (728/729), remove
// ---------------------------------------------------------------------------

func TestMode_QuietAddQueryRemove(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #qar\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #qar +q *!*@noisy.example\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+q") && strings.Contains(l, "noisy.example")
	})

	c.Write([]byte("MODE #qar +q\r\n"))
	line := expectNumeric(t, c, r, "728", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "noisy.example") {
		t.Errorf("728 should contain quiet mask: %q", line)
	}
	expectNumeric(t, c, r, "729", time.Now().Add(2*time.Second))

	c.Write([]byte("MODE #qar -q *!*@noisy.example\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "-q") && strings.Contains(l, "noisy.example")
	})

	// Quiet list should now be empty.
	c.Write([]byte("MODE #qar +q\r\n"))
	expectNumeric(t, c, r, "729", time.Now().Add(2*time.Second))
}

// ---------------------------------------------------------------------------
// User mode edge cases
// ---------------------------------------------------------------------------

func TestMode_UserQuery_RPL_UMODEIS(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("MODE alice\r\n"))
	line := expectNumeric(t, c, r, "221", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "+") {
		t.Errorf("221 should contain mode string with '+': %q", line)
	}
}

func TestMode_UserOtherNick_NoSuchNick(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("MODE bob\r\n"))
	line := expectNumeric(t, c, r, "401", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "bob") {
		t.Errorf("401 should mention target nick: %q", line)
	}
}

func TestMode_UserMinusO_DeopSelf(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes = "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("MODE alice\r\n"))
	line := expectNumeric(t, c, r, "221", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "o") {
		t.Errorf("expected +o in 221 before deop: %q", line)
	}

	c.Write([]byte("MODE alice -o\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE alice ")
	})

	c.Write([]byte("MODE alice\r\n"))
	line = expectNumeric(t, c, r, "221", time.Now().Add(2*time.Second))
	if strings.Contains(line, "o") {
		t.Errorf("expected no 'o' in 221 after -o: %q", line)
	}
}

func TestMode_UserPlusS_ServerNotices(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("MODE alice +s\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE alice ") && strings.Contains(l, "s")
	})

	c.Write([]byte("MODE alice\r\n"))
	line := expectNumeric(t, c, r, "221", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "s") {
		t.Errorf("221 should include 's' after +s: %q", line)
	}
}
