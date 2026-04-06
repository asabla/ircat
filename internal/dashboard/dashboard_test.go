package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/config"
)

// startTestDashboard brings up a Server bound to a random localhost
// port and returns the URL prefix plus a teardown function. Each
// test gets its own dashboard.
func startTestDashboard(t *testing.T, ready func() error) (string, func()) {
	t.Helper()
	cfg := &config.Config{
		Dashboard: config.DashboardConfig{
			Enabled: true,
			Address: "127.0.0.1:0",
		},
	}
	srv := New(Options{
		Config:    cfg,
		ReadyFunc: ready,
	})
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
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("dashboard did not stop")
				}
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("dashboard did not bind")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHealthz_OK(t *testing.T) {
	base, teardown := startTestDashboard(t, nil)
	defer teardown()

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "\"ok\"") {
		t.Errorf("body = %s", body)
	}
}

func TestReadyz_OK(t *testing.T) {
	base, teardown := startTestDashboard(t, nil)
	defer teardown()
	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestReadyz_NotReady(t *testing.T) {
	base, teardown := startTestDashboard(t, func() error {
		return errors.New("warming up")
	})
	defer teardown()
	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "warming up") {
		t.Errorf("body = %s", body)
	}
}

func TestRoot_HTML(t *testing.T) {
	base, teardown := startTestDashboard(t, nil)
	defer teardown()
	resp, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %s", ct)
	}
}

func TestUnknownRoute_404(t *testing.T) {
	base, teardown := startTestDashboard(t, nil)
	defer teardown()
	resp, err := http.Get(base + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDashboardDisabled_RunReturnsImmediately(t *testing.T) {
	cfg := &config.Config{
		Dashboard: config.DashboardConfig{Enabled: false},
	}
	srv := New(Options{Config: cfg})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
