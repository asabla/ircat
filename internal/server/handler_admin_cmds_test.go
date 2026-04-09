package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeReloader struct {
	calls atomic.Int32
	err   error
}

func (f *fakeReloader) Reload(_ context.Context) error {
	f.calls.Add(1)
	return f.err
}

func TestRehash_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("REHASH\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestRehash_TriggersReloader(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	rl := &fakeReloader{}
	WithReloader(rl)(srv)

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("REHASH\r\n"))
	expectNumeric(t, c, r, "382", time.Now().Add(2*time.Second))

	// Reload runs in a goroutine; give it a moment.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rl.calls.Load() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reloader was never called")
}

func TestDie_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	c.Write([]byte("DIE\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestDie_FiresShutdownHook(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	var fired atomic.Int32
	var gotReason string
	WithShutdown(func(reason string) {
		gotReason = reason
		fired.Add(1)
	})(srv)

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("DIE :maintenance\r\n"))
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatal("shutdown hook never fired")
	}
	if gotReason == "" || !contains(gotReason, "maintenance") {
		t.Errorf("expected reason to contain maintenance, got %q", gotReason)
	}
}

func TestRestart_FiresShutdownHook(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()
	var fired atomic.Int32
	WithShutdown(func(reason string) {
		fired.Add(1)
	})(srv)

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("RESTART\r\n"))
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shutdown hook never fired for RESTART")
}

func TestRehash_NoReloaderEmitsNotice(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "o"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c.Write([]byte("REHASH\r\n"))
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	saw := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if contains(line, "NOTICE") && contains(line, "REHASH") {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("expected NOTICE explaining REHASH unavailable")
	}
}

// silence the import linter when only one test ends up exercising it
var _ = errors.New

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
