package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/server"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// writeReloadConfig writes a minimal-but-complete YAML config
// to dir/config.yaml using the supplied logging level. Used by
// the reload tests to flip a single field and re-load.
func writeReloadConfig(t *testing.T, dir, level, motdPath string) string {
	t.Helper()
	cfg := `version: 1
server:
  name: reload.test
  network: ReloadNet
  motd_file: ` + motdPath + `
  listeners:
    - address: "127.0.0.1:0"
      tls: false
storage:
  driver: sqlite
  sqlite:
    path: ` + filepath.Join(dir, "ircat.db") + `
logging:
  level: ` + level + `
  format: text
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReload_LoggingLevel ensures that flipping the level field
// in the config file and calling reloadConfig swaps the
// effective slog level on the running logger via the shared
// LevelVar.
func TestReload_LoggingLevel(t *testing.T) {
	dir := t.TempDir()
	motdPath := filepath.Join(dir, "motd.txt")
	cfgPath := writeReloadConfig(t, dir, "info", motdPath)

	// Open the same store the production startup uses, so the
	// operator-sync branch of reloadConfig has somewhere to
	// write to.
	store, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Construct a logger with a starting level of "info" via
	// NewWithLevel so we have a *slog.LevelVar to assert on.
	logger, _, levelVar, err := logging.NewWithLevel(logging.Options{
		Level:  "info",
		Format: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := levelVar.Level(); got != slog.LevelInfo {
		t.Fatalf("starting level = %v, want info", got)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	srv := server.New(cfg, world, logger, server.WithStore(store))

	deps := &reloadDeps{
		configPath: cfgPath,
		store:      store,
		srv:        srv,
		levelVar:   levelVar,
		logger:     logger,
	}

	// Rewrite the config to debug, then reload, then assert.
	writeReloadConfig(t, dir, "debug", motdPath)
	if err := deps.reloadConfig(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := levelVar.Level(); got != slog.LevelDebug {
		t.Errorf("after reload level = %v, want debug", got)
	}

	// Flip back to warn, reload again.
	writeReloadConfig(t, dir, "warn", motdPath)
	if err := deps.reloadConfig(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := levelVar.Level(); got != slog.LevelWarn {
		t.Errorf("after second reload level = %v, want warn", got)
	}
}

// TestReload_MOTDFile creates an initial MOTD, reloads (no-op),
// rewrites the file with new content, calls reloadConfig, and
// asserts the server's in-memory MOTD reflects the new content.
func TestReload_MOTDFile(t *testing.T) {
	dir := t.TempDir()
	motdPath := filepath.Join(dir, "motd.txt")
	if err := os.WriteFile(motdPath, []byte("first line\nsecond line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeReloadConfig(t, dir, "info", motdPath)

	store, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	logger, _, levelVar, _ := logging.NewWithLevel(logging.Options{Level: "info", Format: "text"})
	cfg, _ := config.Load(cfgPath)
	world := state.NewWorld()
	srv := server.New(cfg, world, logger, server.WithStore(store))

	// Initial load — the server should have read the original
	// content at New time. Force it via the same hook the
	// reload path uses so the test does not depend on internal
	// New-time bootstrapping.
	srv.ReloadMOTD()

	deps := &reloadDeps{
		configPath: cfgPath,
		store:      store,
		srv:        srv,
		levelVar:   levelVar,
		logger:     logger,
	}

	// Overwrite the MOTD file with new content and reload.
	if err := os.WriteFile(motdPath, []byte("brand new line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deps.reloadConfig(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Read the MOTD back via the exported helper. Server has no
	// public Snapshot() so we exercise the same code the welcome
	// burst hits.
	if got := srv.MOTDLines(); len(got) != 1 || got[0] != "brand new line" {
		t.Errorf("MOTD after reload = %#v, want [brand new line]", got)
	}
}
