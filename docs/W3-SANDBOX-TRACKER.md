# W3 — Lua Sandbox Upstream Tracker

Quarterly check on [gopher-lua](https://github.com/yuin/gopher-lua)
for the two upstream-blocked features documented in
[SECURITY.md](SECURITY.md).

## Blocked features

1. **Per-allocation memory hook.** gopher-lua does not expose a
   per-malloc callback. Without it, `Budget.RegistryBytes` cannot
   express a true heap ceiling. The workaround is the registry-slot
   cap (`Budget.RegistrySlots`).

2. **Instruction-count debug hook** (`lua.MaskCount`). gopher-lua
   does not expose the standard Lua debug hook with instruction
   counting. The workaround is the wallclock proxy in
   `Runtime.effectiveDeadline`.

## Decision framework

- If upstream ships both hooks: wire them up, remove the partial-cap
  caveats from SECURITY.md, add new tests.
- If upstream ships one but not the other: wire up what is available,
  keep the remaining caveat.
- If upstream has not moved after two consecutive quarterly checks
  (6 months): commit to a vendored fork with the minimal patches.

## Check log

| Date | gopher-lua version | Memory hook? | Instruction hook? | Action |
|------|-------------------|--------------|-------------------|--------|
| 2026-04-10 | v1.1.2 (go.mod pin) | No | No | No change. First quarterly check post-v1.0. |
| | | | | |
| | | | | |

Next check due: 2026-07-10

## Fork decision

Not yet triggered. The two-check threshold means a fork decision
is on the table at the 2026-10-10 check if upstream has still not
moved.
