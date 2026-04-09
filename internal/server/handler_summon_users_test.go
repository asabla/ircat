package server

import (
	"testing"
	"time"
)

func TestSummon_RepliesWith445(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("SUMMON bob\r\n"))
	expectNumeric(t, c, r, "445", time.Now().Add(2*time.Second))
}

func TestUsers_RepliesWith446(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("USERS\r\n"))
	expectNumeric(t, c, r, "446", time.Now().Add(2*time.Second))
}
