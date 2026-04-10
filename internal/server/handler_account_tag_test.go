package server

import (
	"strings"
	"testing"
	"time"
)

func TestAccountTag_LoggedInUserAttachesTag(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()

	// bob negotiates account-tag.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP LS\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cBob.Write([]byte("CAP REQ :account-tag\r\n"))
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	cBob.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))

	// Set alice's Account directly via the world.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Account = "alice"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// alice sends a PRIVMSG to bob — bob should see @account=alice.
	cAlice.Write([]byte("PRIVMSG bob :hello tagged\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello tagged") {
			if !strings.Contains(line, "@account=alice") {
				t.Errorf("expected @account=alice tag: %q", line)
			}
			return true
		}
		return false
	})
	_ = rAlice // keep alice alive
}

func TestAccountTag_NotLoggedIn_NoTag(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, _ := register(t, addr, "alice")
	defer cAlice.Close()

	// bob negotiates account-tag.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("CAP LS\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cBob.Write([]byte("CAP REQ :account-tag\r\n"))
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	cBob.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cBob, rBob, "001", time.Now().Add(2*time.Second))

	// alice (not logged in) sends to bob — no @account tag.
	cAlice.Write([]byte("PRIVMSG bob :hello untagged\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello untagged") {
			if strings.Contains(line, "@account") {
				t.Errorf("should not have @account tag for non-logged-in user: %q", line)
			}
			return true
		}
		return false
	})
}

func TestAccountTag_RecipientWithoutCap_NoTag(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	// bob does NOT negotiate account-tag.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Account = "alice"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cAlice.Write([]byte("PRIVMSG bob :hello plain\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.Contains(line, "PRIVMSG") && strings.Contains(line, "hello plain") {
			if strings.Contains(line, "@account") {
				t.Errorf("recipient without cap should not see @account: %q", line)
			}
			return true
		}
		return false
	})
	_ = rAlice
}
