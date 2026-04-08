# OPERATIONS.md — running ircat in production

This document covers the day-2 tasks: monitoring, backup, restore,
upgrades, and incident response.

## Endpoints to scrape and probe

| Endpoint | Purpose | Auth |
|----------|---------|------|
| `GET /healthz` | Liveness — process is alive. | none |
| `GET /readyz`  | Readiness — IRC listener is bound. | none |
| `GET /metrics` | Prometheus text format. | none |
| `GET /api/v1/*` | Admin REST API. | bearer token |

The `/metrics` endpoint exposes:

```
ircat_users                  gauge   registered local users
ircat_channels               gauge   tracked channels
ircat_federation_links       gauge   active federation links
ircat_bots                   gauge   registered Lua bots
ircat_messages_in_total      counter inbound IRC messages parsed
ircat_messages_out_total     counter outbound IRC messages written
ircat_uptime_seconds         gauge   process uptime
```

`/healthz`, `/readyz`, and `/metrics` are unauthenticated by design.
Front the dashboard listener with a reverse proxy and IP-allowlist
`/metrics` to your Prometheus scraper.

## Backup

ircat keeps two pieces of state:

1. **The persistence DB** (SQLite or Postgres) — operators, API tokens,
   persistent channel topics/modes, audit log.
2. **The event log** (`events.jsonl` rotation, optional) — append-only
   audit trail.

Both are written to volumes so a container restart preserves them.

### SQLite

The default SQLite path is `/var/lib/ircat/ircat.db`. Two ways to back
it up safely:

```sh
# Option 1: stop ircat, copy the file, start again. Simplest, has
# downtime, fine for nightly cron.
docker compose stop ircat
cp /var/lib/docker/volumes/ircat_ircat-data/_data/ircat.db /backup/ircat-$(date +%F).db
docker compose start ircat

# Option 2: use the SQLite .backup command online, no downtime.
docker compose exec ircat sqlite3 /var/lib/ircat/ircat.db ".backup '/var/lib/ircat/ircat.db.bak'"
docker compose cp ircat:/var/lib/ircat/ircat.db.bak /backup/ircat-$(date +%F).db
docker compose exec ircat rm /var/lib/ircat/ircat.db.bak
```

