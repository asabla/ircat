package server

import (
	"strings"
	"testing"
	"time"
)

func TestExtendedJoin_WithCap_ShowsAccountAndRealname(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice negotiates extended-join and joins a channel.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP LS\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cAlice.Write([]byte("CAP REQ :extended-join\r\n"))
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice Example\r\n"))
	cAlice.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("JOIN #ext\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// Set bob's Account before he joins.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			u.Account = "bobacct"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// bob joins — alice (with extended-join) should see the extended form.
	cBob.Write([]byte("JOIN #ext\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ") {
			// Extended form: ":bob!user@host JOIN #ext bobacct :Bob"
			if !strings.Contains(line, "bobacct") {
				t.Errorf("expected extended-join with account name: %q", line)
			}
			return true
		}
		return false
	})
	_ = rBob
}

func TestExtendedJoin_WithoutCap_NormalForm(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice does NOT negotiate extended-join.
	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()

	cAlice.Write([]byte("JOIN #ext2\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			u.Account = "bobacct"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// bob joins — alice (without cap) should see normal form.
	cBob.Write([]byte("JOIN #ext2\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ") {
			// Normal form should NOT have the account name.
			if strings.Contains(line, "bobacct") {
				t.Errorf("recipient without extended-join should not see account: %q", line)
			}
			return true
		}
		return false
	})
	_ = rBob
	_ = srv
}

func TestExtendedJoin_NotLoggedIn_ShowsStar(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice negotiates extended-join.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP LS\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cAlice.Write([]byte("CAP REQ :extended-join\r\n"))
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	cAlice.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("JOIN #ext3\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// bob joins without being logged in.
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cBob.Write([]byte("JOIN #ext3\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(line string) bool {
		if strings.HasPrefix(line, ":bob!") && strings.Contains(line, " JOIN ") {
			// Should show "*" for non-logged-in account.
			if !strings.Contains(line, " * ") && !strings.Contains(line, " *\r") {
				// Check trailing form too
				parts := strings.Fields(line)
				found := false
				for _, p := range parts {
					if p == "*" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected '*' for non-logged-in user in extended-join: %q", line)
				}
			}
			return true
		}
		return false
	})
	_ = rBob
}
