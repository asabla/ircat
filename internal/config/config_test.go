package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalYAML = `
version: 1
server:
  name: irc.local
  description: dev
  network: devnet
  listeners:
    - address: "0.0.0.0:6667"
      tls: false
storage:
  driver: sqlite
  sqlite:
    path: /tmp/ircat.db
dashboard:
  enabled: true
  address: "0.0.0.0:8080"
`

func TestLoadJSON_Minimal(t *testing.T) {
	in := []byte(`{
		"version": 1,
		"server": {
			"name": "irc.local",
			"network": "devnet",
			"listeners": [{"address": "0.0.0.0:6667"}]
		},
		"storage": {"driver": "sqlite", "sqlite": {"path": "/tmp/ircat.db"}}
	}`)
	cfg, err := LoadJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.Server.Name != "irc.local" {
		t.Errorf("name = %q", cfg.Server.Name)
	}
	if cfg.Server.Limits.PingIntervalSeconds != 120 {
		t.Errorf("default ping interval = %d", cfg.Server.Limits.PingIntervalSeconds)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level = %q", cfg.Logging.Level)
	}
}

func TestLoadJSON_UnknownField(t *testing.T) {
	in := []byte(`{"version":1,"server":{"name":"x","network":"y","not_a_field":1,"listeners":[{"address":"127.0.0.1:6667"}]},"storage":{"driver":"sqlite","sqlite":{"path":"/x"}}}`)
	if _, err := LoadJSON(in); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadYAML_Minimal(t *testing.T) {
	cfg, err := LoadYAML([]byte(minimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := cfg.Server.Listeners[0].Address; got != "0.0.0.0:6667" {
		t.Errorf("listener = %q", got)
	}
	if !cfg.Dashboard.Enabled {
		t.Errorf("dashboard not enabled")
	}
}

func TestLoad_FromFile_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ircat.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Name != "irc.local" {
		t.Errorf("name = %q", cfg.Server.Name)
	}
}

func TestLoad_RelativePathResolution(t *testing.T) {
	dir := t.TempDir()
	yaml := `
version: 1
server:
  name: irc.local
  network: devnet
  motd_file: motd.txt
  listeners:
    - address: "0.0.0.0:6667"
storage:
  driver: sqlite
  sqlite:
    path: ircat.db
`
	path := filepath.Join(dir, "ircat.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	wantMOTD := filepath.Join(dir, "motd.txt")
	if cfg.Server.MOTDFile != wantMOTD {
		t.Errorf("MOTDFile = %q, want %q", cfg.Server.MOTDFile, wantMOTD)
	}
	wantDB := filepath.Join(dir, "ircat.db")
	if cfg.Storage.SQLite.Path != wantDB {
		t.Errorf("sqlite.Path = %q, want %q", cfg.Storage.SQLite.Path, wantDB)
	}
}

func TestValidate_RequiresServerName(t *testing.T) {
	cfg := &Config{Version: 1}
	cfg.applyDefaults()
	cfg.Server.Network = "x"
	cfg.Server.Listeners = []Listener{{Address: "0.0.0.0:6667"}}
	cfg.Storage.SQLite.Path = "/x"
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "server.name") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidate_RejectsBadListener(t *testing.T) {
	cfg := minimalLoaded(t)
	cfg.Server.Listeners[0].Address = "not-a-host-port"
	if err := cfg.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestValidate_TLSListenerNeedsCert(t *testing.T) {
	cfg := minimalLoaded(t)
	cfg.Server.Listeners = append(cfg.Server.Listeners, Listener{
		Address: "0.0.0.0:6697",
		TLS:     true,
	})
	if err := cfg.Validate(); !strings.Contains(err.Error(), "cert_file") {
		t.Fatalf("expected cert_file error, got %v", err)
	}
}

func TestValidate_PostgresNeedsDSN(t *testing.T) {
	cfg := minimalLoaded(t)
	cfg.Storage.Driver = "postgres"
	cfg.Storage.SQLite = SQLiteConfig{}
	if err := cfg.Validate(); !strings.Contains(err.Error(), "postgres.dsn") {
		t.Fatalf("expected postgres.dsn error, got %v", err)
	}
}

func TestResolveEnv_PullsSecrets(t *testing.T) {
	cfg := minimalLoaded(t)
	cfg.Storage.Driver = "postgres"
	cfg.Storage.SQLite = SQLiteConfig{}
	cfg.Storage.Postgres.DSNEnv = "TEST_DSN"
	lookup := map[string]string{"TEST_DSN": "postgres://x"}
	if err := cfg.resolveEnv(func(k string) string { return lookup[k] }); err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Postgres.DSN != "postgres://x" {
		t.Errorf("dsn = %q", cfg.Storage.Postgres.DSN)
	}
}

func TestResolveEnv_MissingValueIsError(t *testing.T) {
	cfg := minimalLoaded(t)
	cfg.Auth.InitialAdmin.PasswordEnv = "ABSENT"
	err := cfg.resolveEnv(func(string) string { return "" })
	if err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// minimalLoaded returns a defaults-applied config that passes validation.
// Tests can mutate fields and re-validate.
func minimalLoaded(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadYAML([]byte(minimalYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}
	return cfg
}
