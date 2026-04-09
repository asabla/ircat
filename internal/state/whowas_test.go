package state

import (
	"testing"
	"time"
)

func TestWhowas_RecordAndLookup(t *testing.T) {
	w := NewWhowas(8, CaseMappingRFC1459)
	w.Record(WhowasEntry{Nick: "alice", User: "alice", Host: "h1", Realname: "Alice", Server: "irc.test", When: time.Unix(1, 0)})
	w.Record(WhowasEntry{Nick: "alice", User: "alice", Host: "h2", Realname: "Alice", Server: "irc.test", When: time.Unix(2, 0)})

	got := w.Lookup("alice", 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Host != "h2" {
		t.Errorf("expected newest first, got Host=%q", got[0].Host)
	}
}

func TestWhowas_LookupCaseFolded(t *testing.T) {
	w := NewWhowas(4, CaseMappingRFC1459)
	w.Record(WhowasEntry{Nick: "Alice", User: "u", Host: "h", When: time.Unix(1, 0)})
	if got := w.Lookup("ALICE", 0); len(got) != 1 {
		t.Errorf("case-fold lookup failed: %d entries", len(got))
	}
}

func TestWhowas_RingOverflow(t *testing.T) {
	w := NewWhowas(3, CaseMappingRFC1459)
	for i := 1; i <= 5; i++ {
		w.Record(WhowasEntry{Nick: "alice", Host: "h", When: time.Unix(int64(i), 0)})
	}
	got := w.Lookup("alice", 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 (cap), got %d", len(got))
	}
	// Newest first → When=5,4,3
	if got[0].When.Unix() != 5 || got[2].When.Unix() != 3 {
		t.Errorf("ring order wrong: %+v", got)
	}
}

func TestWhowas_LookupMissing(t *testing.T) {
	w := NewWhowas(4, CaseMappingRFC1459)
	w.Record(WhowasEntry{Nick: "alice", When: time.Unix(1, 0)})
	if got := w.Lookup("bob", 0); len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestWhowas_LookupMaxCap(t *testing.T) {
	w := NewWhowas(8, CaseMappingRFC1459)
	for i := 1; i <= 4; i++ {
		w.Record(WhowasEntry{Nick: "alice", When: time.Unix(int64(i), 0)})
	}
	if got := w.Lookup("alice", 2); len(got) != 2 {
		t.Errorf("max=2 ignored, got %d", len(got))
	}
}
