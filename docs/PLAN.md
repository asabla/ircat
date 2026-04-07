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

- **Channel mode burst + ongoing MODE re-application on the
  receiver.** v1.0 forwards MODE lines but does not re-apply the
  bits, so a remote peer's view of `+t`/`+i`/`+k` drifts from the
  home server. Add a mode burst to `sendBurst` and have
  `handleRemoteMode` apply the changes via the existing
  `applyMode` machinery.
- **TS-based collision resolution.** Today the second user with a
  duplicate nick or the second peer to claim a channel just wins
  by accident. Add a per-record TS field, propagate it in burst
  and live messages, and use the lower TS as the tiebreaker per
  RFC 2813.
- **SQUIT recovery beyond "drop the link and forget".** When a
  peer goes away, the local node should:
  1. Send `:server SQUIT peer :reason` to every remaining link.
  2. Walk the world and remove every user whose `HomeServer`
     matches the dropped peer.
  3. Emit per-user QUIT broadcasts to every shared channel so
     local clients see the disappearance with the right
     hostmasks.
  Add a `Server.HandleSquit(peerName, reason)` entry point and
  call it from the `OnClosed` callback path.
- **Subscription-aware federation routing.** v1.0 fans every
  channel event to every peer. Switch to a per-channel
  subscription set built from the burst + JOIN/PART events so
  each event hits only peers that actually have a member in the
  channel. Keep the v1.0 fan-everything code path behind a
  `federation.broadcast_mode: fanout|subscription` config knob
  for one minor cycle so a regression can be flipped off
  without a redeploy.
- **KILL routing across links.** Operator KILL on node A
  currently disconnects only the local user. Forward `:nick KILL
  target :reason` over every link and have the receiver call
  `Server.KickUser` for the local conn or drop the remote-user
  record otherwise.

**Exit:** a three-node federation test where node A kills a user
on node C via node B; the user disappears on every node within
500ms; closing node C drops every C-homed user from A and B with
proper QUIT broadcasts.

---

### M10 — Lua sandbox tightening

**Goal:** stop relying on the wallclock + container memory cap as
the sandbox's only guard rails.

- **Per-call instruction budget.** Pin gopher-lua to a major that
  exposes a stable hook API (or ship a small fork that does), and
  attach `Sethook` with `lua.MaskCount` so the runtime decrements
  an instruction counter on every Nth instruction. Trip the call
  once the counter underflows, with the same context-cancel exit
  path the wallclock budget uses today. Keep the wallclock as the
  outer envelope.
- **Per-bot memory ceiling.** Track allocations via a custom
  allocator hook (or, if gopher-lua does not expose one, by
  periodically polling `runtime.MemStats` per goroutine via
  pprof's `goroutine` profile and refusing to schedule the next
  event when the bot's heap exceeds the ceiling). Document the
  trade-off in `docs/SECURITY.md`.
- **Allowlist for `string.format` directives.** `%n` is already
  blocked by gopher-lua but the audit found that an unconstrained
  `%s` with a controlled width can drive O(N²) allocations.
  Either cap the rendered length or refuse format strings whose
  argument count exceeds 16.
- **Sandbox fuzz target.** Add a `go test -fuzz` corpus that
  feeds randomly mutated Lua source through `NewRuntime` +
  `DispatchMessage` and asserts the runtime never panics, never
  outlives the wallclock budget, and never allocates beyond the
  configured ceiling.

**Exit:** `internal/bots/sandbox_test.go` grows three new cases
(instruction underflow terminates a tight loop in <2ms,
allocation cap prevents a 1 GiB blowup, fuzz seed corpus runs
clean for 60s).

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
