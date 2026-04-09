# PLAN — ircat v0.3.0 (historical)

> **This is the historical record for the v0.3.0 release.** Every
> milestone below shipped to main and is tagged as `v0.3.0`. The
> active forward plan lives in [`PLAN.md`](PLAN.md). The v0.1
> historical record is in [`PLAN-v0.1.md`](PLAN-v0.1.md), the v0.2
> historical record in [`PLAN-v0.2.md`](PLAN-v0.2.md).

The v0.2.0 release tightened the federation transport, hardened
the Lua sandbox, replaced "conservative defaults" with measured
numbers, and shipped the polish + release plumbing. See
[`PLAN-v0.2.md`](PLAN-v0.2.md) for the historical record of M9
→ M12, and [`PLAN-v0.1.md`](PLAN-v0.1.md) for M0 → M8.

v0.3.0 is the **operator experience** release. The headline is
that the dashboard stops being a row of read-only HTML tables
and starts being a thing operators actually want to use. The
secondary theme is closing the items v0.2 explicitly deferred:
the equal-TS collision case, channel TS collision behaviour,
ban-list propagation, the soak / Postgres benchmarks at scale,
and a real per-allocation Lua memory hook if gopher-lua exposes
one in time.

## Theme

> "Make the operator dashboard a place you actually open. Close
> the v0.2 deferred list. Don't add new wire protocols."

## Milestones

### M13 — Dashboard polish

**Goal:** the operator dashboard stops being a row of static
tables and becomes a live operator console. No SPA framework,
no build step, no JS bundles — htmx + a tiny amount of vanilla
JS for the SSE wiring.

- **HTMX inclusion + auto-refresh on overview.** Drop the
  htmx script under `/dashboard/static/` (one file, ~14 kB
  vendored) and switch the overview page to poll its content
  block every 5 seconds. Same handler renders the partial when
  `HX-Request` is set, so non-JS clients still get the full
  page.
- **Live log tail page.** The `internal/logging` ring buffer
  has been built specifically for this since M0 and is
  currently unused by any UI. Add a `/dashboard/logs` page
  that subscribes to `/dashboard/logs/sse` (Server-Sent
  Events) and streams new entries as they land. Filter by
  level, search by substring.
- **Federation panel.** New `/dashboard/federation` page
  showing every active link from the registry: peer name,
  state, address, last activity, subscription set size, and a
  "drop link" button that calls into the supervisor.
- **Bot panel** with full CRUD via forms instead of API-only.
  List bots, toggle enabled/disabled, view source, view kv
  state, restart. Reuses `BotManager` already wired in v0.1.
- **Channel detail page.** Click a channel name from the list
  to see members (with op/voice prefixes), modes, current
  topic, ban list. Forms for: edit topic, toggle mode, kick
  member, ban member, set ban-mask exception.
- **User detail page.** Click a nick from the list to see
  every channel they're in, last activity, ident/host,
  hostmask. Forms for: kick, kill (operator-only), set
  per-user mode.
- **Operator + token forms.** Today operators are created via
  POST /api/v1/operators with curl. Add a form on the
  operators page for create + delete. Same for API tokens on
  a new `/dashboard/tokens` page (currently no UI at all).
- **Metric card grid + sparklines.** Pull from the same
  `MetricsSource` interface `/metrics` uses, render the
  numbers as cards on the overview, and draw a sparkline of
  the last 60 samples for `messages_in_total` /
  `messages_out_total` / `users` / `channels`. Sparklines are
  inline SVG, no charting library.
- **CSS polish.** Replace the 88-line classless stylesheet
  with a sidebar nav + card layout + status pill components.
  Still no fonts, no JS framework, still readable in lynx.
- **Search/filter** on the user and channel tables — a tiny
  client-side filter input that hides rows whose text content
  does not contain the query. No backend round-trip.
- **CSRF on all mutating forms.** Today only the kick action
  has a CSRF token; the rest of the new forms in this
  milestone need the same protection.

**Exit:** an operator can drop into the dashboard, see the
current state of the network at a glance, click into a noisy
user, kill them, and watch the audit + log tail confirm the
action — all without touching curl. The container ships with
htmx vendored under `/dashboard/static/htmx.min.js`.

---

### M14 — Federation correctness loose ends

**Goal:** close the items v0.2 documented as deferred under
"Federation correctness". None of these are headline features;
they exist to make the federation transport correct under
adversarial timing.

- **Equal-TS nick collision (RFC 2813 §5.2).** v0.2 keeps the
  existing record on equal TS, which is conservative but rare
  at nanosecond resolution. The RFC says kill both. Implement
  the kill-both branch and add a regression test that drives
  matching TS values into both sides of a link.
- **Channel TS collision behaviour.** v0.2 propagates the
  channel TS via `AdoptOlderTS` but does not yet drop op
  state on a peer's older claim. Wire `AdoptOlderTS` into the
  membership reset path so the older anchor wins consistently
  on every node.
- **Ban list (+b) propagation.** v0.2 drops `+b` toggles in
  `applyRemoteChannelMode`. Add a per-channel ban map to the
  burst (one `:server BAN #chan mask ts setby` line per ban)
  and re-apply on the receiver via the existing
  `state.Channel.AddBan`.