For continuous replication consider [Litestream](https://litestream.io)
pointed at S3 / GCS. Litestream streams the WAL out so RPO is sub-second.

### Postgres

```sh
# Hot logical dump.
docker compose exec postgres pg_dump -U ircat -Fc ircat > /backup/ircat-$(date +%F).dump

# Restore into a fresh database.
docker compose exec -T postgres pg_restore -U ircat -d ircat --clean --if-exists < /backup/ircat-2026-04-07.dump
```

For continuous replication, use Postgres' WAL archiving or a managed
service that handles point-in-time recovery for you.

### Event log

`events.jsonl` is append-only and rotates by size (`rotate_mb` /
`keep` in `events.sinks[].path`). Treat the log directory as a normal
log volume — `tar` it nightly, ship to your archive of choice.

## Restore

1. Stop ircat: `docker compose stop ircat`.
2. Replace the DB file (SQLite) or restore the dump (Postgres).
3. Make sure the file is owned by the container user (`nonroot`,
   uid/gid 65532 in the distroless image).
4. Start ircat: `docker compose start ircat`.
5. Watch `docker compose logs -f ircat`. The startup path runs the
   storage migrations (`internal/storage/<driver>/schema.sql`) and
   restores any persistent channels via `restorePersistentChannels`.
6. Verify `GET /readyz` returns 200 before sending traffic.

## Upgrades

ircat is a single-binary deployment. The recommended flow:

1. Pin a tag in `.env` (`IRCAT_VERSION=v0.x.y`).
2. `docker compose pull ircat && docker compose up -d ircat`.
3. Verify `/readyz` and the metrics counters resume incrementing.

For federation meshes, upgrade nodes one at a time. The reconnect
loop in the dial supervisor handles the brief downtime — no manual
SQUIT needed.

## Incident response

| Symptom | Likely cause | What to check |
|---------|--------------|---------------|
| `/readyz` returns 503 | listener never bound | `docker compose logs ircat` for "bind" lines |
| Spike in `ircat_messages_in_total` with no client growth | flood / abuse | `events.jsonl` for `flood_violation` audit lines |
| `ircat_federation_links` drops to 0 | peer down or auth mismatch | dial logs, peer's listener logs |
| `ircat_users` stuck at 0 after restart | DB inaccessible / migration failed | postgres health, sqlite WAL permissions |

## Measured envelope

Numbers below come from the v1.1 benchmark suites and were
collected on an Intel Xeon E-2286M (8C/16T, 2.4 GHz) under Linux
6.8 with `-race` disabled. Re-run on your own hardware before
relying on absolute values.

### Flood control

Source: `internal/server/floodcontrol_bench_test.go`. Rerun with
`go test -bench=BenchmarkTokenBucket ./internal/server/`.

| Scenario | ns/op | Notes |
|---|---|---|
| Single sender, uncontended | 55 | The floor cost of a `Take()` |
| 1 sender on a shared bucket | 56 | mutex acquire dominates |
| 10 senders on a shared bucket | 98 | low cache contention |
| 100 senders on a shared bucket | 146 | |
| 1000 senders on a shared bucket | 197 | mutex still scales |
| 1 connection, own bucket | 55 | |
| 10 connections, own bucket | 6.7 | parallel scaling kicks in |
| 100 connections, own bucket | 4.7 | |
| 1000 connections, own bucket | 4.3 | per-connection model is the right one |

The single-bucket-shared-by-N-senders column is a worst-case
bound — production uses one bucket per connection so the
"per-connection" column is what an operator actually sees. At
4.3 ns/op the rate limiter is firmly out of the hot path even at
1000 concurrent connections.

The default `message_burst: 100` and `message_refill_per_second:
10` ship in `default-config.yaml` and are deliberately
conservative for a brand-new ircat install. Operators with
large heavily-active channels should raise both numbers — the
benchmark says there is no cost reason not to.

### Storage audit-event throughput

Source: `internal/storage/sqlite/events_bench_test.go` and the
matching file under `internal/storage/postgres/`. Rerun with
`go test -bench=BenchmarkEvents ./internal/storage/sqlite/` (the
Postgres bench only runs when `IRCAT_TEST_POSTGRES_DSN` is set).

| Backend | Serial Append | Parallel Append | Notes |
|---|---|---|---|
| SQLite (WAL + synchronous=NORMAL) | 183 µs | 73 µs | default in v1.1 |
| SQLite (WAL + synchronous=FULL) | 8.4 ms | 9.4 ms | per-commit fsync |

The default v1.1 SQLite DSN uses `synchronous=NORMAL`, which is
the standard SQLite production pairing for WAL mode. It fsyncs
the WAL on checkpoint boundaries rather than every commit,
trading a tiny window of "lost writes on power loss" between
checkpoints for a 46x improvement on serial Appends. The audit
log is also pushed through the jsonl + webhook sinks at publish
time, so the persistent store is not the only durability path.

Postgres numbers depend heavily on the host kernel + storage
class — run the benchmark against your own database to get a
local figure. The benchmark Skip's cleanly when
`IRCAT_TEST_POSTGRES_DSN` is unset.

### Soak harness

`tests/soak/` is a small Go binary that opens N concurrent IRC
connections, joins each to M channels, and runs a sustained
PRIVMSG load against a real ircat instance for the configured
duration. Use it for quick load smoke tests during development,
the nightly CI cron, and the manual reference soak.

```sh
# Smoke test (≈5 seconds, no harm to run on a dev box):
go run ./tests/soak \
  -addr 127.0.0.1:6667 \
  -conns 100 \
  -channels 10 \
  -msgs-per-sec 500 \
  -duration 5s
```

#### Nightly CI run (automatic)

`.github/workflows/soak.yml` runs the harness every day at
03:00 UTC against a fresh ircat instance:

| Setting | Value |
|---|---|
| Connections | 5 000 |
| Channels | 500 |
| Aggregate rate | 500 msgs/sec |
| Duration | 1 hour |
| Max drop rate | 0.1 % |

Targets are deliberately smaller than the reference soak below
because GitHub-hosted runners have 4 CPU and 16 GB RAM, not
dedicated hardware. The CI job exists to catch broadcast
hot-path regressions between releases; the **reference soak**
is the bigger periodic exercise.

Manual trigger via workflow_dispatch:
```sh
gh workflow run soak.yml -f conns=10000 -f channels=1000 \
  -f msgs_per_sec=5000 -f duration=4h
```

Logs and the soak summary are uploaded as workflow artefacts
on every run, including failures.

#### Reference soak (manual, real hardware)

The v1.1 plan called for a 24-hour reference soak at 10k
concurrent connections, 1k channels, 24h on the reference
Hetzner box. It's an operator drill rather than a CI job — the
runner would need at least 4 CPU and 8 GB RAM dedicated to
the harness alone, plus another 4 CPU and 4 GB for ircat.

Recommended invocation, with the host pre-tuned (`ulimit -n
65536`, raised TCP keepalives, no swap pressure):

```sh
go run ./tests/soak \
  -addr 127.0.0.1:6667 \
  -conns 10000 \
  -channels 1000 \
  -msgs-per-sec 5000 \
  -duration 24h \
  -warmup 5m \
  -max-drop-rate 0.0005
```

End-of-run sanity checks the drill commits to:

| Property | Target |
|---|---|
| Drop rate | < 0.05 % |
| RSS at 24h vs RSS at 1h baseline | within 25 % |
| `ircat_messages_in_total` rate | stable to within 5 % over the run |
| Audit log size | linear in event count, no exponential blowup |

Capture the per-process RSS via the dashboard `/metrics`
endpoint or `top -bn1 -p $(pidof ircat)`; the harness itself
does not read RSS because it runs in a separate process.

The harness reports `sent / received / drops / rate` and exits
non-zero when the drop rate exceeds `-max-drop-rate` (default
1 %).

#### Postgres backend benchmark on real hardware

`internal/storage/postgres/events_bench_test.go` hosts the
Postgres equivalent of the SQLite audit-write benchmark. It
Skips cleanly when `IRCAT_TEST_POSTGRES_DSN` is unset, which is
why the v1.1 measured envelope only quotes SQLite. Real
numbers need an actual Postgres on tuned hardware:

```sh
# Against a local container:
docker run --rm -d --name pgbench \
  -e POSTGRES_PASSWORD=ircat -e POSTGRES_DB=ircat -e POSTGRES_USER=ircat \
  -p 5432:5432 postgres:16-alpine

IRCAT_TEST_POSTGRES_DSN='postgres://ircat:ircat@127.0.0.1:5432/ircat?sslmode=disable' \
  go test -bench=BenchmarkEvents -benchtime=2s ./internal/storage/postgres/

docker rm -f pgbench
```

For RDS-class numbers, point the DSN at a tuned managed
instance and let the benchmark run for 30 seconds per case.
Document the result alongside the SQLite numbers at the top of
this section.

## Disaster recovery exercise

Run this drill quarterly. The exit criterion is "the new node serves
existing operator logins, channels, and audit history".

1. Take a fresh backup.
2. Start a new compose stack on a clean host.
3. Restore the backup into the fresh stack.
4. Connect with `irssi` or another client using a known operator
   account. Verify channel topic and mode persistence.
5. Hit `/api/v1/operators` with the existing bearer token. Verify
   the token still validates against the restored hash.
6. Tear the recovery stack down.
