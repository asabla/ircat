package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// BenchmarkEvents_AppendSerial measures the per-Append latency
// of the Postgres audit-event store. Skips if
// IRCAT_TEST_POSTGRES_DSN is unset (the same convention the
// integration tests use), so `go test ./...` runs cleanly on
// machines without a running Postgres.
func BenchmarkEvents_AppendSerial(b *testing.B) {
	s := newBenchStore(b)
	store := s.Events()
	ctx := context.Background()
	now := time.Now().UTC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ev := &storage.AuditEvent{
			ID:        randomULID(),
			Timestamp: now,
			Type:      "kick",
			Actor:     "alice!alice@host",
			Target:    "bob",
			DataJSON:  `{"reason":"flood"}`,
		}
		if err := store.Append(ctx, ev); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvents_AppendParallel measures throughput when many
// goroutines append concurrently. Postgres scales connection
// pool permitting; the benchmark exercises the same path the
// production audit hot path uses.
func BenchmarkEvents_AppendParallel(b *testing.B) {
	s := newBenchStore(b)
	store := s.Events()
	ctx := context.Background()
	now := time.Now().UTC()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ev := &storage.AuditEvent{
				ID:        randomULID(),
				Timestamp: now,
				Type:      "kick",
				Actor:     "alice!alice@host",
				Target:    "bob",
				DataJSON:  `{"reason":"flood"}`,
			}
			if err := store.Append(ctx, ev); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func newBenchStore(b *testing.B) *Store {
	b.Helper()
	dsn := os.Getenv("IRCAT_TEST_POSTGRES_DSN")
	if dsn == "" {
		b.Skip("IRCAT_TEST_POSTGRES_DSN not set; skipping Postgres benchmarks")
	}
	s, err := Open(dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	if _, err := s.db.ExecContext(context.Background(), "DELETE FROM audit_events"); err != nil {
		b.Fatal(err)
	}
	return s
}

func randomULID() string {
	var buf [13]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
