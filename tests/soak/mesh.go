package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asabla/ircat/tests/e2e/ircclient"
)

// MeshConfig configures a three-node federation mesh soak run.
type MeshConfig struct {
	Addrs        [3]string
	ConnsPerNode int
	Channels     int
	MsgsPerSec   int
	Duration     time.Duration
	Warmup       time.Duration
	NickPrefix   string
	MaxDropRate  float64
}

// RunMesh drives a sustained PRIVMSG load across a three-node
// federation mesh. Clients are distributed evenly across the three
// nodes; channels span all nodes. Every 20th message is a cross-node
// probe: a specially-tagged PRIVMSG sent from a client on one node
// whose channel members on the other two nodes watch for.
//
// The harness reports per-node throughput, aggregate rate, and a
// cross-node delivery ratio. It returns a non-nil error if the drop
// rate or probe delivery ratio exceeds the configured thresholds.
func RunMesh(ctx context.Context, cfg MeshConfig) error {
	totalConns := cfg.ConnsPerNode * 3
	log.Printf("mesh: addrs=%v conns=%d channels=%d rate=%d/s duration=%s",
		cfg.Addrs, totalConns, cfg.Channels, cfg.MsgsPerSec, cfg.Duration)

	// Phase 1: connect and register.
	clients := make([]*ircclient.Client, totalConns)
	nodeOf := make([]int, totalConns) // which node index owns each client
	{
		var (
			wg     sync.WaitGroup
			failed atomic.Int32
		)
		wg.Add(totalConns)
		for i := 0; i < totalConns; i++ {
			i := i
			node := i % 3
			go func() {
				defer wg.Done()
				c, err := ircclient.Dial(cfg.Addrs[node], 5*time.Second)
				if err != nil {
					log.Printf("dial[%d node=%d]: %v", i, node, err)
					failed.Add(1)
					return
				}
				nick := fmt.Sprintf("%s%d", cfg.NickPrefix, i)
				if err := c.Register(nick, time.Now().Add(10*time.Second)); err != nil {
					log.Printf("register[%d]: %v", i, err)
					_ = c.Close()
					failed.Add(1)
					return
				}
				clients[i] = c
				nodeOf[i] = node
			}()
		}
		wg.Wait()
		if failed.Load() > 0 {
			meshCloseAll(clients)
			return fmt.Errorf("mesh setup: %d connection(s) failed", failed.Load())
		}
	}
	defer meshCloseAll(clients)
	log.Printf("mesh: connected %d clients (%d per node)", totalConns, cfg.ConnsPerNode)

	// Shared counters used by both the drainer and sender phases.
	var (
		sent     [3]atomic.Int64
		received [3]atomic.Int64
		drops    [3]atomic.Int64
		probes   atomic.Int64
		probeHit atomic.Int64
	)

	// Drainers live for the whole remaining run (JOIN + warmup +
	// measured phase). Starting them before the JOIN burst keeps
	// the server's per-conn sendq from overflowing while we fan
	// JOIN broadcasts across three nodes.
	drainerCtx, drainerCancel := context.WithCancel(ctx)
	defer drainerCancel()

	var drainerWg sync.WaitGroup
	drainerWg.Add(totalConns)
	for i, c := range clients {
		i, c := i, c
		node := nodeOf[i]
		go func() {
			defer drainerWg.Done()
			meshDrainClient(drainerCtx, c, &received[node], &probeHit)
		}()
	}

	// Phase 2: join channels (drainers are running).
	for i, c := range clients {
		for j := 0; j < cfg.Channels; j++ {
			ch := fmt.Sprintf("#mesh%d", j)
			if err := c.Send(fmt.Sprintf("JOIN %s", ch)); err != nil {
				drainerCancel()
				drainerWg.Wait()
				return fmt.Errorf("join[%d/%d]: %w", i, j, err)
			}
		}
	}
	time.Sleep(cfg.Warmup)
	log.Printf("mesh: warmup complete")

	// Reset counters so the summary only reflects the measured
	// PRIVMSG phase.
	for n := 0; n < 3; n++ {
		received[n].Store(0)
	}
	probeHit.Store(0)

	// Phase 3: sustained PRIVMSG load with cross-node probes.
	perConnRate := float64(cfg.MsgsPerSec) / float64(totalConns)
	if perConnRate <= 0 {
		perConnRate = 0.001
	}
	interval := time.Duration(float64(time.Second) / perConnRate)

	meshCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	t0 := time.Now()

	var wg sync.WaitGroup
	wg.Add(totalConns)
	for i, c := range clients {
		i, c := i, c
		node := nodeOf[i]
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(i)))
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			msgNum := int64(0)
			for {
				select {
				case <-meshCtx.Done():
					return
				case <-ticker.C:
					ch := fmt.Sprintf("#mesh%d", r.Intn(cfg.Channels))
					var line string
					msgNum++
					if msgNum%20 == 0 {
						// Cross-node probe: tagged so remote
						// readers can detect it.
						line = fmt.Sprintf("PRIVMSG %s :XPROBE::%d:%d", ch, node, msgNum)
						probes.Add(1)
					} else {
						line = fmt.Sprintf("PRIVMSG %s :mesh %d %d", ch, i, msgNum)
					}
					if err := c.Send(line); err != nil {
						drops[node].Add(1)
						return
					}
					sent[node].Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(t0)

	// Senders are done; shut drainers down too.
	drainerCancel()
	drainerWg.Wait()

	// Phase 4: report.
	totalSent := sent[0].Load() + sent[1].Load() + sent[2].Load()
	totalRecv := received[0].Load() + received[1].Load() + received[2].Load()
	totalDrops := drops[0].Load() + drops[1].Load() + drops[2].Load()
	rate := float64(totalSent) / elapsed.Seconds()

	log.Printf("mesh done: elapsed=%s", elapsed.Round(time.Millisecond))
	for n := 0; n < 3; n++ {
		log.Printf("  node[%d] %s: sent=%d received=%d drops=%d",
			n, cfg.Addrs[n], sent[n].Load(), received[n].Load(), drops[n].Load())
	}
	log.Printf("  aggregate: sent=%d received=%d drops=%d rate=%.0f/s",
		totalSent, totalRecv, totalDrops, rate)

	probesSent := probes.Load()
	probesHit := probeHit.Load()
	probeRatio := 0.0
	if probesSent > 0 {
		probeRatio = float64(probesHit) / float64(probesSent)
	}
	log.Printf("  probes: sent=%d hit=%d ratio=%.4f", probesSent, probesHit, probeRatio)

	dropRate := 0.0
	if totalSent > 0 {
		dropRate = float64(totalDrops) / float64(totalSent)
	}

	if dropRate > cfg.MaxDropRate {
		return fmt.Errorf("FAIL: drop rate %.4f exceeds threshold %.4f", dropRate, cfg.MaxDropRate)
	}
	// Cross-node probes are channel broadcasts received by every
	// member on every node. A 50% hit ratio is the floor — with N
	// members per channel across 3 nodes, a single probe generates
	// (N-1) receives, so the ratio should be well above 1.0 at
	// any non-trivial conn count.
	if probesSent > 0 && probeRatio < 0.5 {
		return fmt.Errorf("FAIL: cross-node probe ratio %.4f below 0.5 threshold", probeRatio)
	}

	log.Printf("PASS (drop rate %.4f, probe ratio %.4f)", dropRate, probeRatio)
	return nil
}

func meshCloseAll(clients []*ircclient.Client) {
	for _, c := range clients {
		if c != nil {
			_ = c.Close()
		}
	}
}

// meshDrainClient drains server replies for a single mesh client,
// counting lines received and cross-node probe hits. Short rolling
// read deadlines keep the goroutine responsive to ctx cancellation;
// a non-timeout error (EOF, closed conn) exits so a dead connection
// does not spin.
func meshDrainClient(ctx context.Context, c *ircclient.Client, received, probeHit *atomic.Int64) {
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
		// Probe line is ":nick!user@host PRIVMSG #chan :XPROBE::N:M"
		// so we just substring-match the tag rather than parse.
		if strings.Contains(line, "XPROBE::") {
			probeHit.Add(1)
		}
	}
}
