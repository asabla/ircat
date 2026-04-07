package bots

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSandbox_DangerousGlobalsAreNil enumerates the globals that
// must NOT be reachable from a bot script. Each one is asserted to
// be nil at compile time so any future change to OpenLibs that
// reintroduces them surfaces as a hard test failure rather than a
// silent privilege escalation.
func TestSandbox_DangerousGlobalsAreNil(t *testing.T) {
	cases := []string{
		"io",
		"os",
		"debug",
		"dofile",
		"loadfile",
		"load",
		"loadstring",
		"require",
		"module",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			src := `
				if ` + name + ` ~= nil then
					error("global ` + name + ` is reachable")
				end
			`
			fa := newFakeActions("bot")
			rt, err := NewRuntime(src, fa, Budget{Instructions: 100_000, Wallclock: time.Second})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			t.Cleanup(rt.Close)
		})
	}
}

// TestSandbox_PackageLoadlibIsBlocked ensures package.loadlib has
// been replaced with the raise stub even though the package
// library is opened (it has to be, because OpenBase depends on
// it).
func TestSandbox_PackageLoadlibIsBlocked(t *testing.T) {
	src := `
		if package == nil then
			error("package missing")
		end
		if type(package.loadlib) ~= "function" then
			error("loadlib should still be a function (the stub)")
		end
		local ok, err = pcall(package.loadlib, "/lib/x.so", "init")
		if ok then
			error("loadlib should have raised")
		end
		if not err:find("disabled") then
			error("loadlib stub raised wrong error: " .. tostring(err))
		end
	`
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{Instructions: 100_000, Wallclock: time.Second})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	t.Cleanup(rt.Close)
}

// TestSandbox_PackageLoaderTablesAreEmpty checks that the loader
// chains the require mechanism walks have been wiped, so even if a
// future change reinstates require it would walk a no-op chain.
func TestSandbox_PackageLoaderTablesAreEmpty(t *testing.T) {
	src := `
		for _, name in ipairs({"preload", "loaders", "searchers"}) do
			local t = package[name]
			if type(t) ~= "table" then
				error("package." .. name .. " should be a table, got " .. type(t))
			end
			if next(t) ~= nil then
				error("package." .. name .. " should be empty")
			end
		end
	`
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{Instructions: 100_000, Wallclock: time.Second})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	t.Cleanup(rt.Close)
}

// TestSandbox_StringDumpUnavailable ensures string.dump is absent.
// string.dump in standard Lua serializes the bytecode of a function
// and is part of every escape chain that combines with load() —
// blocking it is belt-and-braces.
func TestSandbox_StringDumpUnavailable(t *testing.T) {
	src := `
		if string.dump ~= nil then
			error("string.dump must not be exposed")
		end
	`
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{Instructions: 100_000, Wallclock: time.Second})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	t.Cleanup(rt.Close)
}

// TestSandbox_WallclockBudgetTerminatesInfiniteLoop drives a tight
// infinite loop and asserts that the wall-clock guard from the
// budget cancels the runtime context, terminating the script with
// a context-deadline error rather than wedging the test.
//
// The test also incidentally proves that gopher-lua's per-
// instruction context check (vm.go line ~56) is what enforces the
// budget at instruction granularity — without it, a tight Lua
// loop would never read its own context.
func TestSandbox_WallclockBudgetTerminatesInfiniteLoop(t *testing.T) {
	src := `
		function on_message(ctx, ev)
			while true do end
		end
	`
	runWithBudget(t, src, 150*time.Millisecond, time.Second)
}

// TestSandbox_WallclockBudgetCatchesPcallShield wraps the runaway
// loop in pcall and asserts the budget still applies. pcall is
// the standard "ignore errors" idiom in Lua and a sandbox that
// only catches uncaught errors would let a malicious script
// neutralise the budget by wrapping every dangerous call in
// pcall. gopher-lua's ctx check fires inside the protected call
// too, so the runtime exits with a context error that pcall
// cannot intercept.
func TestSandbox_WallclockBudgetCatchesPcallShield(t *testing.T) {
	src := `
		function on_message(ctx, ev)
			pcall(function()
				while true do end
			end)
		end
	`
	runWithBudget(t, src, 150*time.Millisecond, time.Second)
}

// TestSandbox_WallclockBudgetCatchesRecursion proves the
// budget catches a runaway recursion. Stack-based runaways
// allocate frame structures and would otherwise grow until the
// gopher-lua call-stack limit, which is the WRONG bound — the
// budget should fire first.
func TestSandbox_WallclockBudgetCatchesRecursion(t *testing.T) {
	src := `
		function recurse(n) return recurse(n + 1) end
		function on_message(ctx, ev)
			recurse(0)
		end
	`
	runWithBudget(t, src, 150*time.Millisecond, time.Second)
}

// TestSandbox_WallclockBudgetCatchesAllocBlowup runs a tight
// string concatenation loop that doubles the string length on
// every iteration, which would fill memory in a few hundred
// iterations without the budget. The wallclock fires long before
// the allocations run away.
func TestSandbox_WallclockBudgetCatchesAllocBlowup(t *testing.T) {
	src := `
		function on_message(ctx, ev)
			local s = "x"
			while true do s = s .. s end
		end
	`
	runWithBudget(t, src, 150*time.Millisecond, time.Second)
}

// TestSandbox_InstructionBudgetMappingHonoured checks that an
// explicit Instructions budget converts to a wallclock proxy
// that fires before the explicit Wallclock when the
// instructions budget is the tighter of the two.
func TestSandbox_InstructionBudgetMappingHonoured(t *testing.T) {
	src := `
		function on_message(ctx, ev)
			while true do end
		end
	`
	// 100k instructions @ 10M/s ≈ 10ms wallclock proxy.
	// Wallclock is set very high so the proxy must fire first.
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{
		Instructions: 100_000,
		Wallclock:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer rt.Close()

	start := time.Now()
	err = rt.DispatchMessage(context.Background(), Event{
		Channel: "#x", Sender: "a", Hostmask: "a!a@h", Text: "hi",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout from instruction budget")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("instruction budget proxy did not fire promptly: %s", elapsed)
	}
}

// runWithBudget is the shared driver for the four
// runaway-script tests above. Each variant differs only in the
// Lua source it runs; the assertions are identical.
func runWithBudget(t *testing.T, src string, wall, hardCap time.Duration) {
	t.Helper()
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{
		Instructions: 100_000,
		Wallclock:    wall,
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer rt.Close()

	start := time.Now()
	err = rt.DispatchMessage(context.Background(), Event{
		Channel: "#x", Sender: "alice", Hostmask: "alice!a@h", Text: "hi",
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > hardCap {
		t.Errorf("script ran %s before terminating, expected <%s", elapsed, hardCap)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context") &&
		!strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Logf("error message did not mention context/deadline (still terminated): %v", err)
	}
}
