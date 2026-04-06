package events

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingSink captures every Handle call so tests can assert on
// the events that actually reached it.
type recordingSink struct {
	mu      sync.Mutex
	name    string
	handled []Event
	closed  bool
	// blockUntil blocks Handle until the channel is closed; tests
	// use it to force the inbox to fill up for drop-coverage.
	blockUntil chan struct{}
}

func (s *recordingSink) Name() string { return s.name }
func (s *recordingSink) Handle(ctx context.Context, ev Event) error {
	if s.blockUntil != nil {
		<-s.blockUntil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handled = append(s.handled, ev)
	return nil
}
func (s *recordingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
func (s *recordingSink) snap() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.handled))
	copy(out, s.handled)
	return out
}

func TestBus_PublishFansOutToSubscribers(t *testing.T) {
	bus := NewBus(nil)
	a := &recordingSink{name: "a"}
	b := &recordingSink{name: "b"}
	bus.Subscribe(a, SubscribeOptions{})
	bus.Subscribe(b, SubscribeOptions{})

	bus.Publish(Event{ID: "1", Type: "oper_up"})
	bus.Publish(Event{ID: "2", Type: "kick"})

	_ = bus.Close()

	if got := a.snap(); len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Errorf("a = %+v", got)
	}
	if got := b.snap(); len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Errorf("b = %+v", got)
	}
	if !a.closed || !b.closed {
		t.Errorf("Close not called on sinks")
	}
}

func TestBus_SlowSinkDoesNotBlockFastSink(t *testing.T) {
	bus := NewBus(nil)
	slow := &recordingSink{
		name:       "slow",
		blockUntil: make(chan struct{}),
	}
	fast := &recordingSink{name: "fast"}
	bus.Subscribe(slow, SubscribeOptions{QueueSize: 1})
	bus.Subscribe(fast, SubscribeOptions{QueueSize: 1024})

	// Publish more events than the slow sink's inbox can hold.
	// The slow sink is stuck in Handle until we release it, so
	// after the first event is picked up the inbox fills on the
	// second, and subsequent events are dropped. The fast sink
	// must still receive all of them.
	var drops atomic.Int32
	for i := 0; i < 10; i++ {
		bus.Publish(Event{ID: string(rune('a' + i))})
	}
	// Give the fast sink a moment to drain.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(fast.snap()) == 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(fast.snap()); got != 10 {
		t.Errorf("fast sink handled %d, want 10", got)
	}
	// Release the slow sink so Close can drain it.
	close(slow.blockUntil)
	_ = bus.Close()
	_ = drops
}

func TestBus_ClosedBusSwallowsPublish(t *testing.T) {
	bus := NewBus(nil)
	s := &recordingSink{name: "s"}
	bus.Subscribe(s, SubscribeOptions{})
	_ = bus.Close()
	// Publish after Close must not panic.
	bus.Publish(Event{ID: "post-close"})
}

func TestNewID_MonotonicSortable(t *testing.T) {
	ids := make([]string, 10)
	base := time.Now()
	for i := range ids {
		id, err := NewID(base.Add(time.Duration(i) * time.Nanosecond))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("ids not sorted: %v vs %v", ids[i-1], ids[i])
		}
	}
}
