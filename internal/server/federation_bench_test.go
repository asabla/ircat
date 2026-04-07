package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/state"
)

// BenchmarkFederation_PrivmsgRoundtrip measures the wall-clock
// latency of a PRIVMSG that originates on node A, crosses the
// federation link, and is observed by a client on node B. The
// reported ns/op is the per-message latency averaged over b.N
// messages, and the bench also reports a p50 / p99 metric so
// the FEDERATION.md table can quote both.
//
// The bench uses two real *server.Server instances bridged via
// the in-process linkTwoServers helper that the integration
// tests use, so it measures the same code path the production
// federation transport runs through. The link is plain
// net.Pipe rather than TCP — the loopback overhead is ~1 µs on
// modern Linux and would dominate the per-message cost we
// actually want to measure (the broadcast + routing logic).
func BenchmarkFederation_PrivmsgRoundtrip(b *testing.B) {
	addrA, srvA := buildBenchPeer(b, "node-a")
	addrB, srvB := buildBenchPeer(b, "node-b")

	closeLink := linkBenchServers(b, srvA, srvB)
	defer closeLink()

	// Connect alice to A and bob to B, joined to #b. The first
	// JOIN propagates so node B picks up alice as a remote
	// member of #b before we start measuring.
	cAlice, rAlice := dialClient(b, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	skipUntilNumeric(b, cAlice, rAlice, "422")

	cBob, rBob := dialClient(b, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	skipUntilNumeric(b, cBob, rBob, "422")

	// Wait for cross-node visibility before joining.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvA.world.FindByNick("bob") != nil && srvB.world.FindByNick("alice") != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cAlice.Write([]byte("JOIN #b\r\n"))
	skipUntilNumeric(b, cAlice, rAlice, "366")
	cBob.Write([]byte("JOIN #b\r\n"))
	skipUntilNumeric(b, cBob, rBob, "366")

	samples := make([]time.Duration, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		fmt.Fprintf(cAlice, "PRIVMSG #b :ping %d\r\n", i)
		// Wait for bob to see the message.
		for {
			line, err := rBob.ReadString('\n')
			if err != nil {
				b.Fatalf("read on bob: %v", err)
			}
			if strings.Contains(line, "PRIVMSG #b") &&
				strings.Contains(line, fmt.Sprintf("ping %d", i)) {
				break
			}
		}
		samples[i] = time.Since(start)
	}
	b.StopTimer()

	if len(samples) > 0 {
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		p50 := samples[len(samples)*50/100]
		p99 := samples[len(samples)*99/100]
		b.ReportMetric(float64(p50.Nanoseconds()), "p50-ns")
		b.ReportMetric(float64(p99.Nanoseconds()), "p99-ns")
	}
}

// buildBenchPeer is the *testing.B counterpart of
// buildFederationPeer. The signature mirrors the testing.T
// version but uses b.Helper / b.Fatal so failures inside the
// helper short-circuit cleanly.
func buildBenchPeer(b *testing.B, name string) (string, *Server) {
	b.Helper()
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			Name:    name,
			Network: "FedNet",
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
				MessageBurst:            100_000,
				MessageRefillPerSecond:  100_000,
				MessageViolationsToKick: 100_000,
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
			return a[0].String(), srv
		}
		if time.Now().After(deadline) {
			b.Fatal("server did not bind")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// linkBenchServers is a thin alias kept for symmetry with
// buildBenchPeer; it just delegates to linkTwoServers, which
// now accepts testing.TB.
func linkBenchServers(b *testing.B, sa, sb *Server) func() {
	b.Helper()
	return linkTwoServers(b, sa, sb)
}

func skipUntilNumeric(b *testing.B, c net.Conn, r *bufio.Reader, code string) {
	b.Helper()
	deadline := time.Now().Add(3 * time.Second)
	c.SetReadDeadline(deadline)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			b.Fatalf("read: %v", err)
		}
		if extractNumeric(line) == code {
			c.SetReadDeadline(time.Time{})
			return
		}
	}
}

