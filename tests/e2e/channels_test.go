package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/tests/e2e/ircclient"
)

func TestE2E_ChannelChat(t *testing.T) {
	addr, teardown := startServer(t)
	defer teardown()

	cAlice, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cAlice.Close()
	if err := cAlice.Register("alice", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	cBob, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cBob.Close()
	if err := cBob.Register("bob", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Both join #test and drain the NAMES burst.
	for _, c := range []*ircclient.Client{cAlice, cBob} {
		if err := c.Send("JOIN #test"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := c.ExpectNumeric("366", time.Now().Add(2*time.Second)); err != nil {
			t.Fatalf("waiting for 366 after join: %v", err)
		}
	}

	// Alice should see bob join.
	if _, _, err := cAlice.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ")
	}); err != nil {
		t.Fatalf("alice waiting for bob join: %v", err)
	}

	// Alice sends a message; bob should receive it.
	if err := cAlice.Send("PRIVMSG #test :hello bob"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cBob.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG #test ") &&
			strings.HasSuffix(line, ":hello bob")
	}); err != nil {
		t.Fatalf("bob waiting for privmsg: %v", err)
	}

	// Alice sets the topic; bob should see the TOPIC broadcast.
	if err := cAlice.Send("TOPIC #test :pull up a chair"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cBob.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") && strings.Contains(line, " TOPIC #test ")
	}); err != nil {
		t.Fatalf("bob waiting for topic: %v", err)
	}

	// Bob renames himself; alice should see NICK broadcast.
	if err := cBob.Send("NICK robert"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cAlice.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") && strings.Contains(line, " NICK ") && strings.Contains(line, "robert")
	}); err != nil {
		t.Fatalf("alice waiting for bob nick change: %v", err)
	}

	// Robert quits; alice should see QUIT broadcast.
	if err := cBob.Send("QUIT :see you"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cAlice.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":robert!") && strings.Contains(line, " QUIT ") && strings.Contains(line, "see you")
	}); err != nil {
		t.Fatalf("alice waiting for robert quit: %v", err)
	}
}

func TestE2E_ChannelModes(t *testing.T) {
	addr, teardown := startServer(t)
	defer teardown()

	cAlice, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cAlice.Close()
	cAlice.Register("alice", time.Now().Add(3*time.Second))
	cAlice.Send("JOIN #test")
	cAlice.ExpectNumeric("366", time.Now().Add(2*time.Second))

	// Set a key.
	cAlice.Send("MODE #test +k secret")
	if _, _, err := cAlice.Expect(time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, " MODE #test ") && strings.Contains(line, "+k") && strings.Contains(line, "secret")
	}); err != nil {
		t.Fatalf("waiting for MODE +k: %v", err)
	}

	// Bob without key -> 475.
	cBob, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cBob.Close()
	cBob.Register("bob", time.Now().Add(3*time.Second))
	cBob.Send("JOIN #test")
	if _, _, err := cBob.ExpectNumeric("475", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("expected 475: %v", err)
	}

	// Bob with key -> 366.
	cBob.Send("JOIN #test secret")
	if _, _, err := cBob.ExpectNumeric("366", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("expected 366 after key join: %v", err)
	}
}
