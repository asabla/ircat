package server

import (
	"strings"
	"testing"
	"time"
)

func TestSquery_DeliversToService(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// Register a service.
	cSvc, rSvc := dialClient(t, addr)
	defer cSvc.Close()
	cSvc.Write([]byte("SERVICE chanserv * * 0 0 :Channel helper\r\n"))
	expectNumeric(t, cSvc, rSvc, "383", time.Now().Add(2*time.Second))

	// alice queries the service.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("SQUERY chanserv :register #foo\r\n"))

	// chanserv should receive an SQUERY line.
	cSvc.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rSvc.ReadString('\n')
		if err != nil {
			t.Fatal("service never received SQUERY")
		}
		if strings.Contains(line, "SQUERY") && strings.Contains(line, "register #foo") {
			return
		}
	}
}

func TestSquery_NoSuchService(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("SQUERY ghost :hello\r\n"))
	expectNumeric(t, c, r, "408", time.Now().Add(2*time.Second))
}

func TestSquery_RegularUserNotAService(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice is a regular user, NOT a service.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// bob SQUERY alice — should be 408 even though alice exists.
	cBob.Write([]byte("SQUERY alice :hi\r\n"))
	expectNumeric(t, cBob, rBob, "408", time.Now().Add(2*time.Second))
}

func TestServlist_ListsServices(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cSvc, rSvc := dialClient(t, addr)
	defer cSvc.Close()
	cSvc.Write([]byte("SERVICE helper * * 0 0 :Helper Bot\r\n"))
	expectNumeric(t, cSvc, rSvc, "383", time.Now().Add(2*time.Second))

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("SERVLIST\r\n"))
	line := expectNumeric(t, c, r, "234", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "helper") {
		t.Errorf("234 missing helper service: %q", line)
	}
	expectNumeric(t, c, r, "235", time.Now().Add(2*time.Second))
}

func TestServlist_EmptyMaskMatchesNone(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("SERVLIST\r\n"))
	// No services registered: just the 235 terminator.
	expectNumeric(t, c, r, "235", time.Now().Add(2*time.Second))
}
