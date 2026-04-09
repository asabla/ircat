package server

import (
	"strings"
	"testing"
	"time"
)

// drainTo404 reads ahead until the conn returns the
// requested numeric. Used by every handler_info test below.
func waitFor(t *testing.T, addr string, sendLines []string, wantCode string) string {
	t.Helper()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	for _, line := range sendLines {
		c.Write([]byte(line + "\r\n"))
	}
	return expectNumeric(t, c, r, wantCode, time.Now().Add(2*time.Second))
}

func TestVersion_RepliesWith351(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	line := waitFor(t, addr, []string{"VERSION"}, "351")
	if !strings.Contains(line, "ircat") {
		t.Errorf("351 missing software name: %q", line)
	}
}

func TestTime_RepliesWith391(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	line := waitFor(t, addr, []string{"TIME"}, "391")
	if !strings.Contains(line, "irc.test") {
		t.Errorf("391 missing server name: %q", line)
	}
}

func TestAdmin_EmitsAllFourReplies(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("ADMIN\r\n"))
	// Read sequentially in the order the server emits them so the
	// expectNumeric helper does not skip past lines while hunting
	// for an out-of-order code.
	for _, code := range []string{"256", "257", "258", "259"} {
		expectNumeric(t, c, r, code, time.Now().Add(2*time.Second))
	}
}

func TestInfo_EmitsBlockEndedBy374(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("INFO\r\n"))
	// Drain at least one 371 then a 374.
	expectNumeric(t, c, r, "371", time.Now().Add(2*time.Second))
	expectNumeric(t, c, r, "374", time.Now().Add(2*time.Second))
}

func TestMotdCommand_RepliesWith422OnEmptyMOTD(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	line := waitFor(t, addr, []string{"MOTD"}, "422")
	if !strings.Contains(line, "MOTD") {
		t.Errorf("422 missing MOTD label: %q", line)
	}
}

func TestLusers_EmitsAllFiveCounters(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("LUSERS\r\n"))
	for _, code := range []string{"251", "252", "253", "254", "255"} {
		expectNumeric(t, c, r, code, time.Now().Add(2*time.Second))
	}
}
