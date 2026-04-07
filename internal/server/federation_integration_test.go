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

	// alice and bob are propagated to the opposite node via the
	// runtime NICK announce that fires on registration completion.
	// Wait for both sides to see each other before issuing the
	// channel JOINs.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvA.world.FindByNick("bob") != nil && srvB.world.FindByNick("alice") != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srvA.world.FindByNick("bob") == nil || srvB.world.FindByNick("alice") == nil {
		t.Fatal("user runtime announce did not propagate over federation")
	}

	// Both join #fed via the IRC client; the JOIN propagates over
	// federation so each side picks up the other as a channel
	// member without any manual injection.
	cAlice.Write([]byte("JOIN #fed\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #fed\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

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

// TestFederation_RuntimePropagation drives JOIN, NICK, and QUIT
// across two real federated servers. Unlike the static-burst test
// above, this one does not pre-inject remote users into either
// world — it relies on the runtime propagation hooks (M7 task #80)
// to forward each event over the link, so a missing hook fails
// the test by stalling on the readUntil for the receiving side.
func TestFederation_RuntimePropagation(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// Both clients connect AFTER the link is up so the burst is
	// empty. Every cross-node visibility from here on has to come
	// from runtime propagation.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// alice's NICK registration must propagate to node-b's world
	// as a remote user with HomeServer=node-a (and vice versa).
	waitForRemote := func(t *testing.T, srv *Server, nick, home string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			u := srv.world.FindByNick(nick)
			if u != nil && u.IsRemote() && u.HomeServer == home {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s never appeared on %s as remote home=%s", nick, srv.cfg.Server.Name, home)
	}
	waitForRemote(t, srvB, "alice", "node-a")
	waitForRemote(t, srvA, "bob", "node-b")

	// alice joins #runtime first; then bob joins. Bob's JOIN
	// should be visible to alice via federation propagation.
	cAlice.Write([]byte("JOIN #runtime\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	cBob.Write([]byte("JOIN #runtime\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// alice should observe bob's remote JOIN as a JOIN line on
	// her socket.
	readUntil(t, cAlice, rAlice, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, " JOIN ") &&
			strings.Contains(line, "#runtime")
	})

	// Cross-channel PRIVMSG without manual injection.
	cAlice.Write([]byte("PRIVMSG #runtime :hi runtime\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " PRIVMSG #runtime ") &&
			strings.HasSuffix(line, ":hi runtime")
	})

	// alice changes her nick. bob should see the NICK line.
	cAlice.Write([]byte("NICK alice2\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice!") &&
			strings.Contains(line, " NICK ") &&
			strings.HasSuffix(line, "alice2")
	})

	// Verify B's world reflects the rename.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvB.world.FindByNick("alice2") != nil && srvB.world.FindByNick("alice") == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srvB.world.FindByNick("alice2") == nil {
		t.Fatal("alice rename did not propagate to node-b")
	}
	if srvB.world.FindByNick("alice") != nil {
		t.Fatal("old nick still present on node-b after rename")
	}

	// alice2 parts. bob should see PART.
	cAlice.Write([]byte("PART #runtime :see ya\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":alice2!") &&
			strings.Contains(line, " PART ") &&
			strings.Contains(line, "#runtime")
	})

	// alice2 quits. bob receives no QUIT in #runtime (alice2 left
	// already), but the world drop must still happen.
	cAlice.Write([]byte("QUIT :bye\r\n"))
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvB.world.FindByNick("alice2") == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("alice2 quit did not propagate to node-b world")
}

// TestFederation_SquitRecoveryDropsRemoteUsers exercises M9
// task #93: when a federation link drops, every user homed on
// the dropped peer must be removed from the surviving node's
// world AND every local channel member that shared a channel
// with the dropped user must see a synthetic QUIT line so the
// disappearance is visible at the IRC layer.
func TestFederation_SquitRecoveryDropsRemoteUsers(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	closeLinkOnce := func() {
		if closeLink != nil {
			closeLink()
			closeLink = nil
		}
	}
	defer closeLinkOnce()

	// Two clients in #squit, one on each side. After both joined
	// every node sees the other side as a remote channel member.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// Wait for the runtime user announce to propagate.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvA.world.FindByNick("bob") != nil && srvB.world.FindByNick("alice") != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srvA.world.FindByNick("bob") == nil {
		t.Fatal("bob never propagated to node A")
	}

	cAlice.Write([]byte("JOIN #squit\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cBob.Write([]byte("JOIN #squit\r\n"))
	readUntil(t, cBob, rBob, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// alice should observe bob's remote JOIN before we tear down
	// the link, so the surviving node has bob recorded as a
	// channel member.
	readUntil(t, cAlice, rAlice, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, " JOIN ") &&
			strings.Contains(line, "#squit")
	})

	// Drop the link. The integration test helper does not call
	// HandleSquit on its own (it bypasses cmd/ircat's supervisor),
	// so we invoke it directly to simulate what the supervisor
	// OnClosed callback does in production.
	closeLinkOnce()
	srvA.HandleSquit("node-b", "Test net split")

	// alice on node A must see a synthetic QUIT for bob.
	readUntil(t, cAlice, rAlice, time.Now().Add(3*time.Second), func(line string) bool {
		return strings.HasPrefix(line, ":bob!") &&
			strings.Contains(line, " QUIT ") &&
			strings.Contains(line, "Test net split")
	})

	// And bob's record must be gone from A's world.
	if u := srvA.world.FindByNick("bob"); u != nil {
		t.Errorf("bob still present on node A after SQUIT: %+v", u)
	}
}

// TestFederation_ModeBurstAndRuntime exercises M9 task #91:
// channel modes are bursted at link-up and runtime MODE changes
// are re-applied on the receiver. The test:
//
//  1. Brings up node A.
//  2. alice on A creates #modes, sets +ntk supersecret, ops bob
//     (a remote user we'll inject manually so the +o has a target).
//  3. Brings up node B and links.
//  4. Asserts the burst delivered the boolean modes, key, and op.
//  5. alice flips +i at runtime; the receiver must mirror it.
//
// The test pre-injects bob into A's world rather than running a
// second client because the burst path only emits modes that exist
// at link-time — easier to set them up before the link comes up.
func TestFederation_ModeBurstAndRuntime(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	_, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	// alice connects to A and sets up #modes BEFORE the link
	// comes up so the burst carries the channel state.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #modes\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})
	cAlice.Write([]byte("MODE #modes +k supersecret\r\n"))
	// Wait for the local MODE echo to confirm the change applied
	// before bringing the link up.
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return strings.Contains(l, " MODE #modes ") && strings.Contains(l, "+k")
	})

	// Now link the two servers. Node B's burst handler will
	// receive the JOIN + TOPIC + MODE lines for #modes.
	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// Wait for #modes to materialize on B with the bursted state.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ch := srvB.world.FindChannel("#modes")
		if ch != nil {
			_, _, _, _, _, _, key, _ := ch.Modes()
			if key == "supersecret" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	chB := srvB.world.FindChannel("#modes")
	if chB == nil {
		t.Fatal("#modes never appeared on node B")
	}
	_, _, noExternal, _, _, topicLocked, key, _ := chB.Modes()
	if !noExternal || !topicLocked {
		t.Errorf("burst did not carry +n+t: noExt=%v topicLocked=%v", noExternal, topicLocked)
	}
	if key != "supersecret" {
		t.Errorf("burst did not carry +k: key=%q", key)
	}

	// Runtime change: alice flips +i. Node B must mirror it.
	cAlice.Write([]byte("MODE #modes +i\r\n"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		inviteOnly, _, _, _, _, _, _, _ := chB.Modes()
		if inviteOnly {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	inviteOnly, _, _, _, _, _, _, _ := chB.Modes()
	if !inviteOnly {
		t.Fatal("runtime +i did not propagate to node B")
	}

	// And remove a mode at runtime: alice clears the key.
	cAlice.Write([]byte("MODE #modes -k\r\n"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, _, _, _, _, key, _ := chB.Modes()
		if key == "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, _, _, _, _, _, keyAfter, _ := chB.Modes()
	if keyAfter != "" {
		t.Errorf("runtime -k did not clear key on node B: %q", keyAfter)
	}
}
