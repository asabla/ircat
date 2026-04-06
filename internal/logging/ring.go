package logging

import (
	"log/slog"
	"sync"
	"time"
)

// Entry is a captured log record stored in the ring buffer.
//
// It is intentionally small and self-contained so the dashboard can
// JSON-encode it directly without re-walking slog's record API.
type Entry struct {
	Seq     uint64         `json:"seq"`
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// RingBuffer is a fixed-capacity, thread-safe in-memory log tail.
//
// Entries are addressed by a monotonically increasing sequence number;
// readers can ask "give me everything since seq N" and get back any
// entries that have not yet been overwritten. Once the buffer fills,
// writes overwrite the oldest entry — slow readers see a gap (their
// next call returns the new oldest entry) but the writer is never
// blocked.
type RingBuffer struct {
	mu       sync.RWMutex
	capacity int
	entries  []Entry
	// next is the index in entries that the next Append will write to.
	next int
	// total counts every Append ever made; (total - capacity) is the
	// sequence number of the oldest entry currently held.
	total uint64
}

// NewRingBuffer constructs a buffer with the given capacity.
// A non-positive capacity falls back to [DefaultRingEntries].
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultRingEntries
	}
	return &RingBuffer{
		capacity: capacity,
		entries:  make([]Entry, capacity),
	}
}

// Capacity returns the maximum number of entries the buffer holds.
func (r *RingBuffer) Capacity() int { return r.capacity }

// Len returns the number of entries currently in the buffer.
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.total < uint64(r.capacity) {
		return int(r.total)
	}
	return r.capacity
}

// Append stores e in the buffer, assigning it a sequence number.
//
// The assigned sequence number is written into e.Seq before storage.
// Append never blocks and never returns an error.
func (r *RingBuffer) Append(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	e.Seq = r.total
	r.entries[r.next] = e
	r.next = (r.next + 1) % r.capacity
}

// Snapshot returns the entries currently in the buffer in chronological
// order (oldest first). The returned slice is a copy and is safe to
// retain or mutate.
func (r *RingBuffer) Snapshot() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

// Since returns every entry whose sequence number is greater than seq,
// in chronological order. If the requested sequence is older than the
// oldest entry held (because it was overwritten), the caller still gets
// every entry currently in the buffer — they will see a gap, but no
// silent loss.
func (r *RingBuffer) Since(seq uint64) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.snapshotLocked()
	// Binary search would work but the ring is small (10k by default)
	// and snapshots are linear anyway; a single forward scan is fine.
	for i, e := range all {
		if e.Seq > seq {
			out := make([]Entry, len(all)-i)
			copy(out, all[i:])
			return out
		}
	}
	return nil
}

// snapshotLocked returns the entries in chronological order. The
// caller must hold r.mu (read or write).
func (r *RingBuffer) snapshotLocked() []Entry {
	count := r.capacity
	if r.total < uint64(r.capacity) {
		count = int(r.total)
	}
	out := make([]Entry, count)
	if count == 0 {
		return out
	}
	// The oldest entry sits at r.next when the buffer has wrapped.
	// Before wrap, oldest is at index 0 and r.next == count.
	start := 0
	if r.total >= uint64(r.capacity) {
		start = r.next
	}
	for i := 0; i < count; i++ {
		out[i] = r.entries[(start+i)%r.capacity]
	}
	return out
}

// recordToEntry converts a slog.Record into an Entry, flattening
// attributes into a single map. Group attributes are stored as nested
// maps so they round-trip cleanly through JSON.
func recordToEntry(rec slog.Record) Entry {
	e := Entry{
		Time:    rec.Time,
		Level:   rec.Level.String(),
		Message: rec.Message,
	}
	if rec.NumAttrs() == 0 {
		return e
	}
	attrs := make(map[string]any, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = attrValue(a.Value)
		return true
	})
	e.Attrs = attrs
	return e
}

func attrValue(v slog.Value) any {
	v = v.Resolve()
	if v.Kind() == slog.KindGroup {
		group := v.Group()
		out := make(map[string]any, len(group))
		for _, a := range group {
			out[a.Key] = attrValue(a.Value)
		}
		return out
	}
	return v.Any()
}
