# PLAN — ircat v1.3.0

The v1.2.0 release closed the operator-experience gap (the
dashboard became a real console) and the federation
correctness loose ends from v1.1 (equal-TS kill-both, channel
TS reset, ban list propagation, SQUIT loop guard). See
[`PLAN-v1.2.md`](PLAN-v1.2.md) for the historical record of
M13 → M17, [`PLAN-v1.1.md`](PLAN-v1.1.md) for M9 → M12, and
[`PLAN-v1.0.md`](PLAN-v1.0.md) for M0 → M8.

v1.3.0 is a **stabilisation + small-scope cleanup** release.
There are no headline new features. The themes are:

1. Drop the v1.2 deprecations (`federation.broadcast_mode:
   fanout`).
2. Wire the gopher-lua follow-ups if upstream has shipped the
   hooks; if not, vendor the patch ourselves so v1.3 actually
   delivers a true memory cap.
3. Fix the test-cleanup deadlock that the M14 three-node
   integration test surfaced (the SQUIT-during-cleanup hang).
4. Pick up whatever audit findings the nightly soak job
   uncovered between v1.2.0 and v1.3 cut.

## Theme

> "Pay down the small things, finish the upstream-blocked
> sandbox work, and ship a release that mostly tells operators
> nothing new is required."

## Milestones

### M18 — Cleanup

- **Drop `federation.broadcast_mode: fanout`.** Documented
  for one minor cycle in v1.1; removed in v1.3 per the
  deprecation note. The default `subscription` mode has been
  green in every measured case so the safety net can go.
- **Fix the SQUIT-during-cleanup deadlock.** The M14
  integration test for the three-node SQUIT scenario was
  scoped down because the cleanup sequence (drop one link,
  fan SQUIT, defers tear down the rest) caused a flaky
  shutdown deadlock. The deadlock is in the test harness, not
  the production path, but the harness should not have it
  either.
- **Audit + remove every `// silence unused` placeholder.**
  Several files still carry leftover `var _ = fmt.Sprintf`
  bridges from M0 / M1 scaffolding. Clean them out.

**Exit:** `git grep "broadcast_mode" docs/` returns empty,
the three-node SQUIT scenario re-enters the integration test
suite, and `git grep "silence unused"` returns empty too.

---

### M19 — Lua sandbox upstream catch-up

- **Per-allocation memory cap (if upstream).** Track
  gopher-lua quarterly. If a stable allocator hook has
  shipped by the time v1.3 enters dev, wire
  `Budget.RegistryBytes` through to it and update
  `docs/SECURITY.md` to drop the partial-cap caveat. If not,
  vendor the smallest possible patch — the hook surface is
  about 30 lines of Go in the upstream `_state.go` — and
  pin our fork in `go.mod` via a `replace` directive.
- **Per-call instruction count via `Sethook`.** Same gating.
  If upstream has the hook API, attach a real instruction
  counter that decrements on the count mask and trips the
  context-cancel exit path. The wallclock proxy stays as the
  outer envelope.
- **Ship a vendored fork if necessary.** The v1.2 plan
  considered this and decided against it; v1.3 commits to
  doing it if upstream still has not moved.

**Exit:** the `SECURITY.md` "what the sandbox does and does
not cover" section drops the partial-cap caveats. Both items
are tested via new sandbox tests that exercise the
allocation-overrun and instruction-overrun paths and assert
the runtime exits cleanly.

---

### M20 — Operational follow-ups

- **Triage findings from the v1.2 nightly soak.** The job
  runs every night against the v1.2 main; whatever
  regressions or rate cliffs it surfaces between v1.2.0 and
  v1.3 cut land here.
- **Refresh the measured envelope** in `docs/OPERATIONS.md`
  if any benchmark numbers shifted by more than 5 % between
  v1.2 and v1.3 (driven by upstream Go bumps, sqlite version
  bumps, or our own changes).
- **Optional: Postgres benchmark on a real RDS instance.**
  v1.2 documented the drill but did not run it. v1.3 ships
  the numbers if the operator has access to a tuned managed
  Postgres. Skipped without prejudice if not.

**Exit:** the measured envelope tables in `OPERATIONS.md`
match the latest reality, and any soak-surfaced regression
has either been fixed or has its own dedicated issue.

---

### M21 — Release polish

- **Migration guide v1.2 → v1.3.** Will be short — v1.3 is a
  stabilisation release. The main thing operators need to
  know is the `broadcast_mode: fanout` removal; everything
  else is invisible.
- **Tag `v1.3.0`** with the same release pipeline as v1.1 /
  v1.2 (goreleaser, syft, cosign keyless).

**Exit:** `git tag v1.3.0` produces signed cross-platform
archives, a multi-arch container image on ghcr.io, and a
GitHub release whose body points at the migration guide.

---

## Cross-cutting

- Conventional Commits as always.
- Every change still ships with at least one test.
- The CI fuzz job from M16 stays at 5 minutes per PR; v1.3
  is the cycle where we evaluate whether to bump it to
  10 minutes if the corpus has stabilised.

## Out of scope for v1.3

These were considered and explicitly cut. They live in a
v2.0 plan if anywhere.

- IRCv3 capabilities beyond `CAP END` (`message-tags`,
  `account-tag`, `chghost`, ...).
- SERVICE pseudo-server.
- Multi-DC federation routing tables.
- Webhook v2 event payload schema (the v1 jsonl payload
  stays compatible for the whole 1.x line).
- Operator account federation across nodes.
- A real chat surface in the dashboard.
- TS6 SID routing.
- Burst compression (`zip` flag).
- Web push notifications for audit events.
