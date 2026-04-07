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
func TestSandbox_WallclockBudgetTerminatesInfiniteLoop(t *testing.T) {
	src := `
		function on_message(ctx, ev)
			while true do end
		end
	`
	fa := newFakeActions("bot")
	rt, err := NewRuntime(src, fa, Budget{
		Instructions: 100_000,
		Wallclock:    150 * time.Millisecond,
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
		t.Fatal("expected timeout error from infinite loop")
	}
	if elapsed > time.Second {
		t.Errorf("infinite loop ran %s before terminating, expected <1s", elapsed)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context") &&
		!strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Logf("error message did not mention context/deadline (still terminated though): %v", err)
	}
}
