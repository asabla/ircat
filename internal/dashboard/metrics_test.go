package dashboard

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
)

// fakeMetricsSource is a static MetricsSource for the unit test.
// Every getter returns a hard-coded value so the test can assert
// on the rendered text format without race-prone setup.
type fakeMetricsSource struct {
	users, channels, fed, bots int
	in, out                    uint64
	startedAt                  time.Time
}

func (f *fakeMetricsSource) UserCount() int            { return f.users }
func (f *fakeMetricsSource) ChannelCount() int         { return f.channels }
func (f *fakeMetricsSource) FederationLinkCount() int  { return f.fed }
func (f *fakeMetricsSource) BotCount() int             { return f.bots }
func (f *fakeMetricsSource) MessagesIn() uint64        { return f.in }
func (f *fakeMetricsSource) MessagesOut() uint64       { return f.out }
func (f *fakeMetricsSource) StartedAt() time.Time      { return f.startedAt }

func startMetricsDashboard(t *testing.T, src MetricsSource) (string, func()) {
	t.Helper()
	cfg := &config.Config{
		Dashboard: config.DashboardConfig{
			Enabled: true,
			Address: "127.0.0.1:0",
		},
	}
	srv := New(Options{Config: cfg, Metrics: src})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if a := srv.Addr(); a != "" {
			return "http://" + a, func() {
				cancel()
				<-done
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("dashboard did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMetrics_PrometheusTextFormat(t *testing.T) {
	src := &fakeMetricsSource{
		users:     7,
		channels:  3,
		fed:       2,
		bots:      1,
		in:        12345,
		out:       67890,
		startedAt: time.Now().Add(-30 * time.Second),
	}
	base, teardown := startMetricsDashboard(t, src)
	defer teardown()

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	// Each named metric must appear with both HELP and TYPE
	// lines and the expected value.
	expectations := []struct {
		name, kind, value string
	}{
		{"ircat_users", "gauge", "7"},
		{"ircat_channels", "gauge", "3"},
		{"ircat_federation_links", "gauge", "2"},
		{"ircat_bots", "gauge", "1"},
		{"ircat_messages_in_total", "counter", "12345"},
		{"ircat_messages_out_total", "counter", "67890"},
	}
	for _, e := range expectations {
		if !strings.Contains(got, "# HELP "+e.name+" ") {
			t.Errorf("missing HELP for %s", e.name)
		}
		if !strings.Contains(got, "# TYPE "+e.name+" "+e.kind) {
			t.Errorf("missing TYPE %s %s", e.name, e.kind)
		}
		if !strings.Contains(got, e.name+" "+e.value+"\n") {
			t.Errorf("missing %s value %s; body=%q", e.name, e.value, got)
		}
	}
	if !strings.Contains(got, "ircat_uptime_seconds ") {
		t.Errorf("missing uptime; body=%q", got)
	}
}

func TestMetrics_NoSourceReturnsStub(t *testing.T) {
	base, teardown := startMetricsDashboard(t, nil)
	defer teardown()

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "metrics source unavailable") {
		t.Errorf("expected stub, got %q", body)
	}
}
