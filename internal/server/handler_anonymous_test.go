package server

import (
	"strings"
	"testing"
	"time"
)

// joinAndSetAnon brings cli into ch and sets +a as the channel op.
// Returns once the +a echo has been seen.
func joinAndSetAnon(t *testing.T, c writeCloser, r reader, ch string) {
	t.Helper()
	c.Write([]byte("JOIN " + ch + "\r\n"))
	expectChainNumeric(t, c, r, "366")
	c.Write([]byte("MODE " + ch + " +a\r\n"))
	c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("draining +a echo: %v", err)
		}
		if strings.Contains(line, "MODE") && strings.Contains(line, "+a") {
			return
		}
	}
}

// expectChainNumeric is a tiny re-spelling of expectNumeric that
// just dumps everything until the requested code shows up. Used by
// the helper above so the test does not need to thread deadlines.
func expectChainNumeric(t *testing.T, c writeCloser, r reader, code string) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for %s: %v", code, err)
		}
		if strings.Contains(line, " "+code+" ") {
			return
		}
	}
}

// writeCloser / reader are minimal interfaces that match the
// concrete net.Conn / *bufio.Reader pair returned by dialClient.
// They keep the helper signature short.
type writeCloser interface {
	Write([]byte) (int, error)
	SetReadDeadline(time.Time) error
	Close() error
}
type reader interface {
	ReadString(byte) (string, error)
}

func TestAnonymous_PrivmsgRewritesPrefix(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	joinAndSetAnon(t, cAlice, rAlice, "#anon")

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #anon\r\n"))
	expectNumeric(t, cBob, rBob, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("PRIVMSG #anon :hello secretly\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for PRIVMSG: %v", err)
		}
		if strings.Contains(line, "PRIVMSG #anon") && strings.Contains(line, "hello secretly") {
			if strings.Contains(line, "alice") {
				t.Errorf("anonymous channel leaked sender nick: %q", line)
			}
			if !strings.Contains(line, "anonymous") {
				t.Errorf("anonymous channel did not rewrite prefix: %q", line)
			}
			return
		}
	}
}

func TestAnonymous_NamesReturnsAnonymousMember(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	joinAndSetAnon(t, cAlice, rAlice, "#anames")

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("JOIN #anames\r\n"))
	// Drain bob's join until end-of-names — that 353 is generated
	// for the joiner so should already contain "anonymous".
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 353 ") && strings.Contains(line, "#anames") {
			// The 353 has the form ":server 353 <recipient> = #c :nicks".
			// We only care about the trailing list (after the
			// last ':'); the recipient name in the middle is
			// not a leak.
			idx := strings.LastIndex(line, ":")
			body := line[idx+1:]
			if strings.Contains(body, "alice") || strings.Contains(body, "bob") {
				t.Errorf("353 body leaked real nick: %q", line)
			}
			if strings.Contains(body, "anonymous") {
				saw = true
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
	if !saw {
		t.Errorf("353 did not contain 'anonymous'")
	}
}

func TestAnonymous_WhoReturnsAnonymousRow(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	joinAndSetAnon(t, cAlice, rAlice, "#awho")

	cAlice.Write([]byte("WHO #awho\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "352", time.Now().Add(2*time.Second))
	// The 352 form is ":server 352 <recipient> #chan <user> <host>
	// <server> <nick> <flags> :<hopcount> <real>". The recipient
	// nick at index 2 is not a leak; we only care that <user>,
	// <host>, and <nick> do not contain alice's identity.
	parts := strings.Fields(line)
	if len(parts) < 9 {
		t.Fatalf("malformed 352: %q", line)
	}
	for _, idx := range []int{4, 5, 7} { // user, host, nick fields
		if strings.Contains(parts[idx], "alice") {
			t.Errorf("352 leaked alice in field %d: %q", idx, line)
		}
		if !strings.Contains(parts[idx], "anonymous") {
			t.Errorf("352 field %d should be anonymous: %q", idx, parts[idx])
		}
	}
	expectNumeric(t, cAlice, rAlice, "315", time.Now().Add(2*time.Second))
}
