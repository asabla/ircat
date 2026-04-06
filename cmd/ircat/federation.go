package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/federation"
	"github.com/asabla/ircat/internal/server"
)

// startFederation wires up the configured federation links: for
// each link with connect=true we dial and drive the outbound
// handshake; for each with accept=true we bind a listener and
// handle peer-initiated connections.
//
// The supervisor goroutine is tied to ctx; when ctx is cancelled
// every open link drains and shuts down.
//
// For M7 MVP this is plain TCP only — no TLS, no reconnect. A
// dropped link just stays dropped until the operator restarts the
// server. That is explicitly called out as a follow-up in
// docs/PLAN.md.
func startFederation(ctx context.Context, cfg *config.Config, srv *server.Server, logger *slog.Logger) func() {
	if cfg.Federation.Enabled == false || len(cfg.Federation.Links) == 0 {
		return func() {}
	}
	logger = logger.With("component", "federation")
	sup := &fedSupervisor{
		ctx:    ctx,
		srv:    srv,
		logger: logger,
	}
	for _, link := range cfg.Federation.Links {
		link := link
		if link.Connect {
			sup.dialOutbound(link)
		}
	}
	// The accept path is a placeholder: v1 dials outbound only.
	// A future commit binds a listener via a dedicated
	// cfg.Federation.ListenAddress field.
	return func() {
		sup.wg.Wait()
	}
}

// fedSupervisor tracks the goroutines for each active link so
// shutdown can wait for them to drain.
type fedSupervisor struct {
	ctx    context.Context
	srv    *server.Server
	logger *slog.Logger

	wg sync.WaitGroup
}

func (s *fedSupervisor) dialOutbound(spec config.LinkSpec) {
	addr := net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
	cfg := federation.LinkConfig{
		PeerName:    spec.Name,
		PasswordIn:  spec.PasswordIn,
		PasswordOut: spec.PasswordOut,
		Version:     "ircat-0.0.1",
		Description: s.srv.LocalServerName() + " -> " + spec.Name,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		dialer := net.Dialer{Timeout: 10 * time.Second}
		conn, err := dialer.DialContext(s.ctx, "tcp", addr)
		if err != nil {
			s.logger.Warn("federation dial failed", "peer", spec.Name, "addr", addr, "error", err)
			return
		}
		s.runLink(conn, cfg, true)
	}()
}

// runLink is the shared drain routine for both outbound and
// inbound connections. It constructs a federation.Link over the
// supplied conn, opens the handshake, and waits for the link to
// close. Registration with the server's broadcast hot path is
// deferred until the link reaches LinkActive via the OnActive
// callback so a failed handshake leaves no dangling entry.
func (s *fedSupervisor) runLink(conn net.Conn, cfg federation.LinkConfig, outbound bool) {
	cfg.OnActive = func(l *federation.Link) {
		s.srv.RegisterLink(cfg.PeerName, l)
		s.logger.Info("federation link registered", "peer", cfg.PeerName)
	}
	cfg.OnClosed = func(l *federation.Link) {
		s.srv.UnregisterLink(cfg.PeerName)
		s.logger.Info("federation link unregistered", "peer", cfg.PeerName)
	}
	link := federation.New(s.srv, cfg, s.logger)
	reader := federation.WrapConnRead(conn)
	writer := federation.WrapConnWrite(conn)

	defer conn.Close()

	if outbound {
		if err := link.OpenOutbound(); err != nil {
			s.logger.Warn("federation open outbound", "error", err)
			return
		}
	} else {
		if err := link.OpenInbound(); err != nil {
			s.logger.Warn("federation open inbound", "error", err)
			return
		}
	}

	if err := link.Run(s.ctx, reader, writer); err != nil {
		s.logger.Warn("federation link exited", "peer", cfg.PeerName, "error", err)
		return
	}
	s.logger.Info("federation link closed", "peer", cfg.PeerName)
}

// silence unused
var _ = fmt.Sprintf
