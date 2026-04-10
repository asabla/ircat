# PLAN — ircat post-v1.0

The v1.0.0 release is the RFC-complete milestone. Every command,
mode, prefix, and numeric the IRC RFC family (1459, 2810, 2811,
2812, 2813) defines is implemented and tested, plus four IRCv3
capabilities (`message-tags`, `server-time`, `echo-message`,
`multi-prefix`). See the v1.0.0 tag annotation for the full
catalog and the historical pre-1.0 plans for the development arc:

- [`PLAN-v0.1.md`](PLAN-v0.1.md) — original feature-complete release (M0 → M8)
- [`PLAN-v0.2.md`](PLAN-v0.2.md) — federation hardening + lua sandbox (M9 → M12)
- [`PLAN-v0.3.md`](PLAN-v0.3.md) — dashboard polish + federation correctness (M13 → M17)

This document is the **post-v1.0 forward plan**. The themes are:

1. Round out IRCv3 with the remaining commonly-negotiated caps.
2. Sharpen federation: fix the M14 cleanup deadlock and add
   stress coverage for a real three-node mesh under load.
3. Pay down the gopher-lua memory-hook story now that v1.0
   shipped without the upstream API.
4. Build a real services daemon on top of the SERVICE framework.
5. Operational hardening: soak findings, perf bumps, optional
   Postgres-on-RDS numbers.

There is no fixed minor-version cadence beyond v1.0. Items will
land in whatever order they're ready, and a tag goes out when a
coherent batch is shippable.

## Theme

> "v1.0 closed the RFC chapter. Post-v1.0 closes the IRCv3
> chapter, sharpens federation, and builds the things on top
> that the RFC compliance work made possible."

## Workstreams

### W1 — IRCv3 catch-up

The four caps shipped in v1.0 are the table-stakes set
(`message-tags`, `server-time`, `echo-message`, `multi-prefix`).
Three more independent caps shipped on top of v1.0 in this
workstream: `userhost-in-names`, `away-notify`, `invite-notify`.
The remaining tier needs prerequisites.

Shipped post-v1.0:

- ✅ **`userhost-in-names`** — NAMES renders the full
  `nick!user@host` triplet for negotiating clients so a
  follow-up WHO is unnecessary.
- ✅ **`away-notify`** — pushes `:nick!u@h AWAY :reason`
  (and the bare `AWAY` form on return) to every shared-channel
  member with the cap when AWAY toggles.
- ✅ **`invite-notify`** — pushes `:inviter!u@h INVITE target
  #chan` to every channel operator with the cap when an
  INVITE happens.

Shipped post-W4:

- ✅ **`account-tag`** — attaches `@account=<name>` on every
  message from a logged-in account. Wired in `Conn.send`.
- ✅ **`extended-join`** — adds the account name and realname as
  extra params on JOIN broadcasts. Per-recipient branching.
- ✅ **`batch`** — frames NAMES and WHO reply bursts in
  `BATCH +ref` / `BATCH -ref` with `@batch=ref` tags.

Still pending:

- **`chghost`** — notifies channel members when a user's host
  changes. Today the host is captured at registration and never
  rewritten, so this is a near-no-op until the cloak / vhost
  work lands. Wire the cap negotiation when the forthcoming
  MODE +x cloak path appears.

**Exit:** all the unblocked caps shipped (✅). Only `chghost`
remains, blocked on the MODE +x cloak design.

---

### W2 — Federation sharpening

- **Three-node mesh stress test.** The M14 (now v0.3.0)
  integration test was scoped down because the SQUIT-during-
  cleanup teardown caused a flaky shutdown deadlock in the test
  harness. Fix the harness, re-enable the test, and add a
  brand-new soak harness that drives a real three-node mesh
  under sustained load (100s of users, dozens of channels,
  scripted netsplit / netjoin cycles).
  - ✅ Mesh soak harness landed in `tests/soak/mesh.go`:
    `RunMesh()` distributes clients across 3 nodes, runs
    sustained PRIVMSG with cross-node probe verification.
- ✅ **Per-link byte counter dashboard panel.** The dashboard
  federation page now shows sent/recv messages and bytes per link.
  A JSON API endpoint (`GET /api/v1/federation/links`) is also
  available for external monitoring.
- **Burst compression (`zip` flag).** RFC 2813 mentions zlib-
  compressed bursts as an option. Useful on slow links carrying
  large channel state, optional for everything else.
- **Operator account federation.** A user OPER'd on node A is
  not OPER'd on node B today. The store is local. A small
  RPC over the link should let an OPER decision propagate so
  KILL / WALLOPS / DIE work consistently across the mesh.

**Exit:** three-node test back in `internal/server/`,
dashboard fed panel shows live byte counts, optional zlib
fanout exposed via config flag.

