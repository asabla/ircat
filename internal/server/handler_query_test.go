package server

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WHOIS edge cases not covered by query_test.go
// ---------------------------------------------------------------------------

func TestWhois_NoArgs_NoNicknameGiven(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("WHOIS\r\n"))
	expectNumeric(t, c, r, "431", time.Now().Add(2*time.Second))
}

func TestWhois_ShowsOperator313(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	// Grant bob +o via world state.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			u.Modes = "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cAlice.Write([]byte("WHOIS bob\r\n"))
	dl := time.Now().Add(2 * time.Second)
	saw313 := false
	for {
		line, _ := readUntil(t, cAlice, rAlice, dl, func(l string) bool {
			code := extractNumeric(l)
			return code == "313" || code == "318"
		})
		code := extractNumeric(line)
		if code == "313" {
			saw313 = true
			if !strings.Contains(line, "IRC operator") {
				t.Errorf("313 should mention operator: %q", line)
			}
		}
		if code == "318" {
			break
		}
	}
	if !saw313 {
		t.Errorf("WHOIS for +o user should include 313 RPL_WHOISOPERATOR")
	}
}

func TestWhois_ShowsIdle317(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("WHOIS bob\r\n"))
	dl := time.Now().Add(2 * time.Second)
	saw317 := false
	for {
		line, _ := readUntil(t, cAlice, rAlice, dl, func(l string) bool {
			code := extractNumeric(l)
			return code == "317" || code == "318"
		})
		code := extractNumeric(line)
		if code == "317" {
			saw317 = true
			if !strings.Contains(line, "seconds idle") {
				t.Errorf("317 should contain idle info: %q", line)
			}
		}
		if code == "318" {
			break
		}
	}
	if !saw317 {
		t.Errorf("WHOIS should include 317 RPL_WHOISIDLE")
	}
}

func TestWhois_MultiTarget(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	// WHOIS with comma-separated targets.
	cAlice.Write([]byte("WHOIS bob,alice\r\n"))
	dl := time.Now().Add(2 * time.Second)
	endCount := 0
	for endCount < 2 {
		line, _ := readUntil(t, cAlice, rAlice, dl, func(l string) bool {
			return extractNumeric(l) == "318"
		})
		if extractNumeric(line) == "318" {
			endCount++
		}
	}
	// We got two 318 terminators -- one for each target.
}

func TestWhois_ServerParam_TwoArgForm(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	// Two-param form: WHOIS <server> <nick>
	cAlice.Write([]byte("WHOIS irc.test bob\r\n"))
	dl := time.Now().Add(2 * time.Second)
	readUntil(t, cAlice, rAlice, dl, func(l string) bool {
		return extractNumeric(l) == "311" && strings.Contains(l, "bob")
	})
	readUntil(t, cAlice, rAlice, dl, func(l string) bool {
		return extractNumeric(l) == "318"
	})
}

func TestWhois_ChannelMembership319(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	// bob joins a channel and is opped (first joiner).
	cBob.Write([]byte("JOIN #show\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	cAlice.Write([]byte("WHOIS bob\r\n"))
	dl := time.Now().Add(2 * time.Second)
	saw319 := false
	for {
		line, _ := readUntil(t, cAlice, rAlice, dl, func(l string) bool {
			code := extractNumeric(l)
			return code == "319" || code == "318"
		})
		code := extractNumeric(line)
		if code == "319" {
			saw319 = true
			if !strings.Contains(line, "#show") {
				t.Errorf("319 should mention channel: %q", line)
			}
			if !strings.Contains(line, "@") {
				t.Errorf("319 should show op prefix for first joiner: %q", line)
			}
		}
		if code == "318" {
			break
		}
	}
	if !saw319 {
		t.Errorf("WHOIS should include 319 RPL_WHOISCHANNELS when user is in a channel")
	}
}

// ---------------------------------------------------------------------------
// NAMES edge cases not covered by query_test.go
// ---------------------------------------------------------------------------

func TestNames_SecretChannel_NonMemberSeesEmpty(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #secret\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #secret +s\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+s")
	})

	// bob (non-member) asks for NAMES -- should get only 366, no 353.
	cBob.Write([]byte("NAMES #secret\r\n"))
	line, _ := readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366" || extractNumeric(l) == "353"
	})
	if extractNumeric(line) == "353" {
		t.Errorf("non-member should not see 353 for +s channel: %q", line)
	}
}

func TestNames_PrivateChannel_NonMemberSeesEmpty(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #priv\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #priv +p\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+p")
	})

	cBob.Write([]byte("NAMES #priv\r\n"))
	line, _ := readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366" || extractNumeric(l) == "353"
	})
	if extractNumeric(line) == "353" {
		t.Errorf("non-member should not see 353 for +p channel: %q", line)
	}
}

func TestNames_NoArgs_BareEndOfNames(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("NAMES\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	if !strings.Contains(line, "*") {
		t.Errorf("bare NAMES should return 366 with '*': %q", line)
	}
}

// ---------------------------------------------------------------------------
// LIST visibility -- +s and +p channels hidden from non-members
// ---------------------------------------------------------------------------

func TestList_SecretChannelHidden(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #hidden\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #hidden +s\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+s")
	})

	// bob (not a member) does LIST -- should NOT see #hidden.
	cBob.Write([]byte("LIST\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "321"
	})
	// Read until LISTEND, checking no 322 mentions #hidden.
	for {
		line, _ := readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
			return extractNumeric(l) == "322" || extractNumeric(l) == "323"
		})
		code := extractNumeric(line)
		if code == "322" && strings.Contains(line, "#hidden") {
			t.Errorf("non-member should not see +s channel in LIST: %q", line)
		}
		if code == "323" {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// WHO edge cases
// ---------------------------------------------------------------------------

func TestWho_AnonymousChannel_SyntheticRow(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()

	cAlice.Write([]byte("JOIN #anon\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #anon +a\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+a")
	})

	cAlice.Write([]byte("WHO #anon\r\n"))
	line, _ := readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "352"
	})
	if !strings.Contains(line, "anonymous") {
		t.Errorf("WHO on +a channel should return synthetic anonymous row: %q", line)
	}
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "315"
	})
}

func TestWho_NoMask_DefaultStar(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("WHO\r\n"))
	line := expectNumeric(t, c, r, "315", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "*") {
		t.Errorf("WHO with no mask should use '*' in 315: %q", line)
	}
}
