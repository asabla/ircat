package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
	"github.com/asabla/ircat/tests/e2e/ircclient"
)

// startServerWithBot pre-seeds a single enabled bot with the
// supplied source and spawns the binary against it. Returns the
// IRC addr and a teardown.
func startServerWithBot(t *testing.T, botName, botSource string) (ircAddr string, teardown func()) {
	t.Helper()
	ircPort := pickFreePort(t)
	ircAddr = fmt.Sprintf("127.0.0.1:%d", ircPort)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")

	// Seed the bot.
	{
		store, err := sqlite.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := store.Bots().Create(context.Background(), &storage.Bot{
			ID:      "bot_test",
			Name:    botName,
			Source:  botSource,
			Enabled: true,
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
  description: e2e bots
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
  enabled: false
  address: "127.0.0.1:0"
logging:
  level: info
  format: text
`, ircAddr, dbPath)
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
		if c, err := net.DialTimeout("tcp", ircAddr, 100*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatalf("ircat did not bind %s", ircAddr)
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
	return ircAddr, teardown
}

const pingPongBot = `
function init(ctx)
  ctx:join("#demo")
end

function on_command(ctx, event)
  if event.name == "ping" then
    ctx:say(event.channel, "pong")
  end
end
`

func TestE2E_BotPingPong(t *testing.T) {
	addr, teardown := startServerWithBot(t, "botty", pingPongBot)
	defer teardown()

	// Give the supervisor a moment to boot the bot and run init().
	time.Sleep(200 * time.Millisecond)

	c, err := ircclient.Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Register("alice", time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	// Join #demo. The bot should already be in it because init()
	// ran at supervisor start. NAMES should include botty.
	if err := c.Send("JOIN #demo"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	_, trace, err := c.Expect(deadline, func(line string) bool {
		return strings.Contains(line, " 353 ") && strings.Contains(line, "botty")
	})
	if err != nil {
		t.Fatalf("NAMES missing botty: %v\n trace: %v", err, trace)
	}

	// Send !ping and expect the bot to reply.
	if err := c.Send("PRIVMSG #demo :!ping"); err != nil {
		t.Fatal(err)
	}
	_, trace, err = c.Expect(deadline, func(line string) bool {
		return strings.HasPrefix(line, ":botty!") &&
			strings.Contains(line, " PRIVMSG #demo ") &&
			strings.HasSuffix(line, ":pong")
	})
	if err != nil {
		t.Fatalf("bot pong not seen: %v\n trace: %v", err, trace)
	}
}
