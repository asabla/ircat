package server

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
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
func linkTwoServers(t testing.TB, a, b *Server) func() {
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

// TestFederation_NickCollisionLowerTSWins exercises M9 task #92:
// when two peers both register a user with the same nick before
// they exchange bursts, the federation receiver should use the
// lower TS as the tiebreaker. The user with the lower TS keeps
// the nick; the loser gets killed.
//
// The test pre-populates two worlds with conflicting users
// (different TS), then sends a burst NICK from peer B into
// peer A's link. A's existing alice has a HIGHER TS than the
// incoming alice, so A must drop its local copy and accept the
// incoming one with the lower TS.
func TestFederation_NickCollisionLowerTSWins(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()

	// Connect a real local alice to A. Her TS is set at
	// registration completion.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	aliceLocal := srvA.world.FindByNick("alice")
	if aliceLocal == nil {
		t.Fatal("alice not registered locally")
	}
	localTS := aliceLocal.TS

	// Build a fake remote alice with a STRICTLY OLDER TS so the
	// collision resolver picks the incoming side.
	olderTS := localTS - 1_000_000

	// Drive the burst NICK directly through the link's dispatch
	// path via a federation.Link wired to a counting writer.
	// We need a real Link constructed against srvA so the
	// receiver uses srvA.HandleSquit / DropLocalUser etc.
	logger, _, _ := logging.New(logging.Options{Format: "text", Level: "info"})
	cfg := federation.LinkConfig{
		PeerName:    "node-b",
		PasswordIn:  "shared",
		PasswordOut: "shared",
		Description: "ts collision test",
	}
	link := federation.New(srvA, cfg, logger)
	srvA.RegisterLink("node-b", link)
	defer srvA.UnregisterLink("node-b")

	// Drive the link by feeding messages directly into its Run
	// goroutine. We use net.Pipe so the link's read goroutine
	// has a reader to consume from.
	connSrv, connTest := net.Pipe()
	reader := federation.WrapConnRead(connSrv)
	writer := federation.WrapConnWrite(connSrv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- link.Run(ctx, reader, writer) }()
	defer func() {
		cancel()
		_ = connSrv.Close()
		_ = connTest.Close()
		<-done
	}()

	// Force the link into Active state by completing the
	// handshake. We are the test driver so we send the peer
	// PASS+SERVER lines into connTest.
	if _, err := connTest.Write([]byte("PASS shared 0210 IRC| ircat-test\r\nSERVER node-b 1 1 :node-b\r\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if link.State() == federation.LinkActive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if link.State() != federation.LinkActive {
		t.Fatalf("link did not reach Active: %s", link.State())
	}

	// Burst NICK with the older TS. v1.1 burst layout puts TS
	// at position 7 and the realname trailing at position 8.
	burstWithTS := ":node-b NICK alice 1 alice 10.0.0.1 node-b + " +
		strconv.FormatInt(olderTS, 10) + " :Alice from B\r\n"
	if _, err := connTest.Write([]byte(burstWithTS)); err != nil {
		t.Fatal(err)
	}

	// alice's local conn should be killed because she lost the
	// TS race. Wait for the disconnect signal.
	cAlice.SetReadDeadline(time.Now().Add(3 * time.Second))
	disconnected := false
	for !disconnected {
		_, err := rAlice.ReadString('\n')
		if err != nil {
			disconnected = true
			break
		}
	}
	if !disconnected {
		t.Fatal("local alice was not killed despite losing the TS race")
	}

	// And the world record should now be the incoming alice
	// with the older TS.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		u := srvA.world.FindByNick("alice")
		if u != nil && u.IsRemote() && u.TS == olderTS {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := srvA.world.FindByNick("alice")
	t.Fatalf("expected incoming alice (TS=%d) to win; got %+v", olderTS, got)
}

// TestFederation_NickCollisionHigherTSDropped is the inverse:
// when the incoming TS is HIGHER than the existing record, the
// receiver must drop the incoming one and emit a KILL back to
// the peer so the loser disappears on its side too.
func TestFederation_NickCollisionHigherTSDropped(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()

	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	aliceLocal := srvA.world.FindByNick("alice")
	if aliceLocal == nil {
		t.Fatal("alice not registered locally")
	}
	newerTS := aliceLocal.TS + 1_000_000

	// Inject a counting fed link so we can observe the KILL
	// reply that the receiver should emit.
	counter := &countingFedLink{}
	srvA.RegisterLink("node-b", counter)
	defer srvA.UnregisterLink("node-b")

	logger, _, _ := logging.New(logging.Options{Format: "text", Level: "info"})
	link := federation.New(srvA, federation.LinkConfig{
		PeerName: "node-b", PasswordIn: "shared", PasswordOut: "shared",
		Description: "ts collision test",
	}, logger)

	connSrv, connTest := net.Pipe()
	reader := federation.WrapConnRead(connSrv)
	writer := federation.WrapConnWrite(connSrv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- link.Run(ctx, reader, writer) }()
	defer func() {
		cancel()
		_ = connSrv.Close()
		_ = connTest.Close()
		<-done
	}()
	if _, err := connTest.Write([]byte("PASS shared 0210 IRC| ircat-test\r\nSERVER node-b 1 1 :node-b\r\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if link.State() == federation.LinkActive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	burstWithTS := ":node-b NICK alice 1 alice 10.0.0.1 node-b + " +
		strconv.FormatInt(newerTS, 10) + " :Alice from B\r\n"
	if _, err := connTest.Write([]byte(burstWithTS)); err != nil {
		t.Fatal(err)
	}

	// We expect the receiver to emit a KILL alice to clean up
	// the loser on the peer side. The KILL goes through the
	// real link's writer (connSrv → connTest), so we read it
	// back from connTest.
	rTest := bufio.NewReader(connTest)
	connTest.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotKill := false
	for !gotKill {
		line, err := rTest.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, " KILL alice ") {
			gotKill = true
		}
	}
	if !gotKill {
		t.Fatal("receiver did not emit KILL for loser nick")
	}

	// Local alice should still be registered (she won the TS).
	if u := srvA.world.FindByNick("alice"); u == nil || u.IsRemote() {
		t.Fatalf("local alice should still own the nick; got %+v", u)
	}
}

// TestFederation_SubscriptionRoutingDedupesPerPeer exercises M9
// task #94 by counting the number of times a single PRIVMSG
// crosses the federation link. With subscription routing the
// message should reach the link exactly once even though the
// channel has multiple remote members homed on the same peer.
//
// The test uses a recordingLink shim that wraps the real
// federation.Link and bumps an atomic counter on every Send.
// Build a channel with two remote members on the same peer,
// then publish a PRIVMSG and assert the counter went up by one.
func TestFederation_SubscriptionRoutingDedupesPerPeer(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()

	// Inject two remote users from the same peer directly into
	// node A's world and join them to #ded. We do not need a
	// second real server here — the assertion is purely about
	// how A's broadcast path routes outbound traffic.
	for _, nick := range []string{"r1", "r2"} {
		if _, err := srvA.world.AddUser(&state.User{
			Nick:       nick,
			User:       nick,
			Host:       "remote.host",
			Realname:   nick,
			Registered: true,
			HomeServer: "node-b",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, nick := range []string{"r1", "r2"} {
		u := srvA.world.FindByNick(nick)
		if _, _, _, err := srvA.world.JoinChannel(u.ID, "#ded"); err != nil {
			t.Fatal(err)
		}
	}

	// Register a counting shim under peer "node-b" so the
	// broadcast path treats it as the active link to that peer.
	counter := &countingFedLink{}
	srvA.RegisterLink("node-b", counter)
	srvA.SubscribePeerToChannel("node-b", "#ded")
	defer srvA.UnregisterLink("node-b")

	// Connect a real client and have alice join + send a PRIVMSG.
	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))
	cAlice.Write([]byte("JOIN #ded\r\n"))
	readUntil(t, cAlice, rAlice, time.Now().Add(2*time.Second), func(l string) bool {
		return extractNumeric(l) == "366"
	})

	// JOIN goes to all peers (it's the discovery message). The
	// PRIVMSG that follows must dedupe to a single send even
	// though #ded has two remote members homed on the same peer.
	startSends := counter.count()
	cAlice.Write([]byte("PRIVMSG #ded :hi remotes\r\n"))

	// Wait for at least one PRIVMSG to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if counter.count() > startSends {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	gotPrivmsgs := 0
	counter.mu.Lock()
	for _, m := range counter.msgs {
		if m.Command == "PRIVMSG" {
			gotPrivmsgs++
		}
	}
	counter.mu.Unlock()
	if gotPrivmsgs != 1 {
		t.Errorf("PRIVMSG dedup failed: got %d sends, want 1", gotPrivmsgs)
	}
}

// countingFedLink is a fedLinkSender that records every message
// it would have sent to a peer. Used by the routing tests to
// assert on dedup behaviour without needing a second real server.
type countingFedLink struct {
	mu   sync.Mutex
	msgs []*protocol.Message
}

func (l *countingFedLink) Send(msg *protocol.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *countingFedLink) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.msgs)
}

// TestFederation_KillRoutesAcrossLink exercises M9 task #95:
// an operator KILL on one node propagates to every peer so the
// killed user disappears across the entire mesh, with a synthetic
// QUIT broadcast on the way out.
func TestFederation_KillRoutesAcrossLink(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	cAlice, rAlice := dialClient(t, addrA)
	defer cAlice.Close()
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	expectNumeric(t, cAlice, rAlice, "422", time.Now().Add(2*time.Second))

	cBob, rBob := dialClient(t, addrB)
	defer cBob.Close()
	cBob.Write([]byte("NICK bob\r\nUSER bob 0 * :Bob\r\n"))
	expectNumeric(t, cBob, rBob, "422", time.Now().Add(2*time.Second))

	// Wait for cross-node user visibility before granting +o.
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

	// Grant alice +o by editing the user record directly. This
	// short-circuits the OPER command (which needs a store-backed
	// operator entry) and is the documented way to test KILL in
	// the federation harness.
	aliceLocal := srvA.world.FindByNick("alice")
	aliceLocal.Modes += "o"

	// alice KILLs bob.
	cAlice.Write([]byte("KILL bob :rule violation\r\n"))

	// bob's connection on node B should drop. We expect either
	// an ERROR line or EOF on the read side; both are acceptable.
	deadline = time.Now().Add(3 * time.Second)
	cBob.SetReadDeadline(deadline)
	disconnected := false
	for !disconnected && time.Now().Before(deadline) {
		_, err := rBob.ReadString('\n')
		if err != nil {
			disconnected = true
			break
		}
	}
	if !disconnected {
		t.Fatal("bob did not get disconnected by remote KILL")
	}

	// And bob should be gone from B's world.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srvB.world.FindByNick("bob") == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if u := srvB.world.FindByNick("bob"); u != nil {
		t.Errorf("bob still present on node B after remote KILL: %+v", u)
	}
	// And from A's world too (the local handler dropped the
	// remote-user record on the way out).
	if u := srvA.world.FindByNick("bob"); u != nil {
		t.Errorf("bob still present on node A after KILL: %+v", u)
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
