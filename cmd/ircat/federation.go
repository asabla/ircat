package main

import (
	"context"
	"errors"
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
// handshake; if cfg.Federation.ListenAddress is non-empty we also
// bind a listener and accept inbound peer connections, matching
// each accepted conn against the LinkSpec entries with accept=true.
//
// The supervisor goroutine is tied to ctx; when ctx is cancelled
// every open link drains and shuts down.
//
// Plain TCP only for now — TLS and reconnect on dropped links
// are tracked separately in docs/PLAN.md.
func startFederation(ctx context.Context, cfg *config.Config, srv *server.Server, logger *slog.Logger) func() {
	if cfg.Federation.Enabled == false {
		return func() {}
	}
	logger = logger.With("component", "federation")
	sup := &fedSupervisor{
		ctx:    ctx,
		srv:    srv,
		logger: logger,
		links:  cfg.Federation.Links,
	}
	for _, link := range cfg.Federation.Links {
		link := link
		if link.Connect {
			sup.dialOutbound(link)
		}
	}
	if addr := cfg.Federation.ListenAddress; addr != "" {
		if err := sup.startListener(addr); err != nil {
			logger.Error("federation listener bind failed", "addr", addr, "error", err)
		}
	}
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
	links  []config.LinkSpec

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

// startListener binds the configured federation listen address
// and accepts inbound peer connections until ctx is cancelled.
// Each accepted conn is matched against the LinkSpec entries with
// accept=true; the first matching spec drives the inbound
// handshake. Connections that do not match any spec are dropped
// after a single log line.
func (s *fedSupervisor) startListener(addr string) error {
	var lc net.ListenConfig
	listener, err := lc.Listen(s.ctx, "tcp", addr)
	if err != nil {
		return err
	}
	s.logger.Info("federation listener bound", "addr", listener.Addr().String())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-s.ctx.Done()
		_ = listener.Close()
	}()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
					return
				}
				s.logger.Warn("federation accept", "error", err)
				continue
			}
			s.handleAccepted(conn)
		}
	}()
	return nil
}

// handleAccepted picks the first LinkSpec that has accept=true.
// In M7 we do not yet implement peer-name negotiation before the
// SERVER line lands — the supervisor commits to the first
// matching spec optimistically. A future commit reads PASS+SERVER
// before binding to a spec.
func (s *fedSupervisor) handleAccepted(conn net.Conn) {
	var spec *config.LinkSpec
	for i := range s.links {
		if s.links[i].Accept {
			spec = &s.links[i]
			break
		}
	}
	if spec == nil {
		s.logger.Warn("federation inbound: no accept spec configured", "remote", conn.RemoteAddr())
		_ = conn.Close()
		return
	}
	cfg := federation.LinkConfig{
		PeerName:    spec.Name,
		PasswordIn:  spec.PasswordIn,
		PasswordOut: spec.PasswordOut,
		Version:     "ircat-0.0.1",
		Description: s.srv.LocalServerName() + " <- " + spec.Name,
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runLink(conn, cfg, false)
	}()
}

// silence unused
var _ = fmt.Sprintf
