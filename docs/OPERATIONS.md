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
