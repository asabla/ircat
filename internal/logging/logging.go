// Package logging wires up the slog logger used across ircat.
//
// It produces a [*slog.Logger] backed by a [*slog.Handler] that writes
// JSON or text records to an [io.Writer] (stdout by default), and it
// also fans every record into a fixed-size in-memory ring buffer that
// the dashboard tails over Server-Sent Events.
//
// The ring buffer is the *only* reason this package exists at all —
// otherwise the standard library would be enough. Keeping the buffer
// behind a small package boundary lets the dashboard depend on a typed
// snapshot/subscribe API instead of reaching into log/slog internals.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options configures [New].
//
// Zero values are valid: an empty Level is treated as "info", an empty
// Format as "json", and a zero RingBufferEntries as DefaultRingEntries.
type Options struct {
	// Level is one of "debug", "info", "warn", "error" (case-insensitive).
	Level string
	// Format is "json" or "text" (case-insensitive).
	Format string
	// RingBufferEntries is the in-memory tail size. <=0 means default.
	RingBufferEntries int
	// Output is the underlying sink. Defaults to os.Stdout.
	Output io.Writer
}

// DefaultRingEntries is the fallback ring buffer size when Options
// does not specify one.
const DefaultRingEntries = 10_000

// New constructs a logger plus the ring buffer it is wired into.
//
// The returned logger writes to Options.Output (stdout by default) and
// simultaneously appends every record to the returned ring buffer.
func New(opts Options) (*slog.Logger, *RingBuffer, error) {
	level, err := parseLevel(opts.Level)
	if err != nil {
		return nil, nil, err
	}

	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	ring := NewRingBuffer(opts.RingBufferEntries)

	handlerOpts := &slog.HandlerOptions{Level: level}

	var primary slog.Handler
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", "json":
		primary = slog.NewJSONHandler(out, handlerOpts)
	case "text":
		primary = slog.NewTextHandler(out, handlerOpts)
	default:
		return nil, nil, fmt.Errorf("logging: unknown format %q (want json or text)", opts.Format)
	}

	handler := &teeHandler{primary: primary, ring: ring, level: level}
	return slog.New(handler), ring, nil
}

// parseLevel maps a string to an [slog.Level]. Empty defaults to info.
func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logging: unknown level %q (want debug, info, warn, error)", s)
}
