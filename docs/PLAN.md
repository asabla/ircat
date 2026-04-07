# PLAN — ircat v1.1.0

The v1.0.0 release shipped a feature-complete IRC server: full
client surface (RFC 1459/2812), persistent operators and channels,
htmx dashboard, admin API, sandboxed Lua bots, jsonl + webhook
event sinks, two-node federation with TLS, and a hardened
production compose stack. See [`PLAN-v1.0.md`](PLAN-v1.0.md) for
the historical record of M0 → M8.

v1.1.0 picks up the items that were explicitly deferred from v1.0
plus the polish work that the v1.0 audit surfaced. The cuts are
deliberately small — v1.1 is a hardening + polish release, not a
new-feature release. New surfaces (IRCv3 caps beyond CAP END,
SERVICE pseudo-server, full SQUIT recovery) live in v1.2+.

## Theme

> "Make the v1.0 surface boring to operate at scale, and close the
> obvious follow-ups from the v1.0 federation MVP."

## Milestones

### M9 — Federation hardening

**Goal:** the federation transport stops being "MVP" and starts
being production.

Shipped:
- Channel mode burst on link-up (TOPIC + canonical mode word +
  per-member +o/+v) and runtime MODE re-application on the
  receiver via a new `applyRemoteChannelMode` helper that
  forwards each toggle to the existing `state.Channel` setters.
- SQUIT recovery on link drop and remote SQUIT propagation:
  `Server.HandleSquit` walks the world, removes every user
  homed on the dropped peer, fans synthetic QUITs to local
  channel members, and forwards SQUIT to remaining links.
- Subscription-aware channel routing with the v1.0 fanout
  available behind `federation.broadcast_mode`. Subscriptions
  are tracked explicitly via `Server.SubscribePeerToChannel`,
  populated by `sendBurst` (when we tell a peer about a
  channel) and by `handleRemoteJoin` (when a peer tells us
  about one). JOIN remains fanned out so it can establish new
  subscriptions.
- KILL routing across links: local operator KILL forwards over
  every link; receivers fan a synthetic QUIT and either
  disconnect the local conn via the new `Host.DropLocalUser`
  hook or drop the remote-user record. KILL is one-shot per
  node so >2-node meshes do not loop.
- TS-based nick collision resolution per RFC 2813. Burst NICK
  shape carries TS as a positional param (with backward-
  compatible v1.0 sniffing); incoming-lower wins, incoming-
  higher gets KILL'd back to the peer. Channel JOIN burst lines
  carry channel TS and `AdoptOlderTS` lowers the local anchor.

Deferred to v1.2:
- Equal-TS nick collision (RFC says kill both — current code
  keeps the existing record on equal TS, which is conservative
  but rare at nanosecond resolution).
- Channel TS collision *behaviour* — v1.1 propagates the TS but
  does not yet drop op state on a peer's older claim. The next
  cycle wires AdoptOlderTS into the membership reset path.
- Ban list (+b) propagation across links.

**Exit:** every shipped item above is in main with at least one
integration test exercising it through two real Server
instances. ✅

---

### M10 — Lua sandbox tightening

**Goal:** stop relying on the wallclock + container memory cap as
the sandbox's only guard rails.

Shipped:
- **Per-call instruction budget via the existing wallclock.**
  Audit discovered that gopher-lua's VM dispatch loop already
  checks `L.ctx.Done()` between every Lua bytecode instruction,
  so the wallclock budget already enforces at instruction
  granularity — there is no separate hook API to attach. The
  `Budget.Instructions` field maps to a wallclock proxy at a
  conservative 10M-instructions/second rate via
  `Runtime.effectiveDeadline`, taking the lower of the two as
  the actual ceiling. Five new tests cover the four runaway
  vectors (tight loop, pcall-shielded loop, recursion, doubling
  string concat) and the instruction-proxy mapping.
- **Per-bot registry cap as a partial memory ceiling.**
  `Budget.RegistrySlots` wires through to gopher-lua's
  `RegistryMaxSize`, capping the data stack at a configurable
  number of LValue slots (default 65536 ≈ 1 MiB). When a
  script grows past the cap gopher-lua raises a Lua error
  which the runtime turns into a clean handler exit. Partial
  rather than a true allocator hook (gopher-lua doesn't expose
  one), so a script can still allocate large strings or
  userdata outside the registry — the compose stack's 1 GiB
  process cap is the outer envelope.
