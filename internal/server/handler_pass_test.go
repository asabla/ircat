package server

import (
	"context"
	"testing"

	"github.com/asabla/ircat/internal/protocol"
)

// TestHandlePass_StoresAllFields drives handlePass directly so we
// can inspect the per-conn pending state without going through the
// listener handshake. The federation handshake path stashes
// version/flags from the second and third params; this pins that
// they actually land where the federation Link expects.
func TestHandlePass_StoresAllFields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{
		server: &Server{},
		ctx:    ctx,
		out:    make(chan *protocol.Message, 8),
	}
	c.handlePass(&protocol.Message{
		Command: "PASS",
		Params:  []string{"sekrit", "0210", "IRC|"},
	})
	if c.pending.password != "sekrit" {
		t.Errorf("password = %q, want sekrit", c.pending.password)
	}
	if c.pending.passVersion != "0210" {
		t.Errorf("passVersion = %q, want 0210", c.pending.passVersion)
	}
	if c.pending.passFlags != "IRC|" {
		t.Errorf("passFlags = %q, want IRC|", c.pending.passFlags)
	}
}

func TestHandlePass_ClientFormJustPassword(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{
		server: &Server{},
		ctx:    ctx,
		out:    make(chan *protocol.Message, 8),
	}
	c.handlePass(&protocol.Message{
		Command: "PASS",
		Params:  []string{"sekrit"},
	})
	if c.pending.password != "sekrit" {
		t.Errorf("password = %q, want sekrit", c.pending.password)
	}
	if c.pending.passVersion != "" || c.pending.passFlags != "" {
		t.Errorf("client-form PASS should leave version/flags empty, got %q / %q",
			c.pending.passVersion, c.pending.passFlags)
	}
}
