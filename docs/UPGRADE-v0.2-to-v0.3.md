# Upgrading from v0.2 to v0.3

This guide walks through every change a v0.2 operator will see
when they upgrade to v0.3. The headline up front: **in-place
upgrade, no config changes required, no migrations**. v0.3 is
an operator-experience release: the dashboard becomes a real
console, the federation transport closes its v0.2 deferred
correctness items, and the operational story gets a nightly
soak job and a TCP-loopback latency benchmark to go with the
existing measured envelope.

## Compatibility

| Surface | v0.2 → v0.3 status |
|---|---|
| Config schema | Backward-compatible. No new required fields. |
| Persistent SQLite databases | No migration. Existing databases load unchanged. |
| Persistent Postgres databases | Same. No migration. |
| Federation wire protocol | Backward-compatible with v0.2 peers. New burst lines (channel modes, ban list, +o/+v per-member, channel TS) are additive — v0.2 receivers ignore the parts they do not understand. |
| Lua bot API surface | Backward-compatible. Same sandbox, same gopher-lua pin. |
| Admin API endpoints | Additive. No removals. |
| Metrics endpoint | Unchanged. Same metric names and labels. |
| Dashboard URL surface | Additive. Every new page is reachable from the new sidebar; old direct URLs still work. |

There are no breaking changes. You can do an in-place upgrade.

## Upgrade steps

1. Take a backup. (See `OPERATIONS.md` for the SQLite +
   Postgres recipes.)
2. Pull the new image: `docker compose pull ircat`.
3. Restart: `docker compose up -d ircat`.
4. Verify `GET /readyz` returns 200 and `ircat_messages_in_total`
   keeps incrementing.

That's it.

## What changes automatically

### Operator dashboard becomes a real console (M13)

The single biggest visible change. The v0.2 dashboard was five
read-only HTML tables with a single kick action. The v0.3
dashboard ships with:

- **Sidebar nav** with active-page highlighting (overview,
  users, channels, federation, bots, operators, tokens, audit
  log, live logs).
- **Live overview** with six metric cards that auto-refresh
  every 5 seconds via htmx polling, each carrying an inline
  SVG sparkline of the last 60 samples.
- **Federation panel** showing every active link with state,
  description, and the channel subscription set.
- **Bot CRUD via forms** — create, toggle enabled, view source,
  edit source, delete. The supervisor hot-reloads on every
  change. Replaces the curl-against-`/api/v1/bots` workflow.
- **Channel detail page** with topic edit, member list (op /
  voice / local / remote pills), and ban list.
- **User detail page** with channel membership and a kick
  form that takes an optional reason.
- **Operator + token forms** — create operator with password
  (hashed via argon2id before persisting), mint token (the
  plaintext is shown exactly once in a flash bar after
  creation), revoke either.
- **Live log tail page** that streams the in-memory ring
  buffer via `/dashboard/logs/sse`. Filter by level, filter by
  substring.
- **Search filters** on the user and channel tables (client-
  side, no backend round-trip).
- **CSRF on every mutating form** including the logout button.

Visual: replaces the 88-line classless stylesheet with a
sidebar-layout sheet, status pill components, dark mode via
`prefers-color-scheme`. Vendored htmx 1.9.12 (~48 KB) is the
only new asset; no JS framework, no build step.

**No action required.** The new dashboard is what you get on
the first GET after the restart.

### Federation correctness loose ends (M14)

Four items v0.2 documented as deferred and that v0.3 closes:

1. **Equal-TS nick collision (RFC 2813 §5.2 kill-both).** v0.2
   kept the existing record on equal TS. v0.3 implements the
   RFC kill-both branch — both copies disappear and a KILL
   line goes back to the peer. Vanishingly rare in practice
   at nanosecond resolution but the gap was real.
2. **Channel TS reset wired into membership.** v0.2
   propagated `AdoptOlderTS` but did not drop op state on a
   peer's older claim. v0.3 strips per-member flags
   (`ResetMembershipFlags`) when the older anchor wins so the
   bursted MODE +o / +v lines that follow are authoritative.
