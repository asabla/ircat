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
	"github.com/asabla/ircat/internal/federation"
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

// BenchmarkFederation_PrivmsgRoundtripTCP is the same as the
// net.Pipe variant above but bridges the two real Server peers
// via an actual loopback TCP socket. The number it produces is
// the realistic floor for an operator deployment, including
// the kernel TCP overhead. The net.Pipe variant exists for the
// "what does the broadcast + routing logic itself cost"
// question; this one exists for the "what does an operator
// see in production" question.
func BenchmarkFederation_PrivmsgRoundtripTCP(b *testing.B) {
	addrA, srvA := buildBenchPeer(b, "node-a")
	addrB, srvB := buildBenchPeer(b, "node-b")

	closeLink := linkBenchServersTCP(b, srvA, srvB)
	defer closeLink()

	cAlice, rAlice := dialClient(b, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	skipUntilNumeric(b, cAlice, rAlice, "422")

	cBob, rBob := dialClient(b, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	skipUntilNumeric(b, cBob, rBob, "422")

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

// linkBenchServersTCP wires two real *server.Server peers via
// an actual TCP loopback socket. Returns a teardown that
// closes both ends.
func linkBenchServersTCP(b *testing.B, sa, sb *Server) func() {
	b.Helper()
	// Listen on an ephemeral loopback port so two parallel
	// bench runs do not collide.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	acceptCh := make(chan acceptResult, 1)
	go func() {
		c, err := ln.Accept()
		acceptCh <- acceptResult{conn: c, err: err}
	}()

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	connDial, err := dialer.Dial("tcp", ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		b.Fatal(err)
	}
	res := <-acceptCh
	if res.err != nil {
		_ = connDial.Close()
		_ = ln.Close()
		b.Fatal(res.err)
	}
	connAccept := res.conn

	linkA := federation.New(sa, federation.LinkConfig{
		PeerName:    sb.ServerName(),
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Version:     "ircat-test",
		Description: "A->B (tcp)",
	}, nil)
	linkB := federation.New(sb, federation.LinkConfig{
		PeerName:    sa.ServerName(),
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Version:     "ircat-test",
		Description: "B->A (tcp)",
	}, nil)

	readerA := federation.WrapConnRead(connDial)
	writerA := federation.WrapConnWrite(connDial)
	readerB := federation.WrapConnRead(connAccept)
	writerB := federation.WrapConnWrite(connAccept)

	ctx, cancel := context.WithCancel(context.Background())
	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() { doneA <- linkA.Run(ctx, readerA, writerA) }()
	go func() { doneB <- linkB.Run(ctx, readerB, writerB) }()

	sa.RegisterLink(sb.ServerName(), linkA)
	sb.RegisterLink(sa.ServerName(), linkB)

	if err := linkA.OpenOutbound(); err != nil {
		b.Fatal(err)
	}
	if err := linkB.OpenOutbound(); err != nil {
		b.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if linkA.State() == federation.LinkActive && linkB.State() == federation.LinkActive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if linkA.State() != federation.LinkActive || linkB.State() != federation.LinkActive {
		cancel()
		_ = connDial.Close()
		_ = connAccept.Close()
		_ = ln.Close()
		b.Fatalf("tcp handshake stalled: A=%s B=%s", linkA.State(), linkB.State())
	}

	return func() {
		cancel()
		_ = linkA.Close()
		_ = linkB.Close()
		_ = connDial.Close()
		_ = connAccept.Close()
		_ = ln.Close()
		sa.UnregisterLink(sb.ServerName())
		sb.UnregisterLink(sa.ServerName())
		<-doneA
		<-doneB
	}
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

