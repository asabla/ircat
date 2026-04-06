package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// JSONLSinkOptions configures [NewJSONLSink].
type JSONLSinkOptions struct {
	// Path is the target file. Missing parent directories are
	// created on Open.
	Path string
	// RotateBytes is the max size (in bytes) before the sink
	// rotates. Zero disables rotation.
	RotateBytes int64
	// Keep is the number of rotated files to retain (oldest
	// deleted on rotation). Ignored when RotateBytes is zero.
	Keep int
}

// JSONLSink writes each event as one JSON object per line to a
// file. On rotation the current file is renamed with a ".N" suffix
// (newest = .1, oldest = .Keep) and a fresh file is opened.
type JSONLSink struct {
	opts JSONLSinkOptions

	mu      sync.Mutex
	file    *os.File
	written int64
}

// NewJSONLSink opens the sink file (creating parent dirs) and
// returns a ready-to-use JSONLSink.
func NewJSONLSink(opts JSONLSinkOptions) (*JSONLSink, error) {
	if opts.Path == "" {
		return nil, fmt.Errorf("events: jsonl sink: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return nil, fmt.Errorf("events: jsonl mkdir: %w", err)
	}
	f, err := os.OpenFile(opts.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("events: jsonl open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("events: jsonl stat: %w", err)
	}
	return &JSONLSink{
		opts:    opts,
		file:    f,
		written: info.Size(),
	}, nil
}

// Name implements [Sink].
func (s *JSONLSink) Name() string { return "jsonl" }

// Handle implements [Sink]. Serializes ev to JSON + newline and
// appends to the open file. Rotates when the file exceeds
// RotateBytes.
func (s *JSONLSink) Handle(ctx context.Context, ev Event) error {
	buf, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("%w: marshal: %v", ErrInvalidEvent, err)
	}
	buf = append(buf, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.file.Write(buf)
	if err != nil {
		return fmt.Errorf("events: jsonl write: %w", err)
	}
	s.written += int64(n)
	if s.opts.RotateBytes > 0 && s.written >= s.opts.RotateBytes {
		if err := s.rotateLocked(); err != nil {
			return fmt.Errorf("events: jsonl rotate: %w", err)
		}
	}
	return nil
}

// Close implements [Sink].
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// rotateLocked renames the current file to Path.1 (shifting any
// existing .N files up by one and dropping the oldest) and opens a
// fresh Path. Caller must hold s.mu.
func (s *JSONLSink) rotateLocked() error {
	if err := s.file.Close(); err != nil {
		return err
	}
	s.file = nil

	// Shift Path.N -> Path.(N+1), oldest first so we don't clobber.
	// Keep=0 means unlimited; clamp to a sane cap.
	keep := s.opts.Keep
	if keep <= 0 {
		keep = 7
	}
	existing, err := listRotated(s.opts.Path)
	if err != nil {
		return err
	}
	// existing is sorted newest first (Path.1, Path.2, ...).
	// Delete any beyond keep-1, then shift the rest up.
	for i, p := range existing {
		if i >= keep-1 {
			_ = os.Remove(p)
		}
	}
	// Shift surviving files up by one index. Iterate backwards so
	// we never collide.
	for i := len(existing) - 1; i >= 0; i-- {
		if i >= keep-1 {
			continue
		}
		src := existing[i]
		dst := fmt.Sprintf("%s.%d", s.opts.Path, i+2)
		_ = os.Rename(src, dst)
	}
	// Move the current file to Path.1.
	if err := os.Rename(s.opts.Path, s.opts.Path+".1"); err != nil {
		return err
	}
	// Open a fresh file.
	f, err := os.OpenFile(s.opts.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	s.file = f
	s.written = 0
	return nil
}

// listRotated returns existing Path.N files in newest-first order
// (Path.1 before Path.2 before Path.3).
func listRotated(path string) ([]string, error) {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(path) + "."
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		if _, err := fmt.Sscanf(suffix, "%d", new(int)); err != nil {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Slice(out, func(i, j int) bool {
		// Path.1 < Path.2 < Path.10 (numeric, not lexical).
		var ai, aj int
		fmt.Sscanf(strings.TrimPrefix(filepath.Base(out[i]), prefix), "%d", &ai)
		fmt.Sscanf(strings.TrimPrefix(filepath.Base(out[j]), prefix), "%d", &aj)
		return ai < aj
	})
	return out, nil
}