3. **Ban list propagation.** `+b` masks now ride along in the
   burst (one MODE +b line per ban) and runtime `+b` / `-b`
   are mirrored on the receiver via the existing
   `applyRemoteChannelMode` path. Local PRIVMSG enforcement on
   the receiver therefore honours the same blocks the home
   server does.
4. **SQUIT loop guard.** A small `(peer, reason)` seen-set
   with a 5-second TTL keeps the SQUIT fan-out to one
   execution per node in a >3-node mesh. Without it a 4-node
   mesh would re-run the cleanup every time SQUIT comes
   around the loop.

Plus a **subscription routing fix** the ban-list test
surfaced: when a node fans a JOIN out, it now self-subscribes
each peer to the channel on the local side too, closing a
chicken-and-egg gap where a follow-up MODE would not route
back through `forwardChannelToSubscribed` because the local
node had no record of the peer knowing the channel.

The wire format changes are additive — a v0.2 peer that
receives the new burst lines (TOPIC, MODE word, MODE +b
masks, per-member +o/+v, channel TS) just delivers them to
local clients without re-applying. **No action required**;
the improvement happens automatically on the v0.3 side of any
link.

### Operational validation at scale (M15)

- **Nightly soak job.** `.github/workflows/soak.yml` runs the
  existing `tests/soak` harness against a fresh ircat every
  day at 03:00 UTC. 5 000 conns / 500 channels / 1 hour /
  0.1 % drop ceiling. Logs and the soak summary are uploaded
  as workflow artefacts on every run.
- **TCP loopback federation latency benchmark.** v0.2's
  number was measured against `net.Pipe` — the floor of what
  the broadcast logic itself costs. v0.3 adds a real TCP
  loopback variant that captures the kernel cost. Reference
  numbers on the v0.2 reference box: `~38 µs net.Pipe` vs
  `~51 µs TCP loopback`, so the kernel adds ~13 µs mean / ~26
  µs p99. See `docs/FEDERATION.md` for the side-by-side
  table.
- **Documented 24h reference soak + Postgres benchmark
  drill.** `docs/OPERATIONS.md` gains an explicit invocation
  for the 24h reference run (10k conns / 1k channels /
  0.05 % drop ceiling, real hardware) and the Postgres
  audit-event benchmark (against a tuned managed instance via
  `IRCAT_TEST_POSTGRES_DSN`). The v0.2 plan asked for both;
  the v0.2 release shipped the harness but not the doc.

### Lua sandbox follow-ups (M16)

- **CI fuzz job.** A new GitHub Actions matrix entry runs
  `FuzzSandboxNeverPanics` for 5 minutes on every PR against
  the pinned `gopher-lua v0.2.2`. The seed corpus already
  ran as a regression test in v0.2; v0.3 adds real fuzzing
  coverage without waiting for a manual nightly trigger.
- **Pinned upstream documentation.** `docs/SECURITY.md`
  gains a Pinned upstream section explaining that the
  per-allocation memory hook and the `Sethook`-based
  instruction count remain upstream-blocked at gopher-lua
  v0.2.2, what the fallback bounds are
  (`Budget.RegistrySlots`, the wallclock proxy), and which
  test coverage validates the close-out at this version. The
  upgrade to those features lands when upstream gopher-lua
  ships them.

**No action required for any M16 item.** The fuzz job runs in
CI; the documentation is a read-only update.

## What you can opt into

Nothing new in v0.3 requires opt-in. Every behaviour change
is on by default and additive. The federation
`broadcast_mode: fanout` knob from v0.2 still exists for one
more cycle as a regression fallback but is documented as
**deprecated** — it will be removed in v0.4.

## Removed

Nothing.

## Deprecated

- `federation.broadcast_mode: fanout` — still works in v0.3,
  removed in v0.4. The default `subscription` mode is strictly
  better in every measured case.

## Need help

- `docs/OPERATIONS.md` for the day-2 surface, including the
  measured envelope tables and the new soak / Postgres drill.
- `docs/SECURITY.md` for the Lua sandbox audit notes.
- `docs/FEDERATION.md` for the new TCP-loopback latency
  numbers and the side-by-side table.
- Open an issue if a v0.3 default surprises you in a bad way.
