package events

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONLSink_WritesOneLinePerEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	sink, err := NewJSONLSink(JSONLSinkOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	for i, typ := range []string{"oper_up", "kick", "mode"} {
		ev := Event{ID: string(rune('a' + i)), Type: typ, Actor: "alice"}
		if err := sink.Handle(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	var first Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Type != "oper_up" || first.Actor != "alice" {
		t.Errorf("first = %+v", first)
	}
}

func TestJSONLSink_RotatesOnSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	sink, err := NewJSONLSink(JSONLSinkOptions{
		Path:        path,
		RotateBytes: 100, // tiny for the test
		Keep:        3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	// Each event encodes to ~80 bytes. Writing 5 of them should
	// cross the threshold several times.
	for i := 0; i < 5; i++ {
		ev := Event{
			ID:   strings.Repeat("x", 30),
			Type: "test",
		}
		if err := sink.Handle(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}

	// Expect at least one rotated file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sawRotated := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "events.jsonl.") {
			sawRotated = true
			break
		}
	}
	if !sawRotated {
		t.Errorf("no rotated file: %v", entries)
	}
}

func TestJSONLSink_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "events.jsonl")
	sink, err := NewJSONLSink(JSONLSinkOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestJSONLSink_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	sink, err := NewJSONLSink(JSONLSinkOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}
