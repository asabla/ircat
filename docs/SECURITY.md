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

### What the sandbox does not (yet) cover

1. **Per-call instruction budget.** The current safety net is a
   per-call wallclock deadline (default 5s) enforced by setting
   the gopher-lua `Context` and letting the runtime cancel.
   Tight infinite loops terminate within the deadline (verified
   in `TestSandbox_WallclockBudgetTerminatesInfiniteLoop`) but a
   busy script can spend its full 5s budget on every event. A
   future commit can attach a `Sethook` that decrements an
   instruction counter once gopher-lua exposes a stable hook API.
2. **Memory budget.** gopher-lua does not expose a hard memory
   cap. A pathological script can allocate until the per-process
   resource limit kicks in. The compose stack pins ircat to 1 GiB
   (`deploy.resources.limits.memory`) so a runaway bot kills the
   container instead of the host.
3. **CPU starvation across bots.** Each bot runs on its own
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
- Wallclock budget terminates an infinite loop within the
  configured deadline.

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
