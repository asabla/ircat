// Command soak drives a configurable load against a running
// ircat instance and reports throughput, error rates, and a
// rough RSS delta.
//
// The harness opens N concurrent IRC connections, joins each
// one to M channels (round-robin so every channel ends up with
// roughly the same number of members), and then runs a
// sustained PRIVMSG load for the configured duration. At the
// end of the run it prints a summary line and exits non-zero
// if any of the failure thresholds were tripped.
//
// Typical invocation:
//
//	go run ./tests/soak \
//	  -addr 127.0.0.1:6667 \
//	  -conns 1000 \
//	  -channels 50 \
//	  -msgs-per-sec 100 \
//	  -duration 10m
//
// The defaults are intentionally small so a developer can run
// the harness against a local ircat without thinking about it.
// The full v1.1 reference soak (10k conns, 1k channels, 24h)
// is documented in docs/OPERATIONS.md and intended for the
// nightly job rather than interactive use.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/asabla/ircat/tests/e2e/ircclient"
)

func main() {
	var (
		addr        = flag.String("addr", "127.0.0.1:6667", "ircat plain TCP listener")
		nConns      = flag.Int("conns", 100, "number of concurrent client connections")
		nChannels   = flag.Int("channels", 10, "total channels to spread members across")
		msgsPerSec  = flag.Int("msgs-per-sec", 50, "aggregate target PRIVMSG rate (across all conns)")
		duration    = flag.Duration("duration", 60*time.Second, "soak duration after warmup")
		warmup      = flag.Duration("warmup", 5*time.Second, "warm-up period before measuring")
		nickPrefix  = flag.String("nick-prefix", "soak", "nickname prefix for spawned connections")
		maxDropRate = flag.Float64("max-drop-rate", 0.01, "maximum acceptable drop rate as a fraction of sent (0..1); exit non-zero if exceeded")

		meshMode = flag.Bool("mesh", false, "run three-node federation mesh soak instead of single-node")
		addrs    = flag.String("addrs", "", "comma-separated list of exactly 3 ircat addresses (required when -mesh is set)")
	)
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)

	if *meshMode {
		runMeshMode(*addrs, *nConns, *nChannels, *msgsPerSec, *duration, *warmup, *nickPrefix, *maxDropRate)
		return
	}

	log.Printf("soak start: addr=%s conns=%d channels=%d rate=%d/s duration=%s",
		*addr, *nConns, *nChannels, *msgsPerSec, *duration)

	// Establish all connections concurrently. We bail at the
	// first failure rather than running a partial soak — a
	// partial run would skew the rate calculation.
	clients := make([]*ircclient.Client, *nConns)
	{
		var (
			wg     sync.WaitGroup
			failed atomic.Int32
		)
		wg.Add(*nConns)
		for i := 0; i < *nConns; i++ {
			i := i
			go func() {
				defer wg.Done()
				c, err := ircclient.Dial(*addr, 5*time.Second)
				if err != nil {
					log.Printf("dial[%d]: %v", i, err)
					failed.Add(1)
					return
				}
				nick := fmt.Sprintf("%s%d", *nickPrefix, i)
				if err := c.Register(nick, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("register[%d]: %v", i, err)
					_ = c.Close()
					failed.Add(1)
					return
				}
				clients[i] = c
			}()
		}
		wg.Wait()
		if failed.Load() > 0 {
			log.Fatalf("setup failed: %d connection(s) failed", failed.Load())
		}
	}
	defer func() {
		for _, c := range clients {
			if c != nil {
				_ = c.Close()
			}
		}
	}()
	log.Printf("connected %d clients", *nConns)

	// Outer context covers the whole run: JOIN burst, warmup, and
	// the measured PRIVMSG phase. Drainers live for this window.
	// The big headroom allows for a slow JOIN burst on a loaded
	// runner (high conn count × high channel count fans out
	// quadratic work on the server side).
	ctx, cancel := signalContext(*warmup + *duration + 60*time.Minute)
	defer cancel()

	var (
		sent     atomic.Int64
		received atomic.Int64
		drops    atomic.Int64
	)

	// Drainers: one goroutine per client, running for the whole
	// test. They MUST be started before the JOIN burst — otherwise
	// the server fans JOIN broadcasts and NAMES/TOPIC replies into
	// a socket no one is reading, the kernel receive buffer fills,
	// the server's writeLoop back-pressures, and the per-conn
	// sendq overflow guard cancels the connection. That bug
	// manifests client-side as the very next JOIN write returning
	// "connection reset by peer".
	var drainerWg sync.WaitGroup
	drainerWg.Add(*nConns)
	for _, c := range clients {
		c := c
		go func() {
			defer drainerWg.Done()
			drainClient(ctx, c, &received)
		}()
	}

	// Each client joins every channel in parallel, paced so the
	// server's per-conn sendq does not back up under the fan-out
	// storm. Every JOIN to a channel with N existing members
	// generates N+4 replies (fanout + joiner's topic/names/
	// endofnames), so a naive all-at-once burst at high conn
	// counts trivially exceeds the bounded sendq. We cap each
	// client to roughly joinPacePerSec JOINs per second, which
	// keeps the instantaneous fan-out within what the writeLoop
	// can drain to the socket.
	{
		const joinPacePerSec = 200
		var (
			joinWg  sync.WaitGroup
			joinErr atomic.Value // stores error
		)
		joinWg.Add(*nConns)
		for i, c := range clients {
			i, c := i, c
			go func() {
				defer joinWg.Done()
				pace := time.Second / time.Duration(joinPacePerSec)
				for j := 0; j < *nChannels; j++ {
					if err := c.Send(fmt.Sprintf("JOIN #soak%d", j)); err != nil {
						joinErr.CompareAndSwap(nil, fmt.Errorf("join[%d/%d]: %w", i, j, err))
						return
					}
					time.Sleep(pace)
				}
			}()
		}
		joinWg.Wait()
		if v := joinErr.Load(); v != nil {
			cancel()
			drainerWg.Wait()
			log.Fatal(v.(error))
		}
	}
	// Let the JOIN broadcasts quiesce before we start measuring.
	time.Sleep(*warmup)
	log.Printf("warmup complete")

	// Reset so the summary only counts what arrived during the
	// measured PRIVMSG window.
	received.Store(0)

	// Sustained PRIVMSG load. Each client runs at
	// (msgsPerSec / nConns) per second. Senders share a context
	// bounded by *duration; a Ctrl-C tears the run down via ctx.
	senderCtx, senderCancel := context.WithTimeout(ctx, *duration)
	defer senderCancel()

	perConnRate := float64(*msgsPerSec) / float64(*nConns)
	if perConnRate <= 0 {
		perConnRate = 0.001
	}
	interval := time.Duration(float64(time.Second) / perConnRate)

	t0 := time.Now()

	var wg sync.WaitGroup
	wg.Add(*nConns)
	for i, c := range clients {
		i, c := i, c
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(i)))
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-senderCtx.Done():
					return
				case <-ticker.C:
					ch := fmt.Sprintf("#soak%d", r.Intn(*nChannels))
					line := fmt.Sprintf("PRIVMSG %s :soak %d %d", ch, i, sent.Load())
					if err := c.Send(line); err != nil {
						drops.Add(1)
						return
					}
					sent.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(t0)

	// Senders are done; tell drainers to stop and wait for them.
	cancel()
	drainerWg.Wait()

	// Summary. The harness reports sent / received / drops /
	// rate. Server-side RSS observation is the operator's job
	// via the /metrics endpoint or `top -p <pid>` — the harness
	// itself runs in a separate process so it cannot measure
	// the server's heap directly.
	rate := float64(sent.Load()) / elapsed.Seconds()
	log.Printf("soak done: elapsed=%s sent=%d received=%d drops=%d rate=%.0f/s",
		elapsed.Round(time.Millisecond), sent.Load(), received.Load(), drops.Load(), rate)

	dropRate := 0.0
	if sent.Load() > 0 {
		dropRate = float64(drops.Load()) / float64(sent.Load())
	}
	if dropRate > *maxDropRate {
		log.Fatalf("FAIL: drop rate %.4f exceeds threshold %.4f", dropRate, *maxDropRate)
	}
	log.Printf("PASS (drop rate %.4f)", dropRate)
}

