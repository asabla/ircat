// Package sqlite is the SQLite driver for [storage.Store].
//
// Uses modernc.org/sqlite (a pure-Go translation of sqlite, no CGo)
// so cross-compilation stays trivial. The driver opens one
// *sql.DB, runs migrations from the embedded migrations/ directory
// at startup, and constructs the per-table sub-stores against the
// shared connection pool.
//
// Schema migrations are forward-only and ordered by filename:
// 0001_init.sql is applied first, then 0002_..., and so on. The
// applied set is tracked in the schema_migrations table; running
// Migrate twice is a no-op.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/asabla/ircat/internal/storage"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the SQLite implementation of [storage.Store].
type Store struct {
	db *sql.DB

	operators *operatorStore
	tokens    *tokenStore
	bots      *botStore
	channels  *channelStore
	events    *eventStore
}

// Open returns a Store backed by the SQLite database at path.
// path may be ":memory:" for an in-process ephemeral database
// (used by unit tests).
//
// The connection is configured for WAL mode and busy_timeout =
// 5000 ms, which is what every IRC daemon survives a Linux fsync
// stall on.
func Open(path string) (*Store, error) {
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	// modernc.org/sqlite is concurrency-safe but performs best with
	// a small pool — connections are cheap but they all serialize on
	// the file lock anyway.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}
	s := &Store{db: db}
	s.operators = &operatorStore{db: db}
	s.tokens = &tokenStore{db: db}
	s.bots = &botStore{db: db}
	s.channels = &channelStore{db: db}
	s.events = &eventStore{db: db}
	return s, nil
}

// buildDSN composes the DSN string passed to sql.Open. We always
// enable WAL with synchronous=NORMAL plus a generous busy
// timeout; ":memory:" is passed through unchanged.
//
// WAL + synchronous=NORMAL is the standard SQLite production
// pairing. Quoting the upstream docs: "WAL mode with
// PRAGMA synchronous=NORMAL is safe from corruption and is
// generally as fast as synchronous=OFF". It fsyncs the WAL on
// checkpoint boundaries rather than every commit, which is the
// difference between ~8ms/Append (FULL) and ~150µs/Append
// (NORMAL) on a typical ext4 host. The tiny window of "lost
// writes on power loss" between checkpoints is acceptable for
// the audit log because every event is also pushed through the
// jsonl + webhook sinks at publish time, so the persistent
// store is not the only durability path.
func buildDSN(path string) string {
	if path == ":memory:" {
		return "file::memory:?cache=shared&_pragma=journal_mode(memory)&_pragma=busy_timeout(5000)"
	}
	return fmt.Sprintf(
		"file:%s?_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)",
		path,
	)
}

// Operators returns the operator account store.
func (s *Store) Operators() storage.OperatorStore { return s.operators }

// APITokens returns the API token store.
func (s *Store) APITokens() storage.TokenStore { return s.tokens }

// Bots returns the bot definition store.
func (s *Store) Bots() storage.BotStore { return s.bots }

// Channels returns the persistent channel state store.
func (s *Store) Channels() storage.PersistentChannelStore { return s.channels }

// Events returns the audit log store.
func (s *Store) Events() storage.EventStore { return s.events }

// Close releases the underlying database connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// Migrate applies any pending schema migrations from the embedded
// migrations/ directory. Idempotent.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		var exists string
		err := s.db.QueryRowContext(ctx,
			`SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		switch {
		case err == nil:
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := s.db.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}
