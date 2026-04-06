package server

import (
	"strings"
	"testing"
	"time"
)

func TestKick_OpRemovesMember(t *testing.T) {
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
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("KICK #x bob :be nice\r\n"))
	// Both alice and bob should see the KICK broadcast.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":alice!") && strings.Contains(l, " KICK #x bob ") && strings.Contains(l, "be nice")
	})
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":alice!") && strings.Contains(l, " KICK #x bob ") && strings.Contains(l, "be nice")
	})

	// After the kick, bob should be able to PRIVMSG and see 404
	// (default +n) because he is no longer in the channel.
	cBob.Write([]byte("PRIVMSG #x :still here?\r\n"))
	expectNumeric(t, cBob, rBob, "404", time.Now().Add(2*time.Second))
}

func TestKick_NonOpRejected(t *testing.T) {
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

	cBob.Write([]byte("KICK #x alice :nope\r\n"))
	expectNumeric(t, cBob, rBob, "482", time.Now().Add(2*time.Second))
}

func TestKick_TargetNotInChannel(t *testing.T) {
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
	// Make sure bob is registered with the world for FindByNick.
	_ = cBob
	_ = rBob

	cAlice.Write([]byte("KICK #x bob\r\n"))
	expectNumeric(t, cAlice, rAlice, "441", time.Now().Add(2*time.Second))
}

func TestInvite_BypassesInviteOnly(t *testing.T) {
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
	// Make the channel invite-only.
	cAlice.Write([]byte("MODE #x +i\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #x ") && strings.Contains(l, "+i")
	})

	// Bob without invite -> 473.
	cBob.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cBob, rBob, "473", time.Now().Add(2*time.Second))

	// Alice invites bob.
	cAlice.Write([]byte("INVITE bob #x\r\n"))
	// Alice should see RPL_INVITING (341).
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "341"
	})
	// Bob should see the INVITE message.
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":alice!") && strings.Contains(l, " INVITE bob ")
	})

	// Bob can now JOIN.
	cBob.Write([]byte("JOIN #x\r\n"))
	if _, _, err := func() (string, []string, error) {
		s := expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))
		return s, nil, nil
	}(); err != nil {
		t.Fatal(err)
	}

	// A second JOIN should fail again because the invite is one-shot.
	cBob.Write([]byte("PART #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " PART ")
	})
	cBob.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cBob, rBob, "473", time.Now().Add(2*time.Second))
}

func TestInvite_NonOpUnderInviteOnly(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cCarol, _ := register(t, addr, "carol")
	defer cCarol.Close()

	cAlice.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #x +i\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, "+i")
	})
	// alice invites bob so bob can join. Wait for the INVITE to
	// actually land on bob before sending JOIN, otherwise the JOIN
	// can race ahead of the server-side invite record and get 473.
	cAlice.Write([]byte("INVITE bob #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":alice!") && strings.Contains(l, " INVITE bob ")
	})
	cBob.Write([]byte("JOIN #x\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// bob (non-op) tries to invite carol -> 482.
	cBob.Write([]byte("INVITE carol #x\r\n"))
	expectNumeric(t, cBob, rBob, "482", time.Now().Add(2*time.Second))
}
