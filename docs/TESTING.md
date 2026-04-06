# Testing Strategy

ircat's correctness depends on three things: the protocol parser, the state machine, and the federation routing. Tests are organized to attack those three directly, plus everything else.

## Layers

### 1. Unit tests (colocated `*_test.go`)

- Live next to the code they test.
- Table-driven where it makes sense.
- Fast — the entire unit suite must run in under 10 seconds on a laptop.
- No network, no disk (use `t.TempDir` when disk is unavoidable).

Target areas:
- `internal/protocol` — message parsing, encoding, numeric emission. Includes Go fuzz targets.
- `internal/state` — joins, parts, mode transitions, nick changes, case mapping.
- `internal/config` — JSON + YAML decoders, validation errors.
- `internal/storage` — each driver against its own schema. Use the real SQLite; use a Postgres container for the Postgres driver (see integration tests).
- `internal/bots` — sandbox escape attempts, budget enforcement.

### 2. Integration tests (`internal/.../*_integration_test.go`, build tag `integration`)

- May hit real databases and real filesystems.
- `go test -tags=integration ./...` in CI.
- Postgres tests start a container via `testcontainers-go` — the *one* external dep we allow for tests only, gated by build tag so production builds never pull it.

### 3. End-to-end tests (`tests/e2e/`)

Black-box tests that start a real ircat process and drive it over TCP + HTTP.

- Start the server with a temp config and temp DB.
- Spawn real TCP clients using a tiny in-tree IRC client helper (`tests/e2e/ircclient`).
- Assert on observed server output (numerics, channel state, dashboard API responses).
- One scenario per file; scenarios are independent.

Coverage targets:
- Registration happy path.
- Every command from M2 with at least one success and one error case.
- OPER auth.
- Dashboard login → kick via API → verify user is gone.
- Bot create → trigger → assert reply.
- Event sink: jsonl file contains expected events after a scripted session.
- Federation: two-node compose stack, cross-server message delivery, split, recover.

### 4. Fuzz

- `FuzzMessageParser` — feeds random bytes into the parser; must never panic, must produce either a valid parse or a clean error.
- `FuzzConfigLoader` — same for JSON/YAML loader.
- Run in CI nightly for 5 minutes each.

### 5. Benchmarks

- `BenchmarkParse` — sustained parse throughput on representative messages.
- `BenchmarkBroadcast` — fanning a PRIVMSG to N channel members.
- `BenchmarkFederationRoute` — lookup + forward in the routing table.

Benchmarks are not gated in CI but are tracked across releases in `docs/perf/`.

## Test data

- `tests/fixtures/` holds capture files from real IRC sessions that the parser replays against expected output.
- `tests/e2e/configs/` holds minimal configs per scenario.
- No committed binaries.

## CI matrix

| Job | Runs |
|-----|------|
| unit | every PR, every platform we support |
| vet + staticcheck + gofmt | every PR |
| integration (sqlite) | every PR |
| integration (postgres) | every PR |
| e2e | every PR |
| e2e federation | every PR |
| fuzz (5 min) | nightly |
| benchmarks | nightly, report-only |

## Flake policy

A flaky test is a bug. Fix it or delete it. Do not retry tests to mask flakiness. Timeouts in e2e tests should be generous (5s default) but deterministic — no "sleep 100ms and hope".

## Coverage

Track it, don't worship it. The goal is useful tests, not a number. If coverage drops significantly on a PR, the reviewer should ask why.
