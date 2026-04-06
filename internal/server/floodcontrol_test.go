package server

import (
	"testing"
	"time"
)

func TestTokenBucket_BurstThenEmpty(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	b := newTokenBucket(3, 1, clock)

	for i := 0; i < 3; i++ {
		if !b.Take() {
			t.Fatalf("call %d should succeed", i)
		}
	}
	if b.Take() {
		t.Fatal("4th call should fail (bucket empty, no time elapsed)")
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	b := newTokenBucket(2, 2, clock) // 2 tokens/sec

	b.Take()
	b.Take()
	if b.Take() {
		t.Fatal("third Take should fail")
	}

	// Advance the clock by 600 ms — should yield ~1.2 new tokens.
	now = now.Add(600 * time.Millisecond)
	if !b.Take() {
		t.Fatal("Take after refill should succeed")
	}
	// Only ~0.2 tokens left, not enough for another.
	if b.Take() {
		t.Fatal("Take immediately after should fail")
	}
}

func TestTokenBucket_CapsAtBurst(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	b := newTokenBucket(2, 10, clock)

	// Advance clock by 60 seconds — refill of 600 tokens, but
	// the bucket caps at 2.
	now = now.Add(60 * time.Second)
	b.Take()
	b.Take()
	if b.Take() {
		t.Fatal("bucket should cap at burst (2)")
	}
}
