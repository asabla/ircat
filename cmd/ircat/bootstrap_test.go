package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/logging"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

func newBootstrapStore(t *testing.T) (*sqlite.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, func() { _ = s.Close() }
}

func newBootstrapLogger(t *testing.T) *config.Config {
	// helper that just exists to avoid a missing import
	return nil
}

func TestBootstrap_InitialAdminCreatedOnEmptyStore(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()

	cfg := &config.Config{
		Auth: config.AuthConfig{
			PasswordHash: "argon2id",
			InitialAdmin: config.InitialAdminConfig{
				Username: "admin",
				Password: "secret",
			},
		},
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})

	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	got, err := store.Operators().Get(context.Background(), "admin")
	if err != nil {
		t.Fatalf("admin not created: %v", err)
	}
	ok, err := auth.Verify(got.PasswordHash, "secret")
	if err != nil || !ok {
		t.Errorf("password hash does not verify: %v %v", ok, err)
	}
}

func TestBootstrap_InitialAdminSkippedWhenStoreNotEmpty(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()

	// Pre-create an operator so the store is non-empty.
	hash, err := auth.Hash(auth.AlgorithmArgon2id, "preset", auth.Argon2idParams{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Operators().Create(context.Background(), &storage.Operator{
		Name:         "preset",
		PasswordHash: hash,
	}); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Auth: config.AuthConfig{
			PasswordHash: "argon2id",
			InitialAdmin: config.InitialAdminConfig{
				Username: "admin",
				Password: "secret",
			},
		},
	}
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Operators().Get(context.Background(), "admin"); err == nil {
		t.Errorf("admin was created even though store was non-empty")
	}
}

func TestBootstrap_InitialAdminEmptyConfigIsNoop(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()
	logger, _, _ := logging.New(logging.Options{Format: "text"})
	cfg := &config.Config{}
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	all, _ := store.Operators().List(context.Background())
	if len(all) != 0 {
		t.Errorf("operators table should be empty: %v", all)
	}
}

func TestBootstrap_StaticOperatorsUpserted(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()
	logger, _, _ := logging.New(logging.Options{Format: "text"})

	hash, _ := auth.Hash(auth.AlgorithmArgon2id, "static", auth.Argon2idParams{})
	cfg := &config.Config{
		Operators: []config.OperatorConfig{
			{
				Name:         "alice",
				HostMask:     "*@10.0.0.*",
				PasswordHash: hash,
				Flags:        []string{"kill", "rehash"},
			},
		},
	}

	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	got, err := store.Operators().Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.HostMask != "*@10.0.0.*" || len(got.Flags) != 2 {
		t.Errorf("static operator not synced: %#v", got)
	}

	// Update the static config and re-run; the existing record should
	// be updated rather than rejected.
	cfg.Operators[0].HostMask = "*@10.0.0.1"
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	got2, _ := store.Operators().Get(context.Background(), "alice")
	if got2.HostMask != "*@10.0.0.1" {
		t.Errorf("static operator not updated: %#v", got2)
	}
}

// captureLogger returns a logger writing into a bytes.Buffer
// so the bootstrap warn-path test can assert on the rendered
// log line. Uses the production logging.New so the slog
// handler chain matches what runs in cmd/ircat at startup.
func captureLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger, _, err := logging.New(logging.Options{
		Format: "text",
		Level:  "info",
		Output: &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	return logger, &buf
}

func TestBootstrap_InitialAdminMissingPasswordWarns(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()
	logger, buf := captureLogger(t)

	cfg := &config.Config{
		Auth: config.AuthConfig{
			InitialAdmin: config.InitialAdminConfig{
				Username: "admin",
				// Password deliberately empty: simulates the
				// IRCAT_INITIAL_ADMIN_PASSWORD-unset case.
			},
		},
	}
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "initial admin bootstrap skipped") {
		t.Errorf("expected WARN about skipped bootstrap, got: %q", out)
	}
	if !strings.Contains(out, "auth.initial_admin.password") {
		t.Errorf("expected WARN to name the missing field, got: %q", out)
	}
	if !strings.Contains(out, "ircat operator add") {
		t.Errorf("expected WARN to point at the recovery command, got: %q", out)
	}
	// And critically: no operator should have been created.
	all, _ := store.Operators().List(context.Background())
	if len(all) != 0 {
		t.Errorf("operators table should still be empty: %v", all)
	}
}

func TestBootstrap_InitialAdminMissingUsernameWarns(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()
	logger, buf := captureLogger(t)

	cfg := &config.Config{
		Auth: config.AuthConfig{
			InitialAdmin: config.InitialAdminConfig{
				Password: "set-but-no-username",
			},
		},
	}
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "auth.initial_admin.username") {
		t.Errorf("expected WARN to name the missing username, got: %q", out)
	}
}

func TestBootstrap_InitialAdminMissingFieldsAreSilentWhenStoreNotEmpty(t *testing.T) {
	store, cleanup := newBootstrapStore(t)
	defer cleanup()
	// Pre-populate the operators table; the bootstrap should
	// short-circuit before the warn check.
	hash, _ := auth.Hash(auth.AlgorithmArgon2id, "preexisting", auth.Argon2idParams{})
	if err := store.Operators().Create(context.Background(), &storage.Operator{
		Name: "carol", PasswordHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	logger, buf := captureLogger(t)
	cfg := &config.Config{
		Auth: config.AuthConfig{
			InitialAdmin: config.InitialAdminConfig{Username: "admin"},
		},
	}
	if err := bootstrapStore(context.Background(), store, cfg, logger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "initial admin bootstrap skipped") {
		t.Errorf("warn fired even though operators table was non-empty: %q", buf.String())
	}
}

// silence unused warning
var _ = newBootstrapLogger
