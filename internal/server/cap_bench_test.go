package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// BenchmarkCapNegotiation measures the full IRCv3 registration
// hot path: CAP LS -> CAP REQ -> NICK -> USER -> CAP END ->
// welcome burst (001 through 422).
func BenchmarkCapNegotiation(b *testing.B) {
	addr := startCapBenchServer(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchOneCapReg(b, addr, i)
	}
}

func startCapBenchServer(b *testing.B) string {
	b.Helper()
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:    "irc.bench",
			Network: "BenchNet",
			Listeners: []config.Listener{
				{Address: "127.0.0.1:0"},
			},
			Limits: config.LimitsConfig{
				NickLength:              30,
				ChannelLength:           50,
				TopicLength:             390,
				AwayLength:              255,
				KickReasonLength:        255,
				PingIntervalSeconds:     60,
				PingTimeoutSeconds:      120,
				MessageBurst:            100000,
				MessageRefillPerSecond:  100000,
				MessageViolationsToKick: 100000,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "error"})
	if err != nil {
		b.Fatal(err)
	}
	world := state.NewWorld()
	srv := New(cfg, world, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	b.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if a := srv.ListenerAddrs(); len(a) > 0 {
			return a[0].String()
		}
		if time.Now().After(deadline) {
			b.Fatal("server did not bind")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func benchOneCapReg(b *testing.B, addr string, iter int) {
	b.Helper()
	c, r := dialClient(b, addr)
	defer c.Close()

	nick := fmt.Sprintf("b%d", iter)

	// Match the exact flow from TestRegistration_GatedOnCapEnd:
	// CAP LS -> read reply -> CAP REQ -> NICK -> USER -> CAP END
	c.Write([]byte("CAP LS\r\n"))
	dl := time.Now().Add(5 * time.Second)
	_ = c.SetReadDeadline(dl)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			b.Fatalf("waiting for CAP LS reply: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}

	// Send REQ, NICK, USER, END together. We negotiate multi-prefix
	// (tag-free) rather than server-time to avoid @time= tags that
	// break extractNumeric's ":prefix NNN" parser.
	payload := "CAP REQ :multi-prefix\r\n" +
		"NICK " + nick + "\r\n" +
		"USER bench 0 * :Bench User\r\n" +
		"CAP END\r\n"
	c.Write([]byte(payload))

	// Read until 001 RPL_WELCOME.
	lines := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			b.Fatalf("read: %v (after %d lines)", err, lines)
		}
		lines++
		trimmed := strings.TrimRight(line, "\r\n")
		if extractNumeric(trimmed) == "001" {
			break
		}
	}
	b.ReportMetric(float64(lines), "lines/op")

	// Clean disconnect. Drain until ERROR or EOF so the server
	// has time to remove the user before the next iteration.
	c.Write([]byte("QUIT :bench\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, err := r.ReadString('\n')
		if err != nil {
			break
		}
	}
}
