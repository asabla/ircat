package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNew_DefaultsToJSONInfo(t *testing.T) {
	var buf bytes.Buffer
	logger, ring, err := New(Options{Output: &buf})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ring.Capacity() != DefaultRingEntries {
		t.Errorf("Capacity = %d, want %d", ring.Capacity(), DefaultRingEntries)
	}
	logger.Debug("hidden")
	logger.Info("visible", "k", "v")

	if strings.Contains(buf.String(), "hidden") {
		t.Errorf("debug record leaked at default level: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "visible") {
		t.Errorf("info record missing: %s", buf.String())
	}
	// JSON output should parse.
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "visible" || rec["k"] != "v" {
		t.Errorf("unexpected record: %v", rec)
	}
}

func TestNew_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger, _, err := New(Options{Format: "TEXT", Level: "debug", Output: &buf})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.Debug("a thing happened", "id", 7)
	out := buf.String()
	if !strings.Contains(out, "a thing happened") || !strings.Contains(out, "id=7") {
		t.Errorf("text output missing fields: %q", out)
	}
}

func TestNew_BadLevel(t *testing.T) {
	if _, _, err := New(Options{Level: "loud"}); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

func TestNew_BadFormat(t *testing.T) {
	if _, _, err := New(Options{Format: "xml"}); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestRingBuffer_CapturesAndOrders(t *testing.T) {
	var buf bytes.Buffer
	logger, ring, err := New(Options{Output: &buf, RingBufferEntries: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 3; i++ {
		logger.Info("entry", "i", i)
	}

	snap := ring.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(snap))
	}
	for i, e := range snap {
		if e.Message != "entry" {
			t.Errorf("entry %d msg = %q", i, e.Message)
		}
		if got := e.Attrs["i"]; got != int64(i) && got != i {
			t.Errorf("entry %d i = %v (%T), want %d", i, got, got, i)
		}
		if e.Seq != uint64(i+1) {
			t.Errorf("entry %d seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}

func TestRingBuffer_OverwritesOldest(t *testing.T) {
	r := NewRingBuffer(3)
	for i := 1; i <= 5; i++ {
		r.Append(Entry{Message: "m", Attrs: map[string]any{"i": i}})
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(snap))
	}
	wantSeqs := []uint64{3, 4, 5}
	for i, e := range snap {
		if e.Seq != wantSeqs[i] {
			t.Errorf("snap[%d].Seq = %d, want %d", i, e.Seq, wantSeqs[i])
		}
	}
}

func TestRingBuffer_Since(t *testing.T) {
	r := NewRingBuffer(8)
	for i := 0; i < 5; i++ {
		r.Append(Entry{Message: "m"})
	}
	got := r.Since(2)
	if len(got) != 3 {
		t.Fatalf("Since(2) len = %d, want 3", len(got))
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Errorf("Since(2) seqs = [%d..%d], want [3..5]", got[0].Seq, got[2].Seq)
	}
	if got := r.Since(5); got != nil {
		t.Errorf("Since(5) = %v, want nil", got)
	}
}

func TestRingBuffer_SinceAfterWrap(t *testing.T) {
	r := NewRingBuffer(3)
	for i := 1; i <= 6; i++ {
		r.Append(Entry{Message: "m"})
	}
	// Buffer holds seqs 4,5,6 now. Asking since=2 should return all
	// three (the gap is the caller's signal of overflow).
	got := r.Since(2)
	if len(got) != 3 {
		t.Fatalf("Since(2) after wrap len = %d, want 3", len(got))
	}
	if got[0].Seq != 4 {
		t.Errorf("first seq = %d, want 4", got[0].Seq)
	}
}

func TestLogger_WithAttrsFlowsToRing(t *testing.T) {
	var buf bytes.Buffer
	logger, ring, err := New(Options{Output: &buf, Level: "debug"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	logger.With("component", "boot").Info("ready", "port", 6667)

	snap := ring.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d", len(snap))
	}
	e := snap[0]
	if e.Attrs["component"] != "boot" {
		t.Errorf("component attr = %v", e.Attrs["component"])
	}
	if e.Attrs["port"] != int64(6667) && e.Attrs["port"] != 6667 {
		t.Errorf("port attr = %v (%T)", e.Attrs["port"], e.Attrs["port"])
	}
}