---

### W3 — Lua sandbox upstream catch-up

Carried over from the v0.3.0 plan. gopher-lua has not yet shipped
the per-allocation memory hook or the per-call instruction
counter API. The v1.0 release ships the same partial-cap
caveats v0.3 documented.

- **Track upstream.** Quarterly check on the gopher-lua release
  notes. If the hooks land, wire them up.
- **Vendored fork if upstream stalls again.** v0.3 considered
  this and decided against it; post-v1.0 commits to actually
  doing it if upstream still hasn't moved within two quarterly
  checks.

**Exit:** `docs/SECURITY.md`'s "what the sandbox does and does
not cover" section drops the partial-cap caveats. New tests
cover the allocation-overrun and instruction-overrun paths.

---

### W4 — Services daemon

The SERVICE / SQUERY / SERVLIST framework shipped in v1.0
accepts service registrations from any connecting client. There
is no in-tree service implementation. A first-party services
daemon would let operators run a usable network without bringing
their own ChanServ.

- ✅ **Account framework.** Per-user accounts with password,
  email, and SASL PLAIN auth at registration time.
  AccountStore + SQLite/Postgres drivers + migrations shipped.
- ✅ **NickServ.** REGISTER, IDENTIFY, nick enforcement timer
  (60s identify-or-guest-rename), wired into server lifecycle
  via BotDeliverer. `ircat services` subcommand ships it.
- **ChanServ.** Channel registration, founder restoration after
  empty, op grants on join for known accounts, ban-on-disconnect
  rules.
- **MemoServ.** Offline messages forwarded the next time the
  recipient connects.

These are large pieces and will likely span multiple tagged
releases. The minimum useful subset is the account framework
plus NickServ. The full design is documented in
[`SERVICES.md`](SERVICES.md).

**Exit:** `ircat services` is a real subcommand that brings up
ChanServ + NickServ on the local node, registered with the
SERVICE form, persisting accounts to the same store.

---

### W5 — Operational hardening

- **Triage v0.3 nightly soak findings.** The job has been
  running against `main` since v0.3.0 cut. Fold any surfaced
  regressions into the post-v1.0 work.
- **Refresh the measured envelope** in `docs/OPERATIONS.md`
  if benchmark numbers shifted by more than 5 % between v0.3
  and the next tag.
  - ✅ Envelope structure refreshed for v1.1: v0.2 baseline
    preserved, v1.1 placeholder tables added with "how to
    reproduce" commands. Awaiting a dedicated benchmark run
    on the reference hardware to fill the numbers.
  - ✅ CAP negotiation benchmark added
    (`handler_sasl_bench_test.go`) to cover the registration
    hot path; will extend for SASL when W4 lands.
- **Postgres-on-RDS benchmark.** Documented in v0.3 but never
  run for real. Post-v1.0 ships the numbers if a tuned managed
  Postgres is reachable.
  - ✅ RDS setup documented in `OPERATIONS.md` (instance type,
    pg version, network topology, exact commands). Awaiting
    access to a tuned managed Postgres environment.
- **Per-handler unit-test files.** `handler_message`,
  `handler_query`, and `handler_mode` are integration-tested
  but lack focused unit suites. Add them so the inner-loop
  regression detection is faster.
  - ✅ `handler_message_test.go` — moderated, quiet, pre-reg.
  - ✅ `handler_query_test.go` — WHOIS edge cases, NAMES
    visibility, LIST filtering, WHO anonymous.
  - ✅ `handler_mode_test.go` — batched modes, boolean modes,
    quiet list, user mode edge cases, ban removal.
- **Soak findings log.** [`SOAK-FINDINGS.md`](SOAK-FINDINGS.md)
  tracks nightly and ad-hoc results in a structured table.

**Exit:** measured envelope tables in `OPERATIONS.md` reflect
current reality; the three large handlers each have their own
`_test.go` file (✅ done).

---

## Cross-cutting

- Conventional Commits as always.
- Every change still ships with at least one test.
- The CI fuzz job stays at 5 minutes per PR; the corpus has
  stabilised so a bump to 10 minutes is on the table once W3
  lands.
- The compile-time `iface_check_test.go` pattern from v1.0
  (federation/server boundary) is the template for any other
  cross-package interface dance.

## Out of scope for the immediate post-v1.0 work

These remain on the longer horizon:

- TS6 SID routing — pure-name federation works fine for the
  expected mesh size; SID routing is a power-user feature.
- Multi-DC federation routing tables.
- Web push notifications for audit events.
- A real chat surface in the dashboard.
- IRCv3 batched-history replay (`+chathistory`) — depends on
  W4 (storage) and `batch` (W1).
