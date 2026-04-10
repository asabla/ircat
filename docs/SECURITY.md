# SECURITY.md — threat model and audit notes

This document captures ircat's security posture, the trust
boundaries between subsystems, and the audit work that has been
done in M8.

## Trust boundaries

| Boundary | Trusted side | Untrusted side | Mitigation |
|----------|--------------|----------------|------------|
| TCP listener (6667/6697) | the Go process | every IRC client | parser hard limits, flood control, ping timeouts, TLS optional on 6697 |
| Federation listener (7000) | the Go process | every peer node | PASS auth, server name match, optional TLS + fingerprint pin |
| Dashboard listener (8080) | the Go process | every operator browser | session cookies + CSRF, bearer-token API |
| Lua bot runtime | the Go process | every operator-uploaded bot script | sandboxed `lua.LState` (see below) |
| Persistence DB | the Go process | trusted | parameterized queries, no string-built SQL |

The "untrusted" sides assume an adversary can send arbitrary
bytes, replay/reorder packets, and (for the bot runtime) run
arbitrary Lua code. They cannot read host files, escalate
privileges, or break out of the container's read-only root.

## Lua sandbox audit (M8)

ircat embeds [gopher-lua](https://github.com/yuin/gopher-lua) to
run operator-supplied bot scripts. Bots are first-class
`state.User` records — they can JOIN channels, PRIVMSG, etc., but
they must not be able to escape into the host.

### What the sandbox blocks

`internal/bots/runtime.go` constructs every `lua.LState` with
`SkipOpenLibs: true` and only re-opens the libraries listed
below:

- `package` (forced — `OpenBase` requires it)
- `base`
- `table`
- `string`
- `math`

All other standard libraries (`io`, `os`, `debug`, `coroutine`,
`bit32`, ...) are intentionally absent.

After the libraries are open, the runtime strips the following
globals individually:

- `dofile`, `loadfile`, `load`, `loadstring` — block on-the-fly
  bytecode injection.
- `require`, `module` — block dynamic library loading by name.
- `string.dump` — block bytecode serialization (the standard
  ingredient in any `load()`-based escape).
- `package.loadlib` — replaced with a stub that raises an error
  so a script cannot `dlopen` a shared object.
- `package.preload`, `package.loaders`, `package.searchers` —
  reset to empty tables so even if `require` is reinstated by
  mistake the loader chain is a no-op.

### Pinned upstream

The sandbox lives on top of [`gopher-lua`](https://github.com/yuin/gopher-lua)
**v1.1.2**, which is the version every guarantee in this
document was validated against. The pin is in `go.mod`. Two
follow-ups documented in the v0.3 plan are still
**upstream-blocked** at this version:

- **Per-allocation memory hook.** gopher-lua does not expose a
  per-`malloc` callback the way the C reference implementation
  does. Without it the only memory bound we can express is the
  data-stack cap (`Budget.RegistrySlots`, see #2 below). Once
  upstream adds an allocator hook (or we vendor a small fork),
  `Budget.RegistryBytes` ships and the cap becomes a true
  heap ceiling.
- **Instruction-count `Sethook`.** gopher-lua does not expose
  the standard Lua debug hook with `lua.MaskCount`. The
  wallclock proxy in `Runtime.effectiveDeadline` is the
  closest we can get without a fork.

Quarterly upstream checks are tracked in
[W3-SANDBOX-TRACKER.md](W3-SANDBOX-TRACKER.md).

The CI fuzz job (`.github/workflows/ci.yml`) runs
`FuzzSandboxNeverPanics` for 5 minutes on every PR against
this exact pinned version, so any future bump bring its own
regression net.

### What the sandbox does and does not cover

1. **Per-instruction interruption.** gopher-lua's VM dispatch
   loop checks `L.ctx.Done()` between every Lua bytecode
   instruction. The wallclock budget set on the runtime context
   therefore terminates a runaway script at the very next
   instruction, even inside a tight `pcall`-wrapped infinite
   loop or a recursive runaway. The `Budget.Instructions`
   field maps to a wallclock proxy at a conservative 10M
   instructions/second rate via `Runtime.effectiveDeadline`.
   Verified end-to-end by `TestSandbox_WallclockBudget*` and
   `TestSandbox_InstructionBudgetMappingHonoured`.
2. **Per-bot memory ceiling (partial).** `Budget.RegistrySlots`
   wires through to gopher-lua's `RegistryMaxSize`, capping the
   data stack at a configurable number of `LValue` slots (~16
   bytes each, default 65536 ≈ 1 MiB). When a script grows a
   table or call chain past the cap gopher-lua raises a Lua
   error which the runtime turns into a clean handler exit.
   This is a *partial* heap cap — gopher-lua does not expose a
   true allocator hook, so a script can still allocate large
   strings or userdata outside the registry. The compose stack
   pins ircat to 1 GiB (`deploy.resources.limits.memory`) so a
   pathological string-only bot kills the container instead of
   the host. A post-v1.0 follow-up tracks moving to a real
   per-allocation hook once gopher-lua adds one.
3. **`string.format` width and arg bounds.** The default
   gopher-lua `string.format` delegates to `fmt.Sprintf` which
   honours arbitrary width specifiers, so
   `string.format("%999999999s", "x")` triggers an O(N) host
   allocation under operator-controlled width. The sandbox
   replaces `string.format` with `safeStringFormat` which
   refuses widths above 1024, refuses precisions above 1024,
   refuses calls with more than 16 arguments, and clamps the
   rendered output to 8192 bytes after dispatch. Verified by
   the `TestSandbox_StringFormat*` cases.
4. **CPU starvation across bots.** Each bot runs on its own
   goroutine so a slow bot only blocks itself. The wallclock
   deadline ensures it eventually returns.

### Regression coverage

`internal/bots/sandbox_test.go` enumerates every blocked global
and asserts the script cannot reach it. Any future change to
`OpenLibs` that reintroduces a dangerous symbol fails this test
loudly. The cases are:

- `io`, `os`, `debug` — must be nil.
- `dofile`, `loadfile`, `load`, `loadstring`, `require`, `module`
  — must be nil.
- `string.dump` — must be nil.
- `package.loadlib` — must raise.
- `package.preload`, `package.loaders`, `package.searchers` —
  must be empty tables.
- Wallclock budget terminates an infinite loop, a pcall-wrapped
  infinite loop, runaway recursion, and a doubling-string-
  concat loop within the configured deadline.
- Instruction budget proxy fires before the explicit wallclock
  when the instruction count is the tighter of the two.
- `string.format` rejects width > 1024, precision > 1024, more
  than 16 args, and rendered output > 8192 bytes.
- Registry cap stops a 1M-element table allocation from
  exhausting host memory.

Run with:

```sh
go test -race -count=1 -run TestSandbox ./internal/bots/
```

## Operator authentication

Two paths share the same `auth` package:

1. **Dashboard cookie sessions.** Created via `POST /dashboard/login`,
   the cookie holds an HMAC-signed opaque ID. Sessions are stored
   in-memory; restart wipes them by design.
2. **Bearer tokens for the API.** Tokens are minted via
   `POST /api/v1/tokens` and look like `ircat_<id>_<secret>`. The
   storage layer keeps only the SHA-256 hash of the secret. Tokens
   can be revoked via `DELETE /api/v1/tokens/{id}`.

Operator passwords are stored as argon2id hashes (see
`internal/auth.HashPassword`). The bcrypt path exists for legacy
import only and is never written by the live server.

## TLS

- IRC listeners support TLS via `listeners[].tls=true` and a
  `cert_file` / `key_file` pair. Minimum negotiated version is
  TLS 1.2.
- Federation links support TLS in both directions, with optional
  fingerprint pinning so operators can run a self-signed PKI
  without distributing a CA bundle.
- The dashboard listener does not yet terminate TLS itself —
  recommended deployment is to front it with a reverse proxy
  (nginx, caddy) that handles cert provisioning.

## Reporting

Please email security@<ircat-domain> with reproduction steps. Do
not file public issues for unpatched vulnerabilities.
