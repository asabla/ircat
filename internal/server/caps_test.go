package server

import (
	"strings"
	"testing"
	"time"
)

func TestCAP_LSAdvertisesMessageTags(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()

	c.Write([]byte("CAP LS\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(line, "CAP") && strings.Contains(line, "LS") {
			if !strings.Contains(line, "message-tags") {
				t.Errorf("CAP LS missing message-tags: %q", line)
			}
			return
		}
	}
}

func TestCAP_REQAcksKnownCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()

	c.Write([]byte("CAP LS\r\nCAP REQ :message-tags\r\nCAP END\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " ACK ") && strings.Contains(line, "message-tags") {
			saw = true
		}
		if strings.Contains(line, " 001 ") {
			break
		}
	}
	if !saw {
		t.Errorf("CAP REQ message-tags was not ACKed")
	}
}

func TestCAP_REQNaksUnknownCap(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()

	c.Write([]byte("CAP REQ :nonexistent-cap\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " NAK ") {
			return
		}
	}
}