// runMeshMode parses the -addrs flag into a [3]string and
// delegates to RunMesh with the shared soak flags.
func runMeshMode(addrsFlag string, conns, channels, msgsPerSec int, duration, warmup time.Duration, nickPrefix string, maxDropRate float64) {
	parts := strings.Split(addrsFlag, ",")
	if len(parts) != 3 || parts[0] == "" {
		log.Fatal("-mesh requires -addrs with exactly 3 comma-separated addresses")
	}
	cfg := MeshConfig{
		Addrs:        [3]string{parts[0], parts[1], parts[2]},
		ConnsPerNode: conns,
		Channels:     channels,
		MsgsPerSec:   msgsPerSec,
		Duration:     duration,
		Warmup:       warmup,
		NickPrefix:   nickPrefix,
		MaxDropRate:  maxDropRate,
	}
	ctx, cancel := signalContext(duration + warmup + 30*time.Second)
	defer cancel()
	if err := RunMesh(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

// drainClient reads every line the server sends, dropping them on
// the floor after incrementing the receive counter. Short rolling
// read deadlines let the goroutine wake up to notice ctx
// cancellation; a non-timeout read error (EOF, closed conn) exits
// the drainer so a dead connection does not spin.
func drainClient(ctx context.Context, c *ircclient.Client, received *atomic.Int64) {
	for {
		if ctx.Err() != nil {
			return
		}
		line, err := c.ReadLine(time.Now().Add(500 * time.Millisecond))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return
		}
		if line != "" {
			received.Add(1)
		}
	}
}

// signalContext returns a context that fires after d, on a
// SIGINT, or on a SIGTERM — whichever comes first. Used so the
// soak harness can be Ctrl-C'd cleanly mid-run.
func signalContext(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return ctx, cancel
}
