package server

import (
	"strings"
	"testing"
	"time"
)

func TestWho_GlobMaskMatchesByNick(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// Three users so we can prove the glob matches a subset.
	for _, n := range []string{"alice", "alpha", "bob"} {
		c, r := dialClient(t, addr)
		defer c.Close()
		c.Write([]byte("NICK " + n + "\r\nUSER " + n + " 0 * :" + n + "\r\n"))
		expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	}

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK observer\r\nUSER obs 0 * :Obs\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("WHO al*\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	hits := map[string]bool{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 352 ") {
			if strings.Contains(line, "alice") {
				hits["alice"] = true
			}
			if strings.Contains(line, "alpha") {
				hits["alpha"] = true
			}
			if strings.Contains(line, "bob") {
				hits["bob"] = true
			}
		}
		if strings.Contains(line, " 315 ") {
			break
		}
	}
	if !hits["alice"] || !hits["alpha"] {
		t.Errorf("WHO al* missed expected matches: %+v", hits)
	}
	if hits["bob"] {
		t.Errorf("WHO al* should not have matched bob")
	}
}

func TestWho_LiteralNickStillWorks(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c1, r1 := dialClient(t, addr)
	defer c1.Close()
	c1.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c1, r1, "422", time.Now().Add(2*time.Second))

	c2, r2 := dialClient(t, addr)
	defer c2.Close()
	c2.Write([]byte("NICK observer\r\nUSER obs 0 * :Obs\r\n"))
	expectNumeric(t, c2, r2, "422", time.Now().Add(2*time.Second))

	c2.Write([]byte("WHO alice\r\n"))
	line := expectNumeric(t, c2, r2, "352", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "alice") {
		t.Errorf("352 missing alice on literal lookup: %q", line)
	}
}
