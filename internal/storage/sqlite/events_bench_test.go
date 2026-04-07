package sqlite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// BenchmarkEvents_AppendSerial measures the per-Append latency
// of the SQLite audit-event store on the hot path. The bench
// uses a fresh file-backed database (the same flow newTestStore
// uses) so the result reflects what an operator running ircat
// against an on-disk SQLite would see — not :memory:, which
// would skew the result by an order of magnitude.
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
// goroutines append concurrently. SQLite serializes writes via
// the WAL, so the bench is bounded by the WAL fsync rate. The
// reported number is per-call latency averaged across all
// parallel goroutines.
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

// newBenchStore is a non-testing.T helper for benchmarks. It
// mirrors newTestStore but takes *testing.B instead.
func newBenchStore(b *testing.B) *Store {
	b.Helper()
	dir := b.TempDir()
	s, err := Open(dir + "/ircat.db")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	return s
}

// randomULID returns a 26-byte hex-ish blob that satisfies the
// "non-empty ID" check on Append. Real ULIDs come from
// internal/events.NewID; we avoid that dep here to keep the
// benchmark in the storage layer alone.
func randomULID() string {
	var buf [13]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
