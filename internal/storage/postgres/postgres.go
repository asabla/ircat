// Package postgres is the PostgreSQL driver for [storage.Store].
//
// Uses jackc/pgx via its database/sql adapter so the SQL shape stays
// close to the sqlite driver — only the placeholder style ($1 vs ?)
// and a handful of column types differ.
//
// Postgres tests are gated on the IRCAT_TEST_POSTGRES_DSN environment
// variable. When the variable is not set, the tests skip cleanly so
// `go test ./...` still runs end-to-end on a developer machine that
// only has SQLite available. CI sets the DSN against a service
// container.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/asabla/ircat/internal/storage"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the Postgres implementation of [storage.Store].
type Store struct {
	db *sql.DB

	operators *operatorStore
	tokens    *tokenStore
	bots      *botStore
	channels  *channelStore
	events    *eventStore
}

// Open returns a Store backed by the Postgres database at dsn. The
// DSN format is whatever pgx accepts (the canonical postgres://
// URL or the libpq key=value form).
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	// Pool settings; can be tuned via config.Storage.Postgres later.
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	s := &Store{db: db}
	s.operators = &operatorStore{db: db}
	s.tokens = &tokenStore{db: db}
	s.bots = &botStore{db: db}
	s.channels = &channelStore{db: db}
	s.events = &eventStore{db: db}
	return s, nil
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
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies any pending schema migrations from the embedded
// migrations/ directory. Identical contract to the sqlite driver.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
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
			`SELECT version FROM schema_migrations WHERE version = $1`, version).Scan(&exists)
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
			`INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

// splitFlags / joinFlags handle the comma-separated flag list. The
// schema uses TEXT for portability across drivers.
func splitFlags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinFlags(flags []string) string {
	return strings.Join(flags, ",")
}

// isUniqueViolation checks for the postgres SQLSTATE 23505 (unique
// violation). pgx surfaces it via the standard PgError type.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps errors so a substring check is the most portable
	// approach across pgx major versions.
	s := err.Error()
	return strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "duplicate key")
}
