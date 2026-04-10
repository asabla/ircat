# Soak Test Findings

Running log of nightly and ad-hoc soak test results for ircat.
The nightly job is defined in `.github/workflows/soak.yml` and
targets `main`. It spins up a single ircat instance with 5 000
concurrent connections across 500 channels for one hour.

A separate mesh soak exercises three-node federation under the
same traffic profile. Both harnesses live under `tests/soak/`.

Record every run that surfaces something interesting — regressions,
new baselines after optimisation work, or confirmation that a fix
holds. Boring green runs do not need a row, but the first clean
run after a fix should be noted.

## Running the soak manually

```bash
# Single-node soak (defaults: 5000 conns, 500 channels, 1h)
go run ./tests/soak/main.go \
  -addr localhost:6667 \
  -conns 5000 \
  -channels 500 \
  -duration 1h

# Mesh soak (three-node federation)
go run ./tests/soak/main.go \
  -mesh \
  -addrs localhost:6667,localhost:6668,localhost:6669 \
  -conns 5000 \
  -channels 500 \
  -duration 1h
```

The harness reports: elapsed time, sent count, received count,
drops, and rate (msgs/sec). A non-zero drop count at the default
config is a failure.

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

- *(none yet)*
