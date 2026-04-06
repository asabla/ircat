package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
	"github.com/asabla/ircat/tests/e2e/ircclient"
)

// startServerWithDashboard spawns the binary against a config that
// enables both the IRC listener and the dashboard, and pre-seeds an
// operator + API token in the sqlite store. Returns the IRC addr,
// the dashboard URL, and the bearer token.
func startServerWithDashboard(t *testing.T) (ircAddr, dashURL, bearer string, teardown func()) {
	t.Helper()
	ircPort := pickFreePort(t)
	dashPort := pickFreePort(t)
	ircAddr = fmt.Sprintf("127.0.0.1:%d", ircPort)
	dashAddr := fmt.Sprintf("127.0.0.1:%d", dashPort)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Pre-seed the store with an admin operator and an API token.
	hash, err := auth.Hash(auth.AlgorithmArgon2id, "admin-secret", auth.Argon2idParams{})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	{
		store, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := store.Operators().Create(context.Background(), &storage.Operator{
			Name:         "admin",
			HostMask:     "",
			PasswordHash: hash,
			Flags:        []string{"all"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.APITokens().Create(context.Background(), &storage.APIToken{
			ID:    tok.ID,
			Label: "e2e",
			Hash:  tok.Hash,
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
  description: e2e api
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
logging:
  level: info
  format: text
`, ircAddr, dbPath, dashAddr)
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

	// Wait until both IRC and dashboard are accepting.
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
			t.Fatalf("ircat did not bind irc=%v dash=%v", ircUp, dashUp)
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
			t.Error("ircat did not stop within 5s")
		}
	}
	return ircAddr, "http://" + dashAddr, tok.Plaintext, teardown
}

func dialReady(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func apiCall(t *testing.T, method, url, bearer string, body any) (*http.Response, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, respBody
}

func TestE2E_API_GetServerWithToken(t *testing.T) {
	_, dash, bearer, teardown := startServerWithDashboard(t)
	defer teardown()

	resp, body := apiCall(t, "GET", dash+"/api/v1/server", bearer, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "irc.test") {
		t.Errorf("server name missing: %s", body)
	}
}

func TestE2E_API_CreateOperatorAndOper(t *testing.T) {
	ircAddr, dash, bearer, teardown := startServerWithDashboard(t)
	defer teardown()

	// Create a new operator via the API.
	resp, body := apiCall(t, "POST", dash+"/api/v1/operators", bearer, map[string]any{
		"name":     "alice",
		"password": "secret",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create: status %d body %s", resp.StatusCode, body)
	}

	// Connect to IRC and OPER as the new operator.
	c, err := ircclient.Dial(ircAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Register("alice", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c.Send("OPER alice secret"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ExpectNumeric("381", time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("oper: %v", err)
	}
}

func TestE2E_API_KickUser(t *testing.T) {
	ircAddr, dash, bearer, teardown := startServerWithDashboard(t)
	defer teardown()

	// Connect a regular user.
	c, err := ircclient.Dial(ircAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Register("victim", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Kick via the API.
	resp, body := apiCall(t, "POST", dash+"/api/v1/users/victim/kick", bearer, map[string]any{
		"reason": "testing kick",
	})
	if resp.StatusCode != 204 {
		t.Fatalf("kick: status %d body %s", resp.StatusCode, body)
	}

	// The IRC connection should receive ERROR + close.
	deadline := time.Now().Add(2 * time.Second)
	sawError := false
	for {
		line, err := c.ReadLine(deadline)
		if err != nil {
			if !sawError {
				t.Errorf("connection closed without ERROR: %v", err)
			}
			return
		}
		if strings.HasPrefix(line, "ERROR ") {
			sawError = true
		}
	}
}

func TestE2E_API_TokensRoundTrip(t *testing.T) {
	_, dash, bearer, teardown := startServerWithDashboard(t)
	defer teardown()

	// Mint a new token via the API.
	resp, body := apiCall(t, "POST", dash+"/api/v1/tokens", bearer, map[string]any{
		"label": "ci-bot",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create token: status %d body %s", resp.StatusCode, body)
	}
	var created struct {
		ID        string `json:"id"`
		Plaintext string `json:"plaintext"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Plaintext == "" {
		t.Fatal("plaintext not returned")
	}

	// Use the new token to call /server.
	resp, _ = apiCall(t, "GET", dash+"/api/v1/server", created.Plaintext, nil)
	if resp.StatusCode != 200 {
		t.Errorf("new token does not authenticate: %d", resp.StatusCode)
	}

	// Revoke it.
	resp, _ = apiCall(t, "DELETE", dash+"/api/v1/tokens/"+created.ID, bearer, nil)
	if resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}

	// Subsequent use of the revoked token must fail.
	resp, _ = apiCall(t, "GET", dash+"/api/v1/server", created.Plaintext, nil)
	if resp.StatusCode != 401 {
		t.Errorf("revoked token still works: %d", resp.StatusCode)
	}
}
