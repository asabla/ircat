package server

import (
	"strings"
	"testing"
	"time"
)

func TestNames_OnExistingChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #test\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("NAMES #test\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "353"
	})
	if !strings.Contains(line, "@alice") {
		t.Errorf("353 missing @alice: %q", line)
	}
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
}

func TestNames_OnUnknownChannel(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("NAMES #ghost\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366" && strings.Contains(l, "#ghost")
	})
}

func TestList_EmptyAndPopulated(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	// Empty list: just LISTSTART + LISTEND.
	c.Write([]byte("LIST\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "321"
	})
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "323"
	})

	// Join a channel and LIST again.
	c.Write([]byte("JOIN #foo\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	c.Write([]byte("LIST\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "322" && strings.Contains(l, "#foo")
	})
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "323"
	})
}

func TestWho_ChannelMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	// Drain bob join echo on alice.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " JOIN ") && strings.HasPrefix(l, ":bob!")
	})

	cAlice.Write([]byte("WHO #x\r\n"))
	sawAlice := false
	sawBob := false
	for !(sawAlice && sawBob) {
		line, _ := readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
			return extractNumeric(l) == "352"
		})
		if strings.Contains(line, " alice ") {
			sawAlice = true
		}
		if strings.Contains(line, " bob ") {
			sawBob = true
		}
	}
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "315"
	})
}

func TestWhois_KnownUser(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()
	// bob joins a channel so the WHOIS includes RPL_WHOISCHANNELS.
	cBob.Write([]byte("JOIN #x\r\n"))

	cAlice.Write([]byte("WHOIS bob\r\n"))
	deadline := time.Now().Add(2 * time.Second)
	wantCodes := []string{"311", "312", "318"}
	for _, code := range wantCodes {
		readUntil(t, cAlice, rAlice, deadline, func(l string) bool {
			return extractNumeric(l) == code
		})
	}
}

func TestWhois_UnknownUser(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("WHOIS ghost\r\n"))
	deadline := time.Now().Add(2 * time.Second)
	readUntil(t, c, r, deadline, func(l string) bool {
		return extractNumeric(l) == "401"
	})
	readUntil(t, c, r, deadline, func(l string) bool {
		return extractNumeric(l) == "318"
	})
}
