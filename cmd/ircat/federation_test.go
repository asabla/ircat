package main

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/server"
	"github.com/asabla/ircat/internal/state"
)

// startCmdServer brings up a server.Server bound to a kernel-
// assigned port. It mirrors the helper in internal/server tests
// but lives here so cmd/ircat can also exercise startFederation.
func startCmdServer(t *testing.T, name string) (string, *server.Server, func()) {
	t.Helper()
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
				MessageBurst:            100,
				MessageRefillPerSecond:  100,
				MessageViolationsToKick: 5,
			},
		},
	}
	logger, _, err := logging.New(logging.Options{Format: "text", Level: "info"})
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv := server.New(cfg, world, logger)

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
	return addr, srv, teardown
}

// TestStartFederation_DialAndAccept exercises the inbound listener
// path: server B binds a federation listen_address, server A is
// configured to dial it, and after the supervisor brings both
// links up the resulting registration must be visible on both
// sides via Server.LinkFor.
func TestStartFederation_DialAndAccept(t *testing.T) {
	_, srvA, closeA := startCmdServer(t, "node-a")
	defer closeA()
	_, srvB, closeB := startCmdServer(t, "node-b")
	defer closeB()

	// Bind a free port for B's federation listener.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(probe.Addr().String())
	port, _ := strconv.Atoi(portStr)
	_ = probe.Close()

	logger, _, _ := logging.New(logging.Options{Format: "text", Level: "info"})

	cfgB := &config.Config{
		Federation: config.FederationConfig{
			Enabled:       true,
			MyServerName:  "node-b",
			ListenAddress: net.JoinHostPort(host, portStr),
			Links: []config.LinkSpec{{
				Name:        "node-a",
				Accept:      true,
				PasswordIn:  "shared",
				PasswordOut: "shared",
			}},
		},
	}
	cfgA := &config.Config{
		Federation: config.FederationConfig{
			Enabled:      cfgB.Federation.Enabled,
			MyServerName: "node-a",
			Links: []config.LinkSpec{{
				Name:        "node-b",
				Connect:     true,
				Host:        host,
				Port:        port,
				PasswordIn:  "shared",
				PasswordOut: "shared",
			}},
		},
	}

	ctxB, cancelB := context.WithCancel(context.Background())
	waitB := startFederation(ctxB, cfgB, srvB, logger)
	defer func() { cancelB(); waitB() }()

	// Give B's listener a moment to bind before A dials.
	time.Sleep(50 * time.Millisecond)

	ctxA, cancelA := context.WithCancel(context.Background())
	waitA := startFederation(ctxA, cfgA, srvA, logger)
	defer func() { cancelA(); waitA() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srvA.LinkFor("node-b") != nil && srvB.LinkFor("node-a") != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("federation links never registered: A->B=%v B->A=%v",
		srvA.LinkFor("node-b") != nil, srvB.LinkFor("node-a") != nil)
}
