package server

import (
	"strings"
	"testing"
	"time"
)

func TestService_Registration(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("SERVICE chanserv * * 0 0 :Channel registration helper\r\n"))

	// Should get 383 RPL_YOURESERVICE.
	expectNumeric(t, c, r, "383", time.Now().Add(2*time.Second))

	u := srv.world.FindByNick("chanserv")
	if u == nil {
		t.Fatal("service not in world")
	}
	if !u.Service {
		t.Errorf("user.Service flag not set")
	}
	if u.ServiceType != "0" {
		t.Errorf("ServiceType = %q, want 0", u.ServiceType)
	}
}

func TestService_Reregister(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("SERVICE foo * * 0 0 :nope\r\n"))
	expectNumeric(t, c, r, "462", time.Now().Add(2*time.Second))
}

func TestService_NeedMoreParams(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("SERVICE chanserv\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}

func TestService_NickInUse(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice registers normally
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// service tries to grab alice's name
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("SERVICE alice * * 0 0 :collide\r\n"))
	expectNumeric(t, c, r, "433", time.Now().Add(2*time.Second))
}

func TestService_HiddenFromNames(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// service registers and joins #s
	cSvc, rSvc := dialClient(t, addr)
	defer cSvc.Close()
	cSvc.Write([]byte("SERVICE helper * * 0 0 :Helper\r\n"))
	expectNumeric(t, cSvc, rSvc, "383", time.Now().Add(2*time.Second))
	cSvc.Write([]byte("JOIN #svc\r\n"))
	expectNumeric(t, cSvc, rSvc, "366", time.Now().Add(2*time.Second))

	// alice joins
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #svc\r\n"))

	// Drain the NAMES burst and assert "helper" does not appear.
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, " 353 ") {
			body := line[strings.LastIndex(line, ":")+1:]
			if strings.Contains(body, "helper") {
				t.Errorf("353 leaked service nick: %q", line)
			}
		}
		if strings.Contains(line, " 366 ") {
			break
		}
	}
}
