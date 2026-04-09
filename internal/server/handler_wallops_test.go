package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// startTestServerWithHandle is the rare variant that hands the test
// the *Server pointer so it can flip mode bits or peek at internal
// state. Used by WALLOPS tests that need to grant +o without going
// through the store-backed OPER command.
func startTestServerWithHandle(t *testing.T) (addr string, srv *Server, teardown func()) {
	t.Helper()
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:    "irc.test",
			Network: "TestNet",
			Listeners: []config.Listener{
				{Address: "127.0.0.1:0"},
			},
			Limits: config.LimitsConfig{
				NickLength:              30,
				ChannelLength:           50,
				TopicLength:             390,
				AwayLength:              255,
				KickReasonLength:        255,
				PingIntervalSeconds:     1,
				PingTimeoutSeconds:      4,
				MessageBurst:            100,
				MessageRefillPerSecond:  100,
				MessageViolationsToKick: 5,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv = New(cfg, world, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if addrs := srv.ListenerAddrs(); len(addrs) > 0 {
			addr = addrs[0].String()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not bind in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	teardown = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server failed to shut down")
		}
	}
	return addr, srv, teardown
}

func TestWallops_RequiresOperator(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "422", time.Now().Add(2*time.Second))

	c.Write([]byte("WALLOPS :hello\r\n"))
	expectNumeric(t, c, r, "481", time.Now().Add(2*time.Second))
}

func TestWallops_BroadcastsToWModeUsers(t *testing.T) {
	addr, srv, teardown := startTestServerWithHandle(t)
	defer teardown()

	// alice connects and gets +o (granted directly).
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	// Wait for the user to land in the world, then grant +o and +w.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srv.world.FindByNick("alice"); u != nil {
			u.Modes += "ow"
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.world.FindByNick("alice") == nil {
		t.Fatal("alice never registered")
	}

	// bob connects with +w. He'll get the wallops via MODE.
	cBob, rBob := dialClient(t, addr)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))
	cBob.Write([]byte("MODE bob +w\r\n"))
	// Drain the mode echo.
	cBob.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "MODE") && strings.Contains(line, "+w") {
			break
		}
	}

	// charlie connects with no +w — must NOT receive the wallops.
	cChar, rChar := dialClient(t, addr)
	defer cChar.Close()
	cChar.Write([]byte("NICK charlie\r\nUSER charlie 0 * :Charlie\r\n"))
	expectNumeric(t, cChar, rChar, "422", time.Now().Add(2*time.Second))

	// alice sends WALLOPS.
	cAlice.Write([]byte("WALLOPS :the meeting is at 3\r\n"))

	// bob receives it.
	cBob.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := false
	for {
		line, err := rBob.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "WALLOPS") && strings.Contains(line, "meeting") {
			got = true
			break
		}
	}
	if !got {
		t.Errorf("bob (+w) did not receive WALLOPS")
	}

	// charlie does not.
	cChar.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	for {
		line, err := rChar.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "WALLOPS") {
			t.Errorf("charlie (no +w) received WALLOPS: %q", line)
		}
	}
}

func TestWallops_NeedMoreParams(t *testing.T) {
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

	c.Write([]byte("WALLOPS\r\n"))
	expectNumeric(t, c, r, "461", time.Now().Add(2*time.Second))
}
