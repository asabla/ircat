package server

import (
	"strings"
	"testing"
	"time"
)

// TestWhois_AcrossFederation verifies that a WHOIS for a user on the
// remote node returns 311 + 312 with the *remote* server name in
// the 312 line, not the querier's local server name.
func TestWhois_AcrossFederation(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// alice on node-a
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// bob on node-b
	cBob, rBob := dialClient(t, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// Wait for cross-node visibility.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvA.world.FindByNick("bob") != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srvA.world.FindByNick("bob") == nil {
		t.Fatal("bob never propagated to node-a")
	}

	// alice asks WHOIS bob — bob is remote on node-a.
	cAlice.Write([]byte("WHOIS bob\r\n"))
	line311 := expectNumeric(t, cAlice, rAlice, "311", time.Now().Add(2*time.Second))
	if !strings.Contains(line311, "bob") {
		t.Errorf("311 missing nick: %q", line311)
	}
	line312 := expectNumeric(t, cAlice, rAlice, "312", time.Now().Add(2*time.Second))
	// Strip the leading ":node-a 312 alice " prefix (which always
	// contains the local server name) and assert against the
	// remaining params.
	parts := strings.SplitN(line312, " ", 5)
	if len(parts) < 5 {
		t.Fatalf("malformed 312: %q", line312)
	}
	body := parts[4]
	if !strings.Contains(body, "node-b") {
		t.Errorf("312 body should report bob's home server (node-b), got %q", body)
	}
	expectNumeric(t, cAlice, rAlice, "317", time.Now().Add(2*time.Second))
	expectNumeric(t, cAlice, rAlice, "318", time.Now().Add(2*time.Second))
}
