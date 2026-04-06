// Package events implements ircat's outbound event bus.
//
// The bus is the fan-out point for audit events, channel activity,
// and anything else operators may want to export to an external
// system. Producers call [Bus.Publish] with an [Event]; each
// subscribed [Sink] receives the event on its own goroutine via a
// bounded inbox. Slow sinks do not block the producer — the bus
// drops the event and increments a drop counter on the sink
// instead.
//
// The package is deliberately small. JSONL and webhook sinks live
// in their own files and implement the [Sink] interface. Other
// transports (redis, kafka, nats, ...) can land later by dropping a
// new sink file in this package; the bus needs no changes.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Event is the canonical envelope for everything flowing through
// the bus. The shape mirrors docs/EVENTS.md: an ID, a timestamp,
// the server that produced it, a type tag, optional actor/target
// identifiers, and a JSON blob carrying the event-specific payload.
//
// DataJSON is stored as a string rather than an interface{} so the
// bus does not re-marshal on every sink hand-off.
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"ts"`
	Server    string    `json:"server"`
	Type      string    `json:"type"`
	Actor     string    `json:"actor,omitempty"`
	Target    string    `json:"target,omitempty"`
	DataJSON  string    `json:"data,omitempty"`
}

// NewID returns a sortable string ID suitable for an Event. The
// format is "{16-hex-time}-{16-hex-random}" — the same shape the
// server package uses for audit rows, so event IDs and audit IDs
// sort compatibly across the two layers.
func NewID(now time.Time) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", fmt.Errorf("event id rand: %w", err)
	}
	return fmt.Sprintf("%016x-%s", uint64(now.UnixNano()), hex.EncodeToString(rnd[:])), nil
}

// Sink consumes events. Implementations receive each event on the
// bus's subscribe goroutine, NOT on the producer goroutine, so a
// blocking Handle will only stall its own sink.
type Sink interface {
	// Name returns a short identifier for logs and metrics
	// ("jsonl", "webhook", ...).
	Name() string
	// Handle processes one event. A non-nil error is logged but
	// does not stop the subscriber loop; sinks that want to stop
	// permanently should set a flag and return nil on subsequent
	// calls.
	Handle(ctx context.Context, ev Event) error
	// Close releases any resources the sink holds (open files,
	// HTTP idle connections, DLQ writers). Called by the bus at
	// shutdown.
	Close() error
}

// Bus is the fan-out point. Construct with [NewBus], subscribe
// sinks via [Bus.Subscribe], then call [Bus.Publish] from producer
// goroutines. [Bus.Close] drains every sink queue and waits for the
// subscriber goroutines to exit.
type Bus struct {
	logger *slog.Logger

	mu          sync.Mutex
	subscribers []*subscriber
	closed      bool
}

// subscriber is one sink + its inbox + bookkeeping counters.
type subscriber struct {
	sink    Sink
	inbox   chan Event
	dropped atomic.Uint64
	done    chan struct{}
}

// NewBus constructs an empty Bus. No sinks are subscribed yet.
func NewBus(logger *slog.Logger) *Bus {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bus{logger: logger}
}

// SubscribeOptions configures a new subscription.
type SubscribeOptions struct {
	// QueueSize is the inbox depth for this sink. Smaller = faster
	// failure under load; larger = more memory per sink. Default
	// is 256 when zero.
	QueueSize int
}

// Subscribe attaches sink to the bus and spawns its worker
// goroutine. The subscription lives until [Bus.Close].
func (b *Bus) Subscribe(sink Sink, opts SubscribeOptions) {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 256
	}
	sub := &subscriber{
		sink:  sink,
		inbox: make(chan Event, opts.QueueSize),
		done:  make(chan struct{}),
	}
	b.mu.Lock()
	b.subscribers = append(b.subscribers, sub)
	b.mu.Unlock()
	go b.run(sub)
}

// Publish fans the event out to every subscriber. Non-blocking per
// sink: if a sink's inbox is full the event is dropped and the
// drop counter incremented. Returns immediately after handing the
// event to every inbox (or skipping them).
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	subs := make([]*subscriber, len(b.subscribers))
	copy(subs, b.subscribers)
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return
	}
	for _, sub := range subs {
		select {
		case sub.inbox <- ev:
		default:
			if sub.dropped.Add(1)%1000 == 1 {
				b.logger.Warn("event sink inbox full, dropping",
					"sink", sub.sink.Name(),
					"dropped_total", sub.dropped.Load())
			}
		}
	}
}

// Close drains every subscriber queue and waits for their worker
// goroutines to exit. Subsequent Publish calls are no-ops.
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := make([]*subscriber, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.Unlock()

	for _, sub := range subs {
		close(sub.inbox)
	}
	for _, sub := range subs {
		<-sub.done
		if err := sub.sink.Close(); err != nil {
			b.logger.Warn("sink close failed", "sink", sub.sink.Name(), "error", err)
		}
	}
	return nil
}

// run is the per-subscriber worker loop.
func (b *Bus) run(sub *subscriber) {
	defer close(sub.done)
	ctx := context.Background()
	for ev := range sub.inbox {
		if err := sub.sink.Handle(ctx, ev); err != nil {
			b.logger.Warn("sink handle failed",
				"sink", sub.sink.Name(), "event_id", ev.ID, "error", err)
		}
	}
}

// ErrInvalidEvent is returned by sinks when the supplied Event
// cannot be serialized. Producers should treat it as a programming
// bug, not an operational failure.
var ErrInvalidEvent = errors.New("events: invalid event")
