package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// startTestServerWithPassword brings up a server that requires the
// supplied PASS value before completing registration. Used by the
// gate tests below; copy of startTestServer with one extra field set.
func startTestServerWithPassword(t *testing.T, password string) (string, func()) {
	t.Helper()
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:           "irc.test",
			Network:        "TestNet",
			ClientPassword: password,
			Listeners: []config.Listener{
				{Address: "127.0.0.1:0"},
			},
			Limits: config.LimitsConfig{
				NickLength:              30,
				ChannelLength:           50,
				TopicLength:             390,
				AwayLength:              255,
				KickReasonLength:        255,
				PingIntervalSeconds:     5,
				PingTimeoutSeconds:      20,
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
	srv := New(cfg, state.NewWorld(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	var addr string
	for {
		if a := srv.ListenerAddrs(); len(a) > 0 {
			addr = a[0].String()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}
	teardown := func() {
		cancel()
		<-done
	}
	return addr, teardown
}

func TestPassGate_CorrectPasswordCompletes(t *testing.T) {
	addr, teardown := startTestServerWithPassword(t, "hunter2")
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("PASS hunter2\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "001", time.Now().Add(2*time.Second))
}

func TestPassGate_WrongPasswordRejects(t *testing.T) {
	addr, teardown := startTestServerWithPassword(t, "hunter2")
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("PASS wrongpass\r\nNICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "464", time.Now().Add(2*time.Second))
	// The conn should also be closed; an ERROR line is allowed
	// before close, then EOF.
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := r.ReadString('\n'); err != nil {
			break
		}
	}
}

func TestPassGate_MissingPasswordRejects(t *testing.T) {
	addr, teardown := startTestServerWithPassword(t, "hunter2")
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "464", time.Now().Add(2*time.Second))
}

func TestPassGate_UnsetPasswordAllowsAll(t *testing.T) {
	// Empty ClientPassword should leave the historical "no gate"
	// behaviour intact — the standard startTestServer harness uses
	// no password and is exercised by every other test, but pin
	// the contract explicitly.
	addr, teardown := startTestServerWithPassword(t, "")
	defer teardown()
	c, r := dialClient(t, addr)
	defer c.Close()
	c.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, c, r, "001", time.Now().Add(2*time.Second))
}

// silence the import linter — net is brought in for the dialer that
// dialClient uses indirectly via the helper test file we're co-located
// with.
var _ = net.Dial
