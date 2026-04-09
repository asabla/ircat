package server

import (
	"strings"
	"testing"
	"time"
)

func TestStatsL_ReportsLinkByteCounters(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	_, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// alice on A — promote to oper directly so STATS l is allowed.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srvA.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cAlice.Write([]byte("STATS l\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "211", time.Now().Add(2*time.Second))
	expectNumeric(t, cAlice, rAlice, "219", time.Now().Add(2*time.Second))

	parts := strings.Fields(line)
	// Format: ":server 211 alice node-b 0 <sent_msgs> <sent_kb>
	//          <recv_msgs> <recv_kb> <time_open>"
	if len(parts) < 9 {
		t.Fatalf("malformed 211: %q", line)
	}
	// parts[3] is the linkname (node-b), parts[4] sendq,
	// parts[5] sent msgs.
	if parts[3] != "node-b" {
		t.Errorf("link name = %q, want node-b", parts[3])
	}
	// The burst sent at link-up should have produced a non-zero
	// sent message count by the time STATS l reads.
	if parts[5] == "0" {
		t.Errorf("sent_messages should be > 0 after burst, line: %q", line)
	}
	if parts[7] == "0" {
		t.Errorf("recv_messages should be > 0 after burst, line: %q", line)
	}
}
