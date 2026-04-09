package server

import (
	"strings"
	"testing"
	"time"
)

func TestServerTime_AttachedWhenNegotiated(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()

	// Negotiate server-time before registration finishes.
	c.Write([]byte("CAP LS\r\nCAP REQ :server-time\r\nCAP END\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))

	// Drain until welcome and check that lines have @time= prefix.
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "@time=") {
			saw = true
		}
		if strings.Contains(line, " 422 ") || strings.Contains(line, " 376 ") {
			break
		}
	}
	if !saw {
		t.Errorf("expected at least one @time= prefixed line after CAP REQ server-time")
	}
}

func TestServerTime_NotAttachedWithoutCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, "@time=") {
			t.Errorf("server-time tag attached without negotiation: %q", line)
		}
		if strings.Contains(line, " 422 ") {
			break
		}
	}
}

func TestServerTime_AdvertisedInLS(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("CAP LS\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, "CAP") && strings.Contains(line, "LS") {
			if !strings.Contains(line, "server-time") {
				t.Errorf("CAP LS missing server-time: %q", line)
			}
			return
		}
	}
}
