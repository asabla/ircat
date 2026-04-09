package server

import (
	"strings"
	"testing"
	"time"
)

func TestUserhost_ResolvesPresentNick(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// bob registers first so alice can look him up.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("USERHOST bob\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "302", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "bob=+") {
		t.Errorf("302 missing bob=+host token: %q", line)
	}
}

func TestUserhost_AwayMarker(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("AWAY :brb\r\n"))
	expectNumeric(t, cBob, rBob, "306", time.Now().Add(2*time.Second))

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("USERHOST bob\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "302", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "bob=-") {
		t.Errorf("302 should mark bob as away with '-': %q", line)
	}
}

func TestUserhost_DropsMissingNicks(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("USERHOST nosuch\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "302", time.Now().Add(2*time.Second))
	if strings.Contains(line, "nosuch") {
		t.Errorf("302 should silently drop missing nicks: %q", line)
	}
}

func TestIson_ReturnsPresentSubset(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// Packed trailing param form.
	cAlice.Write([]byte("ISON :bob nosuch\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "303", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "bob") {
		t.Errorf("303 should include bob: %q", line)
	}
	if strings.Contains(line, "nosuch") {
		t.Errorf("303 should drop nosuch: %q", line)
	}
}

func TestIson_AcceptsIndividualParams(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// Individual middle params (no trailing colon).
	cAlice.Write([]byte("ISON bob alice\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "303", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "bob") || !strings.Contains(line, "alice") {
		t.Errorf("303 should include both nicks: %q", line)
	}
}
