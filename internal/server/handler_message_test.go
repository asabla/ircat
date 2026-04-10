package server

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Moderated channel (+m) -- RFC 2812 section 3.3.1
// Only voiced (+v) or opped (+o) members may speak. Regular members
// receive 404 ERR_CANNOTSENDTOCHAN.
// ---------------------------------------------------------------------------

func TestPrivmsg_ModeratedChannel_RegularUserBlocked(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #mod\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #mod\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	// alice sets +m on #mod.
	cAlice.Write([]byte("MODE #mod +m\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+m")
	})

	// bob (regular member) tries to speak -- should get 404.
	cBob.Write([]byte("PRIVMSG #mod :i am muted\r\n"))
	expectNumeric(t, cBob, rBob, "404", time.Now().Add(2*time.Second))
}

func TestPrivmsg_ModeratedChannel_VoicedUserCanSpeak(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #modv\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #modv\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("MODE #modv +m\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+m")
	})
	cAlice.Write([]byte("MODE #modv +v bob\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+v")
	})

	cBob.Write([]byte("PRIVMSG #modv :voiced speaking\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, "PRIVMSG #modv") &&
			strings.Contains(line, "voiced speaking")
	})
}

func TestPrivmsg_ModeratedChannel_OpCanSpeak(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #modo\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #modo\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("MODE #modo +m\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+m")
	})

	// alice (op / channel creator) speaks -- bob should receive it.
	cAlice.Write([]byte("PRIVMSG #modo :op speaking\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, "PRIVMSG #modo") &&
			strings.Contains(line, "op speaking")
	})
}

// ---------------------------------------------------------------------------
// Quiet (+q) with op override -- charybdis/inspircd behaviour.
// ---------------------------------------------------------------------------

func TestPrivmsg_QuietChannel_OpOverridesQuiet(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #qo\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #qo\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

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

	cAlice.Write([]byte("MODE #qo +q " + bobMask + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil || strings.Contains(line, "+q") {
			break
		}
	}
	cAlice.Write([]byte("MODE #qo +o bob\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+o") && strings.Contains(l, "bob")
	})

	// bob (opped despite quiet) should be able to speak.
	cBob.Write([]byte("PRIVMSG #qo :op beats quiet\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, "PRIVMSG") && strings.Contains(line, "op beats quiet")
	})
}

// ---------------------------------------------------------------------------
// PRIVMSG / NOTICE before registration -- RFC 2812 section 3.3
// ---------------------------------------------------------------------------

func TestPrivmsg_BeforeRegistration_Gets451(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()

	c.Write([]byte("PRIVMSG someone :early bird\r\n"))
	expectNumeric(t, c, r, "451", time.Now().Add(2*time.Second))
}

func TestNotice_BeforeRegistration_SilentDrop(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()

	c.Write([]byte("NOTICE someone :early bird\r\n"))

	// Send a PING so we can detect when the server has processed
	// the NOTICE -- if we see PONG without any prior reply, the
	// NOTICE was silently dropped.
	c.Write([]byte("PING :probe\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.Contains(line, " 451 ") {
			t.Errorf("NOTICE before registration generated 451: %q", line)
		}
		return strings.Contains(line, "PONG") && strings.Contains(line, "probe")
	})
}

// ---------------------------------------------------------------------------
// NOTICE to moderated channel -- same rules as PRIVMSG but no error reply.
// ---------------------------------------------------------------------------

func TestNotice_ModeratedChannel_SilentDrop(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #modn\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #modn\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("MODE #modn +m\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+m")
	})

	// bob sends NOTICE to the moderated channel -- should be silently
	// dropped (no 404 reply for NOTICE per RFC 2812 section 3.3.2).
	cBob.Write([]byte("NOTICE #modn :silent\r\n"))

	cBob.Write([]byte("PING :modn-probe\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		if extractNumeric(line) == "404" {
			t.Errorf("NOTICE to moderated channel produced 404: %q", line)
		}
		return strings.Contains(line, "PONG") && strings.Contains(line, "modn-probe")
	})

	// alice should NOT see the notice.
	cAlice.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "NOTICE") && strings.Contains(line, "silent") {
			t.Errorf("moderated channel leaked NOTICE from regular user: %q", line)
		}
	}
}

func TestNotice_ModeratedChannel_VoicedUserDelivered(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #modnv\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #modnv\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("MODE #modnv +m\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+m")
	})
	cAlice.Write([]byte("MODE #modnv +v bob\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+v")
	})

	cBob.Write([]byte("NOTICE #modnv :voiced notice\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, "NOTICE #modnv") &&
			strings.Contains(line, "voiced notice")
	})
}
