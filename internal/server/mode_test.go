package server

import (
	"strings"
	"testing"
	"time"
)

func TestMode_QueryReturnsCurrentChannelModes(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("JOIN #x\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	c.Write([]byte("MODE #x\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "324"
	})
	if !strings.Contains(line, "+nt") {
		t.Errorf("324 missing +nt: %q", line)
	}
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "329"
	})
}

func TestMode_OpAndDeop(t *testing.T) {
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

	// Alice ops bob.
	cAlice.Write([]byte("MODE #x +o bob\r\n"))
	// Both alice and bob should see the MODE broadcast.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #x ") && strings.Contains(l, "+o") && strings.Contains(l, "bob")
	})
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #x ") && strings.Contains(l, "+o") && strings.Contains(l, "bob")
	})

	// Bob can now set the topic (despite default +t).
	cBob.Write([]byte("TOPIC #x :bob is here\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " TOPIC ")
	})
}

func TestMode_NonOpRejected(t *testing.T) {
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

	cBob.Write([]byte("MODE #x +n\r\n"))
	expectNumeric(t, cBob, rBob, "482", time.Now().Add(2*time.Second))
}

func TestMode_KeyAndJoin(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #x +k secret\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+k") && strings.Contains(l, "secret")
	})

	// Bob without key -> 475.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cBob, rBob, "475", time.Now().Add(2*time.Second))

	// Bob with key -> success.
	cBob.Write([]byte("JOIN #x secret\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
}

func TestMode_LimitAndJoin(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #x +l 1\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+l") && strings.Contains(l, "1")
	})

	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cBob, rBob, "471", time.Now().Add(2*time.Second))
}

func TestMode_BanAndJoin(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	// Ban every bob.
	cAlice.Write([]byte("MODE #x +b bob!*@*\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+b") && strings.Contains(l, "bob!*@*")
	})

	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cBob, rBob, "474", time.Now().Add(2*time.Second))

	// Ban list query.
	cAlice.Write([]byte("MODE #x +b\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "367" && strings.Contains(l, "bob!*@*")
	})
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "368"
	})
}

func TestMode_UserSelf(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("MODE alice +iw\r\n"))
	readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE alice ") && strings.Contains(l, "i") && strings.Contains(l, "w")
	})

	// MODE alice +o is silently dropped (cannot self-promote).
	c.Write([]byte("MODE alice +o\r\n"))
	line, _ := readUntil(t, c, r, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE alice ")
	})
	if strings.Contains(line, "o") {
		t.Errorf("user managed to set +o: %q", line)
	}
}
