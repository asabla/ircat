package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// MetricsSource is the small read-only surface the /metrics
// handler pulls from. It is implemented by internal/server.Server
// alongside PageActuator and PageServerInfo, so the dashboard does
// not need a separate adapter type. Each method is intentionally
// cheap — counters and gauges, no aggregation — because the
// scrape budget for /metrics is single-digit milliseconds in a
// healthy deployment.
type MetricsSource interface {
	// UserCount returns the number of registered local users
	// (i.e. excluding remote federation users).
	UserCount() int
	// ChannelCount returns the number of channels currently
	// tracked by the world.
	ChannelCount() int
	// FederationLinkCount returns the number of currently
	// registered (Active) federation links.
	FederationLinkCount() int
	// BotCount returns the number of registered bots.
	BotCount() int
	// MessagesIn returns the total number of inbound IRC messages
	// the server has parsed since startup.
	MessagesIn() uint64
	// MessagesOut returns the total number of outbound IRC
	// messages the server has written since startup.
	MessagesOut() uint64
	// StartedAt returns the server start time.
	StartedAt() time.Time
}

// handleMetrics writes a Prometheus text-format response. The
// shape is hand-rolled rather than pulling in
// prometheus_client_golang because the surface is tiny and the
// stdlib-first principle in CLAUDE.md is louder than the
// ergonomic gain.
//
// Endpoint is unauthenticated by design: prometheus does not
// authenticate scrapes, and the recommended deployment is to
// firewall /metrics off the public internet. The dashboard
// listener already lives behind the operator network in the
// reference compose stack.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	src := s.metrics
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if src == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "# metrics source unavailable\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	emit := func(name, help, kind string, value any) {
		fmt.Fprintf(w, "# HELP %s %s\n", name, help)
		fmt.Fprintf(w, "# TYPE %s %s\n", name, kind)
		switch v := value.(type) {
		case int:
			fmt.Fprintf(w, "%s %d\n", name, v)
		case int64:
			fmt.Fprintf(w, "%s %d\n", name, v)
		case uint64:
			fmt.Fprintf(w, "%s %d\n", name, v)
		case float64:
			fmt.Fprintf(w, "%s %s\n", name, strconv.FormatFloat(v, 'g', -1, 64))
		}
	}
	emit("ircat_users", "Registered local IRC users.", "gauge", src.UserCount())
	emit("ircat_channels", "Tracked channels.", "gauge", src.ChannelCount())
	emit("ircat_federation_links", "Active federation links.", "gauge", src.FederationLinkCount())
	emit("ircat_bots", "Registered Lua bots.", "gauge", src.BotCount())
	emit("ircat_messages_in_total", "Total inbound IRC messages parsed.", "counter", src.MessagesIn())
	emit("ircat_messages_out_total", "Total outbound IRC messages written.", "counter", src.MessagesOut())
	emit("ircat_uptime_seconds", "Time since server start.", "gauge", time.Since(src.StartedAt()).Seconds())
}