- **`string.format` hardening.** The default gopher-lua
  `string.format` is replaced with `safeStringFormat` which
  refuses widths and precisions above 1024, refuses calls with
  more than 16 args, and clamps the rendered output to 8192
  bytes. Five tests cover each rejection path plus the happy
  path. Closes the
  `string.format("%999999999s", "x")` allocation vector.
- **Fuzz target.** `FuzzSandboxNeverPanics` drives random Lua
  source through compile + dispatch and asserts no panic, no
  budget overrun. Verified end-to-end with a 10s real fuzz run
  (369k executions, 215 new corpus entries, zero failures).
  Doubles as a regression test in CI via the seed corpus.

Deferred to v1.2:
- True per-allocation memory hook (waiting on a gopher-lua
  release that exposes one).
- Soak fuzz beyond 60s as part of the M11 nightly job.

---

### M11 — Operational validation

**Goal:** stop guessing about the operating envelope. Replace
"conservative defaults" with measured numbers.

- **Soak test rig.** Write a `tests/soak/` harness (Go binary
  built off the existing `ircclient` test helper) that opens N
  connections, joins each one to M channels, and runs a sustained
  PRIVMSG load for D hours. Configurable via flags. Targets:
  - 10k concurrent registered connections.
  - 1k channels with average 10 members each.
  - 24h run on the reference Hetzner box documented in
    `docs/OPERATIONS.md`.
  - End-of-run assertions: zero dropped lines, zero protocol
    violations, RSS within 25% of the 1h baseline.
- **Flood-control benchmark suite.** `internal/server/floodcontrol`
  already has the token bucket; add `Benchmark*` cases that
  measure the steady-state ceiling at 1, 10, 100, 1000 senders
  and produce a CSV the docs can plot. Use the result to revise
  the default `message_burst` / `message_refill_per_second`
  values in `default-config.yaml` and `production.yaml`.
- **Federation latency benchmark.** Two-node Compose stack, one
  client on each side, measure the wall-clock between
  `c.Write([]byte("PRIVMSG #x :hi"))` on node A and the
  corresponding read on node B's client. Repeat for 100k samples
  and document the median + p99 in `docs/FEDERATION.md`.
- **Storage benchmark.** Time the SQLite vs. Postgres backends
  on the audit-write hot path (`storage.Audit().Append`) at
  1k/10k/100k events. Document the results in
  `docs/OPERATIONS.md` so operators can pick a backend with
  numbers in hand.

**Exit:** `docs/OPERATIONS.md` gains a "measured envelope"
section pointing at concrete CSV/text artefacts checked into
`tests/soak/results/`. The defaults in production.yaml are
updated to match the soak result.

---

### M12 — Polish & release plumbing

**Goal:** clean up everything that v1.0 left scuffed.

- **TLS termination on the dashboard listener.** The recommended
  v1.0 deployment fronts the dashboard with a reverse proxy. Make
  in-tree TLS work too, so the operator can opt out of the proxy
  layer. Reuse the existing `dashboard.tls.{cert_file,key_file}`
  fields that already round-trip through the config loader.
- **Reload-on-SIGHUP.** v1.0 documents which config sections are
  hot-reloadable but only the MOTD is actually wired. Wire
  `logging.level`, `operators` (statically configured ones), and
  the bot list. Everything else still requires a restart.
- **`/api/v1/config/reload` admin endpoint.** Same surface as
  SIGHUP but addressable from the API.
- **Release plumbing.** GoReleaser config, container image
  signing (cosign), SBOM generation, an `installer.sh` that
  fetches the latest tagged binary and a sample compose file.
- **Migration guide v1.0 → v1.1.** Empty section unless M9 ends
  up needing a TS-field migration on the persistent state — in
  which case the guide explains the cold-restart upgrade path.

**Exit:** `git tag v1.1.0` cuts a signed release whose changelog
covers M9 → M12 in operator-readable language.

---

## Cross-cutting

- Every commit still follows Conventional Commits — see
  `CLAUDE.md` for the type table.
- Every new feature still ships with at least one test, and
  every cross-API surface still ships with an e2e test.
- `docs/PROTOCOL.md` continues to absorb RFC ambiguities as
  they come up.
- `docs/CONFIG.md` is updated in the same commit as any new
  config field.

## Out of scope for v1.1

These were considered and explicitly cut. They live in a future
v1.2+ plan.

- IRCv3 capabilities beyond `CAP END` (`message-tags`,
  `account-tag`, `chghost`, ...).
- SERVICE pseudo-server.
- Multi-DC federation routing tables.
- Webhook v2 event payload schema (the v1 jsonl payload stays
  compatible for the whole 1.x line).
- Operator account federation (operator records are still
  per-node).
