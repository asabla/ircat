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

func TestPrivmsg_Channel_DeliveredToOtherMembers(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	// Drain bob join echo on alice stream.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.HasPrefix(l, ":bob!") && strings.Contains(l, " JOIN ")
	})

	cAlice.Write([]byte("PRIVMSG #test :hello world\r\n"))

	// Bob should see the PRIVMSG.
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG #test ") &&
			strings.HasSuffix(line, ":hello world")
	})

	// Alice should NOT see her own message echoed. Verify with a
	// short read deadline.
	_ = cAlice.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if line, err := rAlice.ReadString('\n'); err == nil {
		t.Errorf("alice received echo: %q", line)
	}
}

func TestPrivmsg_DirectUserDelivery(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, _ := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("PRIVMSG bob :hi bob\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG bob ") &&
			strings.HasSuffix(line, ":hi bob")
	})
}

func TestPrivmsg_NoSuchNick(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("PRIVMSG ghost :anyone home\r\n"))
	expectNumeric(t, c, r, "401", time.Now().Add(2*time.Second))
}

func TestPrivmsg_NoRecipient(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("PRIVMSG\r\n"))
	expectNumeric(t, c, r, "411", time.Now().Add(2*time.Second))
}

func TestPrivmsg_NoTextToSend(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("PRIVMSG bob\r\n"))
	expectNumeric(t, c, r, "412", time.Now().Add(2*time.Second))
}

func TestPrivmsg_ChannelNoExternalMessages(t *testing.T) {
	// Default new-channel modes include +n. A non-member should hit
	// 404 when trying to PRIVMSG the channel.
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cAlice.Write([]byte("JOIN #test\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()
	cBob.Write([]byte("PRIVMSG #test :smuggling in\r\n"))
	expectNumeric(t, cBob, rBob, "404", time.Now().Add(2*time.Second))
}

func TestNotice_NoErrorRepliesEvenOnFailure(t *testing.T) {
	// NOTICE must NOT generate any reply per RFC 2812 §3.3.2 even
	// when the target does not exist.
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()
	c.Write([]byte("NOTICE ghost :silent failure\r\n"))

	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if line, err := r.ReadString('\n'); err == nil {
		t.Errorf("NOTICE produced an unexpected reply: %q", line)
	}
}

// startFloodTestServer is a variant of startTestServer that pins
// flood control to a tight bucket (burst 2, refill 1/s, kick after
// 3 violations) so the flood test can actually trigger the kick
// without spamming thousands of messages.
func startFloodTestServer(t *testing.T) (addr string, teardown func()) {
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
				PingIntervalSeconds:     5,
				PingTimeoutSeconds:      20,
				MessageBurst:            2,
				MessageRefillPerSecond:  1,
				MessageViolationsToKick: 3,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv := New(cfg, world, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
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
	teardown = func() {
		cancel()
		<-done
	}
	return addr, teardown
}

func TestPrivmsg_FloodKicksConnection(t *testing.T) {
	addr, teardown := startFloodTestServer(t)
	defer teardown()

	cAlice, rAlice := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, _ := register(t, addr, "bob")
	defer cBob.Close()

	// Burn through the bucket: burst is 2, kick after 3 violations,
	// so 2 successful + 3 dropped = 5 messages should trip the kick.
	for i := 0; i < 5; i++ {
		cAlice.Write([]byte("PRIVMSG bob :spam\r\n"))
	}

	// Read alice stream until we see ERROR or EOF.
	deadline := time.Now().Add(3 * time.Second)
	sawError := false
	for {
		_ = cAlice.SetReadDeadline(deadline)
		line, err := rAlice.ReadString('\n')
		if err != nil {
			if !sawError {
				t.Errorf("connection closed without ERROR (last err: %v)", err)
			}
			return
		}
		if strings.HasPrefix(line, "ERROR ") {
			sawError = true
		}
	}
}

func TestNotice_DeliveredToTarget(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	cAlice, _ := register(t, addr, "alice")
	defer cAlice.Close()
	cBob, rBob := register(t, addr, "bob")
	defer cBob.Close()

	cAlice.Write([]byte("NOTICE bob :ping\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(line string) bool {
		return strings.Contains(line, " NOTICE bob ") && strings.HasSuffix(line, ":ping")
	})
}