- **SQUIT loop guard.** When `Server.HandleSquit` forwards
  SQUIT to remaining peers it does not yet stamp a hop
  counter, so a future >3-node mesh could re-introduce a
  flood. Add a tiny "seen" set keyed on `(peer, reason)` that
  expires after 5 seconds, sufficient to break a fan-out
  loop.

**Exit:** the same three-node integration test the v0.2 plan
called for, now actually present in `internal/server/`. Drives
collisions, channel TS resets, ban propagation, and a SQUIT
storm; all four scenarios converge to the same state on every
node within 500 ms.

---

### M15 — Operational validation at scale

**Goal:** turn the v0.2 soak / benchmark *capability* into
soak / benchmark *results*.

- **Nightly soak job.** Schedule the existing
  `tests/soak` harness against a real ircat instance via a
  GitHub Actions cron. Targets:
  - 5 000 concurrent connections, 500 channels, 1 hour, 0.1 %
    drop rate ceiling. (Smaller than the v0.2 reference
    target, large enough to find regressions.)
  - The job uploads the per-run summary as a workflow
    artefact and posts a comment on the latest commit if
    drops exceed the ceiling.
- **24h reference soak.** Manual trigger of the harness
  against the reference Hetzner box for the v0.2 reference
  target (10k conns, 1k channels, 24 h). Document the result
  and the host config in `docs/OPERATIONS.md`. This is the
  one v0.2 was missing — the harness was there, the run was
  not.
- **Postgres benchmark on tuned hardware.** Same idea — the
  benchmark Skips cleanly without `IRCAT_TEST_POSTGRES_DSN`,
  so v0.2 had no published Postgres numbers. Run it against a
  real RDS-class box and put the result in
  `docs/OPERATIONS.md` next to the SQLite numbers.
- **Federation latency on real loopback.** Re-run
  `BenchmarkFederation_PrivmsgRoundtrip` against actual TCP
  loopback (and ideally a 1 ms / 10 ms / 100 ms LAN
  emulation via `tc qdisc`). v0.2's number is from
  `net.Pipe`; document the realistic ones in
  `docs/FEDERATION.md`.

**Exit:** `docs/OPERATIONS.md` has measured numbers from real
hardware for every benchmark, and the nightly job has been
green for at least one full week before tagging.

---

### M16 — Lua sandbox follow-ups

**Goal:** finish what M10 deferred when gopher-lua did not
expose the right hooks.

- **True per-allocation memory cap.** If gopher-lua has added
  an allocator hook by the time v0.3 enters dev (track the
  upstream project quarterly), wire `Budget.RegistryBytes`
  through to it. If not, document why the registry slot cap
  is the closest we can get and pin the gopher-lua version we
  rely on.
- **Per-call instruction count via Sethook.** Same gating —
  if gopher-lua exposes `Sethook` with a count mask in time,
  add a real instruction counter that decrements on the hook
  and trips through the existing context-cancel exit path.
  The wallclock proxy stays as the outer envelope.
- **Sandbox fuzz job in CI.** Run
  `FuzzSandboxNeverPanics` for 5 minutes on every PR via a
  separate GitHub Actions matrix entry. Today the seed corpus
  runs as a regression test but real fuzzing only happens
  manually.

**Exit:** the SECURITY.md "what the sandbox does and does not
cover" section loses the partial-cap caveats, OR the doc
explicitly pins the gopher-lua version we have validated and
the v0.4 plan inherits the open items.

---

### M17 — Release polish

**Goal:** v0.3 ships with v0.2 → v0.3 migration guidance and
the v0.3 release pipeline produces the same shape of artefacts
as v0.2.

- **Migration guide v0.2 → v0.3.** Mostly empty unless M14 or
  M16 introduces a behaviour change that needs operator
  attention. The dashboard polish in M13 is additive — no
  upgrade action needed.
- **Release notes generator.** Today the v0.1.0 and v0.2.0
  release notes are hand-written annotated tag messages.
  Switch to the goreleaser changelog generator + a small
  prelude template so the next major does not need a
  hand-typed wall of text.

**Exit:** `git tag v0.3.0` produces a release with the same
GitHub Actions pipeline as v0.2.0, plus the dashboard images
visible at `https://github.com/asabla/ircat/releases/tag/v0.3.0`.

---

## Cross-cutting

- Conventional Commits, same as every prior milestone.
- Every new dashboard page ships with at least one HTTP-level
  test that drives the form / handler end-to-end. The TLS test
  pattern from v0.2 is the template.
- `docs/PROTOCOL.md`, `docs/CONFIG.md`, `docs/DASHBOARD.md` get
  updated in the same commit as the change.
- The `gocyclo` / `staticcheck` CI gates stay in place — no
  drop in code quality is acceptable for the dashboard work.

## Out of scope for v0.3

These were considered and explicitly cut. They live in a
future v0.4+ plan.

- A real chat surface in the dashboard (operators can use a
  regular IRC client; the dashboard is for moderation, not
  participation).
- IRCv3 capabilities beyond `CAP END` (`message-tags`,
  `account-tag`, `chghost`, ...).
- SERVICE pseudo-server.
- Multi-DC federation routing tables.
- Webhook v2 event payload schema.
- Operator account federation across nodes.
- An npm/yarn build for the dashboard. The "no build step,
  htmx + vanilla JS, vendored as a single file" rule from
  CLAUDE.md still applies.
