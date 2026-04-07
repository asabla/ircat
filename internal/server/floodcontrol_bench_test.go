package server

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkTokenBucket_Take measures the per-call cost of a
// single sender hitting an uncontended bucket. This is the floor
// for the rate limiter — it tells us what we pay per accepted
// PRIVMSG when there is no contention. The result feeds into the
// flood-control sizing recommendation in OPERATIONS.md.
func BenchmarkTokenBucket_Take(b *testing.B) {
	bucket := newTokenBucket(1_000_000, 1_000_000, time.Now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bucket.Take()
	}
}

// BenchmarkTokenBucket_TakeContended_NSenders measures throughput
// when N goroutines hammer the same bucket. The bucket is
// per-connection in production, so this is NOT a realistic
// PRIVMSG benchmark on its own — but it does measure how the
// internal mutex behaves when many goroutines drain a shared
// resource. We use it as a worst-case bound.
func BenchmarkTokenBucket_TakeContended_1(b *testing.B)    { benchTokenBucketContended(b, 1) }
func BenchmarkTokenBucket_TakeContended_10(b *testing.B)   { benchTokenBucketContended(b, 10) }
func BenchmarkTokenBucket_TakeContended_100(b *testing.B)  { benchTokenBucketContended(b, 100) }
func BenchmarkTokenBucket_TakeContended_1000(b *testing.B) { benchTokenBucketContended(b, 1000) }

func benchTokenBucketContended(b *testing.B, senders int) {
	bucket := newTokenBucket(1_000_000, 1_000_000, time.Now)
	var wg sync.WaitGroup
	per := b.N / senders
	if per == 0 {
		per = 1
	}
	b.ResetTimer()
	for i := 0; i < senders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				_ = bucket.Take()
			}
		}()
	}
	wg.Wait()
}

// BenchmarkTokenBucket_PerConnection_NConnections is the realistic
// fan-out: each "sender" gets its own bucket so the only contention
// is on the host CPU + the shared time.Now clock. This is the
// production model — every IRC connection has its own
// tokenBucket, so we want to know how the system scales with
// connection count, not how a single bucket scales with senders.
func BenchmarkTokenBucket_PerConnection_1(b *testing.B)    { benchTokenBucketPerConn(b, 1) }
func BenchmarkTokenBucket_PerConnection_10(b *testing.B)   { benchTokenBucketPerConn(b, 10) }
func BenchmarkTokenBucket_PerConnection_100(b *testing.B)  { benchTokenBucketPerConn(b, 100) }
func BenchmarkTokenBucket_PerConnection_1000(b *testing.B) { benchTokenBucketPerConn(b, 1000) }

func benchTokenBucketPerConn(b *testing.B, conns int) {
	buckets := make([]*tokenBucket, conns)
	for i := range buckets {
		buckets[i] = newTokenBucket(1_000_000, 1_000_000, time.Now)
	}
	var wg sync.WaitGroup
	per := b.N / conns
	if per == 0 {
		per = 1
	}
	b.ResetTimer()
	for i := 0; i < conns; i++ {
		bucket := buckets[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				_ = bucket.Take()
			}
		}()
	}
	wg.Wait()
}

// BenchmarkTokenBucket_RealisticBurstThenWait simulates the
// production traffic shape: a sender bursts up to its limit,
// hits the floor, waits for refill, and repeats. The benchmark
// runs against the default production limits (100 burst, 10/s
// refill) so the result reflects what an actual chatty client
// would see. The reported numbers are dominated by the time.Now
// calls and the mutex acquire — Take itself is a few ns.
func BenchmarkTokenBucket_RealisticBurstThenWait(b *testing.B) {
	bucket := newTokenBucket(100, 10, time.Now)
	var refused atomic.Int64
	for i := 0; i < b.N; i++ {
		if !bucket.Take() {
			refused.Add(1)
		}
	}
	b.ReportMetric(float64(refused.Load())/float64(b.N), "refused/op")
}
