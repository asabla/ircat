package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// FederationLister is the read-only surface the federation links
// endpoint consults. internal/server.Server satisfies it.
// Optional — when nil the endpoint returns an empty array.
type FederationLister interface {
	FederationSnapshot() []FederationLinkRow
}

// FederationLinkRow is the interface each link snapshot row must
// satisfy. Mirrors the server-side FederationLinkRow getters so
// the api package never imports internal/server.
type FederationLinkRow interface {
	Peer() string
	State() string
	SentMessages() uint64
	SentBytes() uint64
	RecvMessages() uint64
	RecvBytes() uint64
	OpenedAt() time.Time
}

// federationLinkJSON is the JSON envelope for one federation link.
type federationLinkJSON struct {
	Peer         string  `json:"peer"`
	State        string  `json:"state"`
	SentMessages uint64  `json:"sent_msgs"`
	SentKB       uint64  `json:"sent_kb"`
	RecvMessages uint64  `json:"recv_msgs"`
	RecvKB       uint64  `json:"recv_kb"`
	UptimeSecs   float64 `json:"uptime_secs"`
}

// handleListFederationLinks returns per-link byte and message
// counters for every active federation link. This is the JSON
// form of what STATS l exposes on the IRC wire.
func (a *API) handleListFederationLinks(w http.ResponseWriter, _ *http.Request) {
	var rows []FederationLinkRow
	if a.federation != nil {
		rows = a.federation.FederationSnapshot()
	}
	out := make([]federationLinkJSON, 0, len(rows))
	now := a.now()
	for _, r := range rows {
		uptime := 0.0
		if !r.OpenedAt().IsZero() {
			uptime = now.Sub(r.OpenedAt()).Seconds()
		}
		out = append(out, federationLinkJSON{
			Peer:         r.Peer(),
			State:        r.State(),
			SentMessages: r.SentMessages(),
			SentKB:       r.SentBytes() / 1024,
			RecvMessages: r.RecvMessages(),
			RecvKB:       r.RecvBytes() / 1024,
			UptimeSecs:   uptime,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
