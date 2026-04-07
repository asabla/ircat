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
//
// New is a thin wrapper over NewWithLevel that discards the level
// controller. Most callers do not need to change levels at runtime;
// cmd/ircat uses NewWithLevel so it can flip the level on SIGHUP.
func New(opts Options) (*slog.Logger, *RingBuffer, error) {
	logger, ring, _, err := NewWithLevel(opts)
	return logger, ring, err
}

// NewWithLevel is the same as New but also returns a *slog.LevelVar
// the caller can mutate at runtime to change the effective log
// level without rebuilding the handler chain. Used by cmd/ircat for
// SIGHUP / config reload.
func NewWithLevel(opts Options) (*slog.Logger, *RingBuffer, *slog.LevelVar, error) {
	level, err := parseLevel(opts.Level)
	if err != nil {
		return nil, nil, nil, err
	}
	levelVar := new(slog.LevelVar)
	levelVar.Set(level)

	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	ring := NewRingBuffer(opts.RingBufferEntries)

	handlerOpts := &slog.HandlerOptions{Level: levelVar}

	var primary slog.Handler
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", "json":
		primary = slog.NewJSONHandler(out, handlerOpts)
	case "text":
		primary = slog.NewTextHandler(out, handlerOpts)
	default:
		return nil, nil, nil, fmt.Errorf("logging: unknown format %q (want json or text)", opts.Format)
	}

	handler := &teeHandler{primary: primary, ring: ring, level: levelVar}
	return slog.New(handler), ring, levelVar, nil
}

// ParseLevel exposes the package-private parseLevel for callers
// that want to validate a config-supplied level string before
// applying it via *slog.LevelVar.Set.
func ParseLevel(s string) (slog.Level, error) { return parseLevel(s) }

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
