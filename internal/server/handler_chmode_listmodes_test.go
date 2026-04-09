package server

import (
	"strings"
	"testing"
	"time"
)

func TestChannelMode_ExceptionListAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// Set a +e exception, then query the list back.
	cAlice.Write([]byte("MODE #x +e *!*@safe.example\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "MODE") && strings.Contains(line, "+e") {
			break
		}
	}

	cAlice.Write([]byte("MODE #x +e\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "348", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "safe.example") {
		t.Errorf("348 missing exception mask: %q", line)
	}
	expectNumeric(t, cAlice, rAlice, "349", time.Now().Add(2*time.Second))
}

func TestChannelMode_InvexListAndQuery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #y\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	cAlice.Write([]byte("MODE #y +I *!*@trusted.example\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "MODE") && strings.Contains(line, "+I") {
			break
		}
	}

	cAlice.Write([]byte("MODE #y +I\r\n"))
	line := expectNumeric(t, cAlice, rAlice, "346", time.Now().Add(2*time.Second))
	if !strings.Contains(line, "trusted.example") {
		t.Errorf("346 missing invex mask: %q", line)
	}
	expectNumeric(t, cAlice, rAlice, "347", time.Now().Add(2*time.Second))
}

func TestChannelMode_ExceptionLetsBannedHostJoin(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice creates #x and bans bob's hostmask but adds an exception.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #x\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))

	// Pre-register bob in the world so we know his hostmask, then
	// add ban+exception both matching it.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	var bobMask string
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			bobMask = u.Hostmask()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bobMask == "" {
		t.Fatal("bob never registered")
	}

	cAlice.Write([]byte("MODE #x +b " + bobMask + "\r\n"))
	cAlice.Write([]byte("MODE #x +e " + bobMask + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "+e") {
			break
		}
	}

	// bob joins — exception overrides the ban so he should land
	// in the channel and see his own JOIN echoed.
	cBob.Write([]byte("JOIN #x\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	joined := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 366 ") {
			joined = true
			break
		}
		if strings.Contains(line, " 474 ") {
			t.Fatalf("bob was banned despite +e: %q", line)
		}
	}
	if !joined {
		t.Errorf("bob never reached end-of-names")
	}
}

func TestChannelMode_InvexBypassesInviteOnly(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #z\r\n"))
	expectNumeric(t, cAlice, rAlice, "366", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("MODE #z +i\r\n"))

	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	var bobMask string
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("bob"); u != nil {
			bobMask = u.Hostmask()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bobMask == "" {
		t.Fatal("bob never registered")
	}

	cAlice.Write([]byte("MODE #z +I " + bobMask + "\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "+I") {
			break
		}
	}

	cBob.Write([]byte("JOIN #z\r\n"))
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	joined := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " 366 ") {
			joined = true
			break
		}
		if strings.Contains(line, " 473 ") {
			t.Fatalf("bob was rejected despite +I match: %q", line)
		}
	}
	if !joined {
		t.Errorf("bob never reached end-of-names")
	}
}
