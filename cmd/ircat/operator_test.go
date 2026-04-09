package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/asabla/ircat/internal/storage/sqlite"
)

func TestExtractPositional_FlagsAfter(t *testing.T) {
	pos, rest, err := extractPositional([]string{"alice", "--config", "x.yaml", "--flags", "kill"})
	if err != nil {
		t.Fatal(err)
	}
	if pos != "alice" {
		t.Errorf("pos = %q, want alice", pos)
	}
	if !reflect.DeepEqual(rest, []string{"--config", "x.yaml", "--flags", "kill"}) {
		t.Errorf("rest = %v", rest)
	}
}

func TestExtractPositional_FlagsBeforeIsRejected(t *testing.T) {
	// Convention: positional first, flags after. A leading
	// flag is a usage error rather than a silent reorder.
	_, _, err := extractPositional([]string{"--config", "x.yaml", "alice"})
	if err == nil {
		t.Fatal("expected error for flag-before-positional")
	}
}

func TestExtractPositional_DoubleDashTerminator(t *testing.T) {
	pos, rest, err := extractPositional([]string{"--", "-weird-name", "--config", "x.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if pos != "-weird-name" {
		t.Errorf("pos = %q", pos)
	}
	if !reflect.DeepEqual(rest, []string{"--config", "x.yaml"}) {
		t.Errorf("rest = %v", rest)
	}
}

func TestExtractPositional_NoPositional(t *testing.T) {
	_, _, err := extractPositional(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// writeOperatorConfig writes a minimal sqlite-backed config
// the operator subcommand can load. Returns the path.
func writeOperatorConfig(t *testing.T, dbPath string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := `version: 1
server:
  name: op.test
  network: OpNet
  listeners:
    - address: "127.0.0.1:0"
      tls: false
storage:
  driver: sqlite
  sqlite:
    path: ` + dbPath + `
dashboard:
  enabled: false
auth:
  password_hash: argon2id
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOperatorAdd_PersistsHashedPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")
	cfgPath := writeOperatorConfig(t, dbPath)

	pwFile := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwFile, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runOperatorAdd([]string{
		"alice",
		"--config", cfgPath,
		"--password-file", pwFile,
		"--flags", "kill,kline",
		"--host-mask", "*@10.0.0.*",
	}); err != nil {
		t.Fatalf("add alice: %v", err)
	}

	// Reopen the store directly and verify the persisted shape.
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.Operators().Get(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.HostMask != "*@10.0.0.*" {
		t.Errorf("host mask = %q", got.HostMask)
	}
	if !reflect.DeepEqual(got.Flags, []string{"kill", "kline"}) {
		t.Errorf("flags = %v", got.Flags)
	}
	if got.PasswordHash == "" || got.PasswordHash == "hunter2" {
		t.Errorf("password not hashed: %q", got.PasswordHash)
	}

	// Re-run add against the existing record — should upsert,
	// not error.
	pwFile2 := filepath.Join(dir, "pw2")
	_ = os.WriteFile(pwFile2, []byte("freshpass"), 0o600)
	if err := runOperatorAdd([]string{
		"alice",
		"--config", cfgPath,
		"--password-file", pwFile2,
		"--flags", "admin",
	}); err != nil {
		t.Fatalf("upsert alice: %v", err)
	}
	got2, _ := store.Operators().Get(context.Background(), "alice")
	if !reflect.DeepEqual(got2.Flags, []string{"admin"}) {
		t.Errorf("flags after upsert = %v, want [admin]", got2.Flags)
	}
	if got2.PasswordHash == got.PasswordHash {
		t.Errorf("password hash should have rotated on upsert")
	}
}

func TestOperatorDelete_RemovesEntry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ircat.db")
	cfgPath := writeOperatorConfig(t, dbPath)

	pwFile := filepath.Join(dir, "pw")
	_ = os.WriteFile(pwFile, []byte("p"), 0o600)
	if err := runOperatorAdd([]string{
		"alice", "--config", cfgPath, "--password-file", pwFile,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runOperatorDelete([]string{"alice", "--config", cfgPath}); err != nil {
		t.Fatal(err)
	}
	store, _ := sqlite.Open(dbPath)
	defer store.Close()
	if _, err := store.Operators().Get(context.Background(), "alice"); err == nil {
		t.Errorf("alice still present after delete")
	}
}
