package bots

import (
	"sync"
	"time"
)

// DefaultBotLogCapacity is the per-bot log tail size. Picked to
// match the task brief (~1000 lines) and to stay small enough
// that even a fleet of 100 bots keeps log memory in the tens of
// MBs at worst. Operators who want a longer tail can widen this
// later; it is a package constant rather than a field so the
// dashboard does not need to grow a config knob.
const DefaultBotLogCapacity = 1000

// BotLogEntry is one captured log line from a bot's ctx:log()
// call. It is addressed by a monotonically increasing sequence
// number so SSE consumers can poll "give me everything since
// seq N" against the ring without missing entries in the normal
// case.
//
// The four methods mirror the shape of
// internal/dashboard.BotLogEntry so the dashboard package can
// consume values of this type via a small read-only interface
// without importing internal/bots directly. Keeping the getters
// as methods on the concrete type (rather than on *BotLogEntry)
// means the slice returned by the ring buffer is cheap to hand
// out and satisfies the interface without pointer juggling.
type BotLogEntry struct {
	Seq     uint64
	Time    time.Time
	Level   string
	Message string
}

// Sequence / Timestamp / LevelName / MessageText satisfy the
// internal/dashboard.BotLogEntry interface.
func (e BotLogEntry) Sequence() uint64     { return e.Seq }
func (e BotLogEntry) Timestamp() time.Time { return e.Time }
func (e BotLogEntry) LevelName() string    { return e.Level }
func (e BotLogEntry) MessageText() string  { return e.Message }

// botLogRing is a small fixed-capacity ring buffer the
// per-bot session uses to keep the most recent N log lines.
// Lifted from internal/logging.RingBuffer but kept tiny and
// package-private so it can evolve without dragging the
// observability plumbing along.
type botLogRing struct {
	mu       sync.RWMutex
	capacity int
	entries  []BotLogEntry
	next     int
	total    uint64
}

func newBotLogRing(capacity int) *botLogRing {
	if capacity <= 0 {
		capacity = DefaultBotLogCapacity
	}
	return &botLogRing{
		capacity: capacity,
		entries:  make([]BotLogEntry, capacity),
	}
}

// append stores one entry, assigning it a sequence number.
// Never blocks and never returns an error. Overwrites the
// oldest entry once the ring is full.
func (r *botLogRing) append(level, message string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	r.entries[r.next] = BotLogEntry{
		Seq:     r.total,
		Time:    at,
		Level:   level,
		Message: message,
	}
	r.next = (r.next + 1) % r.capacity
}

// since returns every entry whose sequence number is greater
// than seq, in chronological order. If the requested sequence
// is older than the oldest entry currently held, the caller
// still gets every entry in the buffer.
func (r *botLogRing) since(seq uint64) []BotLogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := r.capacity
	if r.total < uint64(r.capacity) {
		count = int(r.total)
	}
	if count == 0 {
		return nil
	}
	start := 0
	if r.total >= uint64(r.capacity) {
		start = r.next
	}
	// Walk in chronological order and copy anything strictly
	// newer than seq. A forward scan is fine for 1000 entries;
	// it also keeps the code trivial versus a binary-searched
	// slice path.
	out := make([]BotLogEntry, 0, count)
	for i := 0; i < count; i++ {
		e := r.entries[(start+i)%r.capacity]
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
