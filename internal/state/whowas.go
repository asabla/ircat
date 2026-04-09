package state

import (
	"sync"
	"time"
)

// WhowasEntry is one historical snapshot of a user as it existed at
// the moment they disconnected (or were renamed away from a nick).
// The fields mirror what 314 RPL_WHOWASUSER and 312 RPL_WHOISSERVER
// need: nick/user/host/realname plus the server they were on and the
// time the snapshot was taken.
//
// Snapshots are immutable copies, not pointers into the live World.
type WhowasEntry struct {
	Nick     string
	User     string
	Host     string
	Realname string
	Server   string
	When     time.Time
}

// Whowas is a fixed-capacity ring buffer of historical user entries
// indexed by case-folded nickname. RFC 2812 §3.6.3 describes the
// command but leaves retention entirely up to the implementation; we
// keep the most recent N entries globally and look them up by nick.
//
// Concurrency: all access goes through the internal mutex. The buffer
// is small (default 1024) so a linear scan on lookup is cheap.
type Whowas struct {
	mu      sync.Mutex
	mapping CaseMapping
	cap     int
	// entries is a circular buffer; head is the index of the next
	// write position. When the buffer is full, the oldest entry is
	// overwritten in place.
	entries []WhowasEntry
	head    int
	full    bool
}

// NewWhowas builds an empty Whowas with the given capacity. A capacity
// <= 0 falls back to the package default of 1024 entries.
func NewWhowas(capacity int, mapping CaseMapping) *Whowas {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Whowas{
		mapping: mapping,
		cap:     capacity,
		entries: make([]WhowasEntry, 0, capacity),
	}
}

// Record appends a new historical entry. If the buffer is at capacity
// the oldest entry is overwritten. Empty Nick is rejected silently
// because the entry would be unreachable.
func (w *Whowas) Record(e WhowasEntry) {
	if e.Nick == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.full {
		w.entries = append(w.entries, e)
		if len(w.entries) == w.cap {
			w.full = true
			w.head = 0
		}
		return
	}
	w.entries[w.head] = e
	w.head = (w.head + 1) % w.cap
}

// Lookup returns up to max entries for nick, most recent first. A max
// of 0 means "all matching entries". Lookup is the only RFC-relevant
// caller; tests may inspect via Snapshot.
func (w *Whowas) Lookup(nick string, max int) []WhowasEntry {
	if nick == "" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	key := w.mapping.Fold(nick)
	// Walk newest → oldest. When the buffer is full, the newest
	// entry sits at index (head-1+cap)%cap; otherwise it's the last
	// element of the slice.
	var out []WhowasEntry
	n := len(w.entries)
	for i := 0; i < n; i++ {
		var idx int
		if w.full {
			idx = (w.head - 1 - i + w.cap) % w.cap
		} else {
			idx = n - 1 - i
		}
		e := w.entries[idx]
		if w.mapping.Fold(e.Nick) != key {
			continue
		}
		out = append(out, e)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// Len reports the current number of stored entries (not the capacity).
func (w *Whowas) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.entries)
}
