package server

import (
	"testing"
	"time"
)

func TestService_FederationBurstCarriesServiceFlag(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	addrB, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	// Register a service on node A BEFORE the link comes up so
	// the burst is the path that carries it.
	cSvc, rSvc := dialClient(t, addrA)
	defer cSvc.Close()
	cSvc.Write([]byte("SERVICE chanserv * * 0 0 :Channel registration\r\n"))
	expectNumeric(t, cSvc, rSvc, "383", time.Now().Add(2*time.Second))

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// Wait for the burst to flush. Node B should have the service
	// in its world with Service=true and HomeServer=node-a.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srvB.world.FindByNick("chanserv"); u != nil {
			if !u.Service {
				t.Errorf("burst dropped Service flag")
			}
			if u.HomeServer != "node-a" {
				t.Errorf("HomeServer = %q, want node-a", u.HomeServer)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("service never propagated to node B")

	_ = addrB
}

func TestService_RuntimeRegistrationPropagates(t *testing.T) {
	addrA, srvA, closeA := buildFederationPeer(t, "node-a")
	defer closeA()
	_, srvB, closeB := buildFederationPeer(t, "node-b")
	defer closeB()

	closeLink := linkTwoServers(t, srvA, srvB)
	defer closeLink()

	// Now register a service on A — it should reach B via the
	// runtime announce path, not the burst.
	cSvc, rSvc := dialClient(t, addrA)
	defer cSvc.Close()
	cSvc.Write([]byte("SERVICE helper * * 0 0 :Helper Bot\r\n"))
	expectNumeric(t, cSvc, rSvc, "383", time.Now().Add(2*time.Second))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if u := srvB.world.FindByNick("helper"); u != nil {
			if !u.Service {
				t.Errorf("runtime announce dropped Service flag")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runtime service never propagated to node B")
}
