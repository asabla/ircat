package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
	"github.com/asabla/ircat/tests/e2e/ircclient"
)

// eventSink is a small httptest server that collects every webhook
// POST so the e2e test can assert on the body.
type eventSink struct {
	mu      sync.Mutex
	events  []map[string]any
	hs      *httptest.Server
	wakeup  chan struct{}
}

func newEventSink(t *testing.T) *eventSink {
	t.Helper()
	s := &eventSink{wakeup: make(chan struct{}, 64)}
	s.hs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var ev map[string]any
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.events = append(s.events, ev)
		s.mu.Unlock()
		select {
		case s.wakeup <- struct{}{}:
		default:
		}
		w.WriteHeader(200)
	}))
	return s
}

func (s *eventSink) waitFor(t *testing.T, deadline time.Time, want func(map[string]any) bool) map[string]any {
	t.Helper()
	for {
		s.mu.Lock()
		for _, ev := range s.events {
			if want(ev) {
				out := ev
				s.mu.Unlock()
				return out
			}
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for event; seen %d", len(s.events))
		}
		select {
		case <-s.wakeup:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// startServerWithWebhook spawns the binary against a config that
// points its webhook sink at the supplied URL. Pre-seeds an
// admin operator + API token so the test can call the admin API.
func startServerWithWebhook(t *testing.T, webhookURL string) (ircAddr, dashURL, bearer string, teardown func()) {
	t.Helper()
	ircPort := pickFreePort(t)
	dashPort := pickFreePort(t)
	ircAddr = fmt.Sprintf("127.0.0.1:%d", ircPort)
	dashAddr := fmt.Sprintf("127.0.0.1:%d", dashPort)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	hash, _ := auth.Hash(auth.AlgorithmArgon2id, "admin-secret", auth.Argon2idParams{})
	tok, _ := auth.GenerateAPIToken()
	{
		store, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := store.Operators().Create(context.Background(), &storage.Operator{
			Name: "admin", HostMask: "", PasswordHash: hash, Flags: []string{"all"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.APITokens().Create(context.Background(), &storage.APIToken{
			ID: tok.ID, Label: "e2e", Hash: tok.Hash,
		}); err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
	}

	configPath := filepath.Join(dir, "ircat.yaml")
	cfg := fmt.Sprintf(`version: 1
server:
  name: irc.test
  network: TestNet
  description: e2e events
  listeners:
    - address: "%s"
      tls: false
  limits:
    nick_length: 30
    channel_length: 50
    topic_length: 390
    away_length: 255
    kick_reason_length: 255
    ping_interval_seconds: 5
    ping_timeout_seconds: 20
storage:
  driver: sqlite
  sqlite:
    path: %s
dashboard:
  enabled: true
  address: "%s"
events:
  sinks:
    - type: webhook
      url: "%s"
      timeout_seconds: 2
      retry:
        max_attempts: 2
        backoff_seconds:
          - 1
      dead_letter_path: "%s/dlq.jsonl"
logging:
  level: info
  format: text
`, ircAddr, dbPath, dashAddr, webhookURL, dir)
	if err := os.WriteFile(configPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath, "--config", configPath)
	cmd.Stdout = &testWriter{t: t, prefix: "ircat-stdout"}
	cmd.Stderr = &testWriter{t: t, prefix: "ircat-stderr"}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ircUp := dialReady(ircAddr)
		dashUp := dialReady(dashAddr)
		if ircUp && dashUp {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatal("ircat did not bind")
		}
		time.Sleep(50 * time.Millisecond)
	}
	teardown = func() {
		cancel()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("ircat did not stop")
		}
	}
	return ircAddr, "http://" + dashAddr, tok.Plaintext, teardown
}

func TestE2E_WebhookSinkReceivesAuditEvents(t *testing.T) {
	sink := newEventSink(t)
	defer sink.hs.Close()

	_, dash, bearer, teardown := startServerWithWebhook(t, sink.hs.URL)
	defer teardown()

	// Create an operator via the admin API. This triggers the
	// audit log path for admin_action (the dashboard would produce
	// the same event for a human operator creating one from the
	// UI). Wait for the webhook sink to receive the matching
	// event.
	//
	// Actually, create an operator does not currently emit an
	// audit event; we need an audit-producing action. OPER via
	// IRC does emit oper_up. But we do not want to connect IRC
	// just for this. Instead, hit a kick endpoint against a live
	// user.
	//
	// Simplest: connect an IRC user, kick them via the API, and
	// wait for the admin_action audit event.

	ircClient, err := ircclient.Dial(strings.TrimPrefix(dash, "http://"), time.Second)
	if err == nil {
		ircClient.Close() // just checking dash addr parses right
	}

	// Parse the IRC addr from the teardown scope via the return
	// shape. We have dash; the IRC addr was returned as the first
	// value (ignored). Re-dial from the same config by asking the
	// dashboard for the server info.
	resp, err := httpGet(dash+"/api/v1/server", bearer)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	var info struct {
		Listeners []string `json:"listeners"`
	}
	_ = json.Unmarshal(resp, &info)
	if len(info.Listeners) == 0 {
		t.Fatal("no listeners reported")
	}
	ircAddr := info.Listeners[0]

	// Connect a victim to IRC so the API kick has a target.
	victim, err := ircclient.Dial(ircAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	if err := victim.Register("victim", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Kick via the admin API. The server's KickUser helper emits
	// an admin_action audit event that publishes to the bus and
	// reaches our webhook sink.
	kickURL := dash + "/api/v1/users/victim/kick"
	if err := httpPost(kickURL, bearer, `{"reason":"testing"}`); err != nil {
		t.Fatal(err)
	}

	ev := sink.waitFor(t, time.Now().Add(5*time.Second), func(m map[string]any) bool {
		return m["type"] == "admin_action"
	})
	if ev["target"] != "victim" {
		t.Errorf("target = %v", ev["target"])
	}
	if !strings.Contains(fmt.Sprint(ev["data"]), "testing") {
		t.Errorf("data = %v", ev["data"])
	}
}

func httpGet(url, bearer string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func httpPost(url, bearer, body string) error {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// silence unused
var _ = net.IPv4
