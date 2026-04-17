# Soak Test Findings

Running log of nightly and ad-hoc soak test results for ircat.
The nightly job is defined in `.github/workflows/soak.yml` and
targets `main`. On a GitHub-hosted runner it spins up a single
ircat instance with 500 concurrent connections across 10 channels
for one hour; a beefier self-hosted runner can use the
`workflow_dispatch` inputs to drive the v1.1 reference shape
(5 000 conns × 500 channels / 1h).

A separate mesh soak exercises three-node federation under the
same traffic profile. Both harnesses live under `tests/soak/`.

Record every run that surfaces something interesting — regressions,
new baselines after optimisation work, or confirmation that a fix
holds. Boring green runs do not need a row, but the first clean
run after a fix should be noted.

## Running the soak manually

```bash
# Single-node soak (CI shape: 500 conns, 10 channels, 1h)
go run ./tests/soak/main.go \
  -addr localhost:6667 \
  -conns 500 \
  -channels 10 \
  -duration 1h

# Reference shape (requires a beefier host than the GH runner)
go run ./tests/soak/main.go \
  -addr localhost:6667 \
  -conns 5000 \
  -channels 500 \
  -duration 1h

# Mesh soak (three-node federation)
go run ./tests/soak/main.go \
  -mesh \
  -addrs localhost:6667,localhost:6668,localhost:6669 \
  -conns 500 \
  -channels 10 \
  -duration 1h
```

The harness reports: elapsed time, sent count, received count,
drops, and rate (msgs/sec). A drop rate above `-max-drop-rate`
(default 0.01, CI uses 0.001) is a failure.

## Single-node findings

| Date | Version / Commit | Config | Result | Rate (msgs/sec) | Drop Rate | Notes |
|------|------------------|--------|--------|-----------------|-----------|-------|
|      |                  |        |        |                 |           |       |

Config shorthand: `5K/500/1h` = 5 000 connections, 500 channels,
1 hour duration. Note deviations from the default in the Config
column.

## Mesh soak findings

Three-node federation mesh. Each node receives an equal share of
connections; channels span all three nodes.

| Date | Version / Commit | Config | Result | Rate (msgs/sec) | Drop Rate | Cross-node Latency (p99) | Notes |
|------|------------------|--------|--------|-----------------|-----------|--------------------------|-------|
|      |                  |        |        |                 |           |                          |       |

## Known issues and patterns

Document recurring observations, confirmed root causes, and
workarounds here. Link to the commit or PR that resolves each
item.

- **JOIN-storm sendq overflow (2026-04)**: The first iteration of
  the nightly soak had never produced a green run. The harness
  issued the full JOIN burst before starting reader goroutines, so
  server-side broadcast fan-out piled into a socket no one was
  reading, the kernel receive buffer filled, `writeLoop`
  back-pressured, and the 64-slot per-conn sendq overflowed.
  Clients got killed and the very next JOIN write returned
  `connection reset by peer`. Fixed by (a) starting a drainer per
  client *before* the JOIN burst, (b) pacing each client's JOINs
  to 200/sec, (c) bumping `outboundQueue` to 1024, and
  (d) batching writes through a bufio.Writer so a fan-out no
  longer costs one syscall per recipient. CI defaults were also
  reduced from 5 000/500 to 500/10 — the reference shape still
  exists as a `workflow_dispatch` override for self-hosted runs.
