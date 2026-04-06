package main

import (
	"context"
	"path/filepath"
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

// silence unused warning
var _ = newBootstrapLogger
