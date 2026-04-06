package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/events"
	"github.com/asabla/ircat/internal/logging"
)

func TestBuildEventBus_UnknownTypeErrors(t *testing.T) {
	cfg := &config.Config{
		Events: config.EventsConfig{
			Sinks: []config.SinkConfig{{Type: "nope"}},
		},
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	if _, err := buildEventBus(cfg, logger); err == nil {
		t.Fatal("expected error for unknown sink type")
	}
}

func TestBuildEventBus_DisabledSinkSkipped(t *testing.T) {
	disabled := false
	dir := t.TempDir()
	cfg := &config.Config{
		Events: config.EventsConfig{
			Sinks: []config.SinkConfig{
				{
					Type:    "jsonl",
					Enabled: &disabled,
					Path:    filepath.Join(dir, "skipped.jsonl"),
				},
				{
					Type: "jsonl",
					Path: filepath.Join(dir, "active.jsonl"),
				},
			},
		},
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	bus, err := buildEventBus(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Publish one event and wait for the bus to close — only the
	// active sink should have written a line.
	bus.Publish(events.Event{ID: "1", Type: "test"})
	_ = bus.Close()

	if _, err := os.Stat(filepath.Join(dir, "skipped.jsonl")); err == nil {
		t.Errorf("disabled sink file was created")
	}
	body, err := os.ReadFile(filepath.Join(dir, "active.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"id":"1"`) {
		t.Errorf("active sink missing event: %s", body)
	}
}

func TestBuildEventBus_JSONLAndWebhookConstruct(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Events: config.EventsConfig{
			Sinks: []config.SinkConfig{
				{
					Type:     "jsonl",
					Path:     filepath.Join(dir, "events.jsonl"),
					RotateMB: 10,
					Keep:     3,
				},
				{
					Type:           "webhook",
					URL:            "http://127.0.0.1:1/ignored",
					TimeoutSeconds: 1,
					Retry: &config.RetryBlock{
						MaxAttempts:    2,
						BackoffSeconds: []int{1},
					},
					DeadLetterPath: filepath.Join(dir, "dlq.jsonl"),
				},
			},
		},
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	bus, err := buildEventBus(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	// Publish one event. The jsonl sink will write it; the webhook
	// sink will fail to connect (port 1 refuses) and fall through
	// to the DLQ. Both paths are exercised.
	bus.Publish(events.Event{ID: "2", Type: "admin_action"})
	_ = bus.Close()

	body, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var ev events.Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(body))), &ev); err != nil {
		t.Fatalf("jsonl body: %v", err)
	}
	if ev.ID != "2" {
		t.Errorf("jsonl id = %q", ev.ID)
	}
	// DLQ should have the failed webhook delivery.
	if _, err := os.Stat(filepath.Join(dir, "dlq.jsonl")); err != nil {
		t.Errorf("dlq missing: %v", err)
	}
}

// TestBuildEventBus_LoadFromDocConfigExample verifies that the
// inline backoff_seconds syntax the docs advertise (copied here)
// parses and constructs cleanly through the same pipeline a real
// config file would travel.
func TestBuildEventBus_LoadFromDocConfigExample(t *testing.T) {
	dir := t.TempDir()
	yamlBody := `version: 1
server:
  name: irc.test
  network: TestNet
  listeners:
    - address: "127.0.0.1:0"
storage:
  driver: sqlite
  sqlite:
    path: ` + filepath.Join(dir, "ircat.db") + `
events:
  sinks:
    - type: jsonl
      path: ` + filepath.Join(dir, "events.jsonl") + `
      rotate_mb: 100
      keep: 7
    - type: webhook
      url: https://hooks.example.org/ircat
      timeout_seconds: 5
      retry:
        max_attempts: 5
        backoff_seconds: [1, 2, 5, 15, 60]
`
	path := filepath.Join(dir, "ircat.yaml")
	if err := os.WriteFile(path, []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Events.Sinks) != 2 {
		t.Fatalf("sinks = %d", len(cfg.Events.Sinks))
	}
	retry := cfg.Events.Sinks[1].Retry
	if retry == nil {
		t.Fatal("retry missing")
	}
	if len(retry.BackoffSeconds) != 5 || retry.BackoffSeconds[0] != 1 || retry.BackoffSeconds[4] != 60 {
		t.Errorf("backoff_seconds = %v", retry.BackoffSeconds)
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	bus, err := buildEventBus(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	_ = bus.Close()
	// silence unused
	_ = context.Background
}
