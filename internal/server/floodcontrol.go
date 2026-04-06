package server

import (
	"sync"
	"time"
)

// tokenBucket is a small per-connection rate limiter.
//
// Each successful [tokenBucket.Take] consumes one token. The bucket
// refills at a steady rate up to its burst capacity. Time is read
// from a caller-supplied clock so tests can be deterministic.
//
// The bucket holds tokens as a float64 so partial refills accumulate
// across calls — a refill rate of 2/s with a 100 ms gap between
// calls credits 0.2 of a token.
type tokenBucket struct {
	mu sync.Mutex

	burst      float64
	refillRate float64 // tokens per second
	available  float64
	lastRefill time.Time
	clock      func() time.Time
}

func newTokenBucket(burst, refillPerSecond int, clock func() time.Time) *tokenBucket {
	if burst <= 0 {
		burst = 1
	}
	if refillPerSecond <= 0 {
		refillPerSecond = 1
	}
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	return &tokenBucket{
		burst:      float64(burst),
		refillRate: float64(refillPerSecond),
		available:  float64(burst),
		lastRefill: now,
		clock:      clock,
	}
}

// Take attempts to consume one token. Returns true on success, false
// if the bucket is empty.
func (b *tokenBucket) Take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.available += elapsed * b.refillRate
		if b.available > b.burst {
			b.available = b.burst
		}
		b.lastRefill = now
	}
	if b.available < 1 {
		return false
	}
	b.available--
	return true
}

// Available returns the current token count rounded down. Used by
// tests; no production callers.
func (b *tokenBucket) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.available)
}
