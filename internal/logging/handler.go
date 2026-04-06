package logging

import (
	"context"
	"log/slog"
)

// teeHandler delivers each record to both a primary slog handler and
// the in-memory ring buffer.
//
// The primary handler is what ends up on stdout (or whatever the
// operator wired up). The ring buffer is the source for the dashboard
// log tail. Both receive a fully-resolved record; group attributes
// flow through unchanged.
type teeHandler struct {
	primary slog.Handler
	ring    *RingBuffer
	level   slog.Level
	// attrs and groups carry With/WithGroup state so it can be applied
	// to records before they reach the ring buffer. The primary handler
	// already tracks its own copy via primary.WithAttrs/WithGroup.
	attrs  []slog.Attr
	groups []string
}

func (h *teeHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.level
}

func (h *teeHandler) Handle(ctx context.Context, rec slog.Record) error {
	if err := h.primary.Handle(ctx, rec); err != nil {
		// We still want the entry in the ring buffer even if stdout
		// fails (e.g., closed pipe), but we surface the error.
		h.ring.Append(h.recordToEntry(rec))
		return err
	}
	h.ring.Append(h.recordToEntry(rec))
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := h.clone()
	clone.primary = h.primary.WithAttrs(attrs)
	clone.attrs = append(clone.attrs, attrs...)
	return clone
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := h.clone()
	clone.primary = h.primary.WithGroup(name)
	clone.groups = append(clone.groups, name)
	return clone
}

func (h *teeHandler) clone() *teeHandler {
	cp := *h
	cp.attrs = append([]slog.Attr(nil), h.attrs...)
	cp.groups = append([]string(nil), h.groups...)
	return cp
}

// recordToEntry merges any handler-level attrs/groups onto the record
// before flattening it into a ring buffer entry.
func (h *teeHandler) recordToEntry(rec slog.Record) Entry {
	if len(h.attrs) == 0 && len(h.groups) == 0 {
		return recordToEntry(rec)
	}
	// Build a synthetic record so attrs and groups are reflected in
	// the ring buffer the same way the primary handler renders them.
	merged := slog.NewRecord(rec.Time, rec.Level, rec.Message, rec.PC)
	for _, g := range h.groups {
		merged.AddAttrs(slog.Group(g, attrSliceToAny(h.attrs)...))
	}
	if len(h.groups) == 0 {
		merged.AddAttrs(h.attrs...)
	}
	rec.Attrs(func(a slog.Attr) bool {
		merged.AddAttrs(a)
		return true
	})
	return recordToEntry(merged)
}

func attrSliceToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}
