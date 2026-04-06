package server

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/federation"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// buildFederationPeer spins up one Server for the two-node
// federation integration test. Each peer runs on its own random
// localhost port so real IRC clients can connect.
func buildFederationPeer(t *testing.T, name string) (string, *Server, func()) {
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
	srv := New(cfg, world, logger)

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

// linkTwoServers wires two servers together via net.Pipe and
// returns a cleanup function. The link uses in-process streams
// with a line framer to drive federation.Link from both sides.
func linkTwoServers(t *testing.T, a, b *Server) func() {
	t.Helper()
	connA, connB := net.Pipe()

	readerA := wrapConnRead(connA)
	writerA := wrapConnWrite(connA)
	readerB := wrapConnRead(connB)
	writerB := wrapConnWrite(connB)

	linkA := federation.New(a, federation.LinkConfig{
		PeerName:    b.ServerName(),
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Version:     "ircat-test",
		Description: "A->B",
	}, nil)
	linkB := federation.New(b, federation.LinkConfig{
		PeerName:    a.ServerName(),
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Version:     "ircat-test",
		Description: "B->A",
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() { doneA <- linkA.Run(ctx, readerA, writerA) }()
	go func() { doneB <- linkB.Run(ctx, readerB, writerB) }()

	// Register the outbound-send side on each server so the
	// broadcast path knows where to forward.
	a.RegisterLink(b.ServerName(), linkA)
	b.RegisterLink(a.ServerName(), linkB)

	// Drive both handshakes.
	if err := linkA.OpenOutbound(); err != nil {
		t.Fatal(err)
	}
	if err := linkB.OpenOutbound(); err != nil {
		t.Fatal(err)
	}

	// Wait for both sides to reach Active.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if linkA.State() == federation.LinkActive && linkB.State() == federation.LinkActive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if linkA.State() != federation.LinkActive || linkB.State() != federation.LinkActive {
		cancel()
		_ = connA.Close()
		_ = connB.Close()
		t.Fatalf("handshake did not finish: A=%s B=%s", linkA.State(), linkB.State())
	}

	return func() {
		cancel()
		_ = linkA.Close()
		_ = linkB.Close()
		_ = connA.Close()
		_ = connB.Close()
		a.UnregisterLink(b.ServerName())
		b.UnregisterLink(a.ServerName())
		<-doneA
		<-doneB
	}
}

// wrapConnRead turns a net.Conn into a channel of parsed
// protocol.Messages, closing the channel on EOF. Used by the
// federation integration tests to bridge net.Conn to the Link
// readMessages parameter.
func wrapConnRead(c net.Conn) <-chan *protocol.Message {
	out := make(chan *protocol.Message, 64)
	go func() {
		defer close(out)
		r := bufio.NewReaderSize(c, 4096)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			msg, perr := protocol.Parse(line)
			if perr != nil {
				continue
			}
			out <- msg
		}
	}()
	return out
}

// wrapConnWrite returns a lineWriter equivalent that encodes each
// message and writes it to c. It matches the unexported lineWriter
// alias in internal/federation via interface satisfaction.
func wrapConnWrite(c net.Conn) func(msg *protocol.Message) error {
	return func(msg *protocol.Message) error {
		data, err := msg.Bytes()
		if err != nil {
			return err
		}
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Write(data)
		return err
	}
}

func TestFederation_TwoNodesPrivmsgAcrossLink(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// Connect alice to node A, bob to node B.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// Node A should see "bob" as a remote user after the burst
	// (which reran when alice registered? actually the burst
	// happens at link-up, so alice and bob are NOT in the burst).
	//
	// For M7 MVP the burst is static (at handshake time). Users
	// that connect AFTER the burst have to be propagated by
	// runtime NICK messages. The M7 scope does not cover that
	// yet, so the test pre-registers alice/bob via the world
	// directly on BOTH sides to simulate a post-burst propagation.

	// Inject alice into B's world as a remote user.
	if _, err := srvB.world.AddUser(&state.User{
		Nick:       "alice",
		User:       "alice",
		Host:       "127.0.0.1",
		Realname:   "Alice",
		Registered: true,
		HomeServer: "node-a",
	}); err != nil {
		t.Fatalf("inject alice remotely: %v", err)
	}
	// Inject bob into A's world as a remote user.
	if _, err := srvA.world.AddUser(&state.User{
		Nick:       "bob",
		User:       "bob",
		Host:       "127.0.0.1",
		Realname:   "Bob",
		Registered: true,
		HomeServer: "node-b",
	}); err != nil {
		t.Fatalf("inject bob remotely: %v", err)
	}

	// Both join #fed. On node A, alice joins and bob is a remote
	// member. On node B, bob joins and alice is a remote member.
	// Run JOIN via the IRC client on both sides for the local
	// halves; membership for the remote halves is set up manually.
	cAlice.Write([]byte("JOIN #fed\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #fed\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// Add remote members directly to the channel maps so the
	// broadcast path sees them as present. This simulates what
	// the federation burst / JOIN propagation would do.
	chB := srvB.world.FindChannel("#fed")
	bobRemoteA := srvA.world.FindByNick("bob")
	aliceRemoteB := srvB.world.FindByNick("alice")
	chA := srvA.world.FindChannel("#fed")
	if chA == nil || chB == nil || bobRemoteA == nil || aliceRemoteB == nil {
		t.Fatalf("missing setup: chA=%v chB=%v bobRemoteA=%v aliceRemoteB=%v",
			chA, chB, bobRemoteA, aliceRemoteB)
	}
	if _, _, _, err := srvA.world.JoinChannel(bobRemoteA.ID, "#fed"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := srvB.world.JoinChannel(aliceRemoteB.ID, "#fed"); err != nil {
		t.Fatal(err)
	}

	// Alice sends PRIVMSG #fed :hello. Bob on node B should
	// receive it via federation.
	cAlice.Write([]byte("PRIVMSG #fed :hello from A\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG #fed ") &&
			strings.HasSuffix(line, ":hello from A")
	})

	// And reverse: bob sends, alice receives.
	cBob.Write([]byte("PRIVMSG #fed :hi from B\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, " PRIVMSG #fed ") &&
			strings.HasSuffix(line, ":hi from B")
	})
}
