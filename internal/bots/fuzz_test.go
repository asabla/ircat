package bots

import (
	"context"
	"testing"
	"time"
)

// FuzzSandboxNeverPanics drives random Lua source through the
// sandbox's compile + dispatch path and asserts three properties
// regardless of input:
//
//  1. NewRuntime never panics — it either compiles cleanly or
//     returns an error.
//  2. DispatchMessage never panics — even on a wedged or
//     pathological script.
//  3. DispatchMessage never outlives the wallclock budget plus a
//     small fudge factor. The wallclock is the floor for every
//     guarantee the sandbox makes, so any input that lets a
//     script run longer is a regression.
//
// The fuzz seed corpus is a small set of known-tricky inputs:
// nested pcalls, runaway recursion, table blowups, the
// string.format vectors we already test deterministically, and
// some malformed source. Go's fuzzing engine mutates these to
// generate the rest.
//
// Run with:
//
//	go test -fuzz=FuzzSandboxNeverPanics -fuzztime=60s ./internal/bots/
//
// Without -fuzz the function still runs every seed as a normal
// test case, so it doubles as a regression test in CI.
func FuzzSandboxNeverPanics(f *testing.F) {
	seeds := []string{
		// Compiles and exits cleanly.
		`function on_message(ctx, ev) end`,
		// Tight infinite loop.
		`function on_message(ctx, ev) while true do end end`,
		// pcall-shielded infinite loop.
		`function on_message(ctx, ev) pcall(function() while true do end end) end`,
		// Runaway recursion.
		`function r(n) return r(n+1) end function on_message(ctx, ev) r(0) end`,
		// Doubling string concat.
		`function on_message(ctx, ev) local s = "x" while true do s = s..s end end`,
		// Table blowup.
		`function on_message(ctx, ev) local t={} for i=1,10000000 do t[i]=i end end`,
		// string.format width attack.
		`function on_message(ctx, ev) string.format("%9999999s", "x") end`,
		// Nested pcalls.
		`function on_message(ctx, ev) pcall(function() pcall(function() error("x") end) end) end`,
		// Empty body.
		``,
		// Syntax error.
		`function on_message(ctx, ev`,
		// Random punctuation soup.
		`!!!@#$%^&*()`,
		// Lua keywords without bodies.
		`if then else end while do for in repeat until function local return`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		// Cap input length. Very long fuzzer-generated sources
		// are unlikely to find bugs that shorter inputs miss,
		// and gopher-lua's table/string allocations bypass the
		// RegistrySlots cap so a single iteration with a large
		// source can allocate tens of megabytes of Go heap.
		// Over millions of iterations the GC cannot keep up and
		// the worker OOMs.
		if len(src) > 4096 {
			return
		}

		// Catch any panic the runtime might raise so the fuzz
		// run reports it as a failed input rather than a
		// process-wide crash.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v\nsource: %q", r, src)
			}
		}()

		fa := newFakeActions("bot")
		const wallclock = 100 * time.Millisecond
		rt, err := NewRuntime(src, fa, Budget{
			Instructions:  100_000,
			Wallclock:     wallclock,
			RegistrySlots: 4096,
		})
		if err != nil {
			// Compile errors are fine — the sandbox refused
			// the input cleanly, which is the correct
			// outcome for malformed source.
			return
		}
		defer rt.Close()

		start := time.Now()
		_ = rt.DispatchMessage(context.Background(), Event{
			Channel:  "#x",
			Sender:   "alice",
			Hostmask: "alice!a@h",
			Text:     "hi",
		})
		elapsed := time.Since(start)
		// The wallclock guarantees the call returns within the
		// budget. The fudge factor has to be generous: Go's
		// fuzzer runs N workers in parallel (defaults to GOMAXPROCS)
		// and a tight-loop seed in one worker can starve scheduling
		// on the others for seconds at a time on a shared CI host.
		// We have observed ~1s elapsed for trivial scripts on
		// contended runners. The point of this assertion is to
		// catch a runtime that runs *forever* (e.g. a missing
		// context cancellation), not one that misses a 100ms
		// budget by a small multiple, so a 5s ceiling is the
		// right shape.
		if elapsed > wallclock+5*time.Second {
			t.Fatalf("DispatchMessage ran %s with %s budget; source: %q", elapsed, wallclock, src)
		}
	})
}
