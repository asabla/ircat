# Architecture

## Goals

1. **RFC-correct IRC server** — clients written against RFC 1459 / 2812 work unchanged.
2. **Federation-capable** — multiple ircat servers can link via RFC 2813 server-to-server protocol and behave as one network.
3. **Single binary, container-native** — one Go binary, one process, configured by one file.
4. **Operator-friendly** — built-in dashboard and admin API make day-to-day ops (who's online, ban a user, reload config) trivial.
5. **Extensible without forking** — Lua bots for in-network automation; Redis/webhook sinks for out-of-process consumers.
6. **Minimal dependencies** — Go standard library does the heavy lifting.

## Non-goals (v1)

- Clustering a single "virtual server" across multiple ircat processes behind a load balancer. Federation (distinct servers linked via S2S) is the scaling story.
- IRCv3 parity. Tracked as a v2 milestone; v1 may adopt specific extensions where they're essentially free (e.g., `message-tags`, `server-time`).
- TLS client certificate auth. TLS yes, client-cert auth later.
- Services (NickServ/ChanServ) as built-ins. The Lua bot runtime is the intended vehicle.

## Module overview

```
┌──────────────────────────────────────────────────────────┐
│                     cmd/ircat (main)                    │
│   config load → dependency wiring → graceful shutdown   │
└──────────────────────────────────────────────────────────┘
          │           │            │           │
          ▼           ▼            ▼           ▼
    ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐
    │ server  │ │ dashboard│ │   api    │ │   bots   │
    │ (IRC)   │ │ (htmx)   │ │ (admin)  │ │  (lua)   │
    └────┬────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘
         │           │            │           │
         └─────┬─────┴─────┬──────┴─────┬─────┘
               ▼           ▼            ▼
          ┌────────┐ ┌──────────┐ ┌──────────┐
          │ state  │ │  events  │ │ storage  │
          │ (hot)  │ │   bus    │ │ (sqlite/ │
          │        │ │          │ │ postgres)│
          └───┬────┘ └────┬─────┘ └──────────┘
              │           │
              ▼           ▼
        ┌────────────┐  ┌──────────┐
        │ federation │  │  sinks   │
        │   (S2S)    │  │ redis/wh │
        └────────────┘  └──────────┘
```

### `cmd/ircat`
Thin main: parses flags, loads config, builds dependencies, starts listeners, waits on `signal.NotifyContext(..., SIGINT, SIGTERM)`, runs graceful shutdown.

### `internal/config`
One `Config` struct. Two decoders (`encoding/json` + minimal YAML). `Validate()` is authoritative; decoders delegate. Live reload on `SIGHUP` for the subset that can be hot-reloaded (log level, MOTD, operator list, bot list). Listener ports require a restart.

### `internal/protocol`
- `Message` type: prefix, command, params, tags.
- Parser: byte-for-byte RFC-compliant, 512-byte hard limit (extendable if `message-tags` enabled).
- Writer: encodes outgoing messages; escapes trailing parameter correctly.
- `Command` dispatch table mapping verb → handler. Numeric replies live in `numeric.go` as constants.
- No I/O. Pure functions + types. Makes fuzzing trivial.

### `internal/server`
- TCP/TLS listener(s). One goroutine per connection.
- Per-connection state machine: `Unregistered → NickSet → UserSet → Registered → Quit`.
- Read loop feeds the parser; write loop drains a bounded outbound queue; a third loop handles ping timeout.
- Flood control: token bucket per connection.
- Backpressure: if the outbound queue is full, the connection is killed with `ERROR :SendQ exceeded` — matches ircd tradition.

### `internal/state`
In-memory, authoritative runtime state:
- `Users` keyed by numeric ID and by (case-mapped) nickname.
- `Channels` keyed by case-mapped name. Each holds membership, modes, bans, topic.
- Registered handlers are given a transactional handle (`state.Txn`) that batches modifications and emits events on commit. Keeps the hot path single-writer-ish per entity.
- Nickname case mapping: RFC 1459 (`[]\^` ↔ `{}|~`). Configurable to `ascii` if an operator insists.

### `internal/federation`
- Server links: outbound (we initiate) and inbound (peer initiates), both authenticated via PASS.
- State burst on link-up: servers, users, channels, modes, bans — in that order, per RFC 2813.
- Routing table: `nick → link`. Messages destined for a remote nick are forwarded along the spanning tree.
- Netsplit handling: on SQUIT, mark affected users/channels, re-converge on reconnect.
- TS (timestamp) reconciliation for channel/nick collisions — the older timestamp wins, following the widely-deployed TS6 conventions where they don't conflict with RFC 2813.

### `internal/storage`
```go
type Store interface {
    Operators() OperatorStore
    Bots() BotStore
    APITokens() TokenStore
    Channels() PersistentChannelStore // topics, modes, bans that survive restart
    Events() EventStore               // audit log
    Migrate(ctx context.Context) error
    Close() error
}
```
Two drivers: `sqlite` (default) and `postgres`. Each driver has its own SQL files in `internal/storage/<driver>/migrations/`. No ORM.

### `internal/events`
Single event bus (`chan Event` behind a fan-out). Consumers:
- Dashboard SSE log/chat streams.
- Redis Streams sink (if configured).
- Webhook sink (if configured).
- JSONL file sink (if configured).
- Audit log (always-on, to the DB).

Events are append-only and typed (`MessageEvent`, `JoinEvent`, `ModeEvent`, `AdminEvent`, etc.).

### `internal/dashboard`
- `net/http` + `html/template` + htmx 1.9 (shipped as a single static asset).
- Pages: login, overview, users, channels, bots, settings, logs, chat.
- Live updates via Server-Sent Events (`text/event-stream`). No WebSockets required — SSE keeps the dep surface small and proxies better.
- Log tail is an SSE stream off the logging package's ring buffer.
- Chat page is effectively an IRC client embedded in the dashboard, authenticated as a dashboard user (not an IRC user) that proxies into the server.

### `internal/api`
- JSON over HTTP under `/api/v1/`.
- Token auth via `Authorization: Bearer <token>`; tokens issued from the dashboard.
- Endpoints mirror dashboard actions: list/kick users, join/part bots to channels, reload config, rotate tokens, fetch audit log, send a raw IRC command as a privileged operator.
- Full reference: [`API.md`](API.md).

### `internal/bots`
- Gopher-Lua runtime per bot. Each bot is a goroutine with a bounded inbox.
- Sandbox: strip `io`, `os`, `debug`, `package.loadlib`. Provide `ctx` object with vetted functions.
- Persistent KV store per bot, backed by `storage.Store`.
- Hot reload: editing a bot via dashboard/API triggers a restart of just that bot.

### `internal/auth`
- Operator accounts (password + bcrypt or argon2id — pick one, justify in code).
- API tokens (random 32 bytes, stored hashed).
- TLS cert loading for the IRC listener and the dashboard listener (they may share or split).

### `internal/logging`
- `log/slog` JSON handler wired to stdout *and* an in-memory ring buffer.
- Ring buffer is the source for the dashboard log tail SSE.

## Dependencies

Go standard library covers: HTTP, TLS, templating, SQL, JSON, crypto, context, logging.

Necessary external dependencies (each must be justified):

| Dep | Purpose | Why no stdlib alternative |
|-----|---------|---------------------------|
| `modernc.org/sqlite` | SQLite driver | No stdlib SQLite. `modernc.org/sqlite` is pure Go (no CGo), which keeps cross-compile trivial. |
| `github.com/jackc/pgx/v5` | PostgreSQL driver | Stdlib has `database/sql` but needs a driver; pgx is the de-facto best. |
| `github.com/yuin/gopher-lua` | Lua runtime | No stdlib Lua. Gopher-Lua is pure Go, widely used, sandboxable. |
| YAML loader | Config | Stdlib has no YAML. See decision below. |

**YAML decision:** we will implement a *minimal* YAML 1.2 subset loader in-tree (`internal/config/yaml`) supporting only the constructs ircat needs: maps, sequences, scalars, anchors not required. If that proves fragile, we fall back to `sigs.k8s.io/yaml` (which itself shells out to `gopkg.in/yaml.v3`). The decision will be revisited after milestone M2 with a benchmark / bug-count check-in.

**Not allowed:** Gin/Echo/Fiber, GORM/ent/sqlc, logrus/zap, viper, cobra (we use `flag`). If you think you need one, open a discussion that updates this table.

## Concurrency model

- Each connection has a read goroutine and a write goroutine. The read goroutine parses a message and submits a `Command` job into a per-connection handler goroutine (or directly into the state txn).
- `state` is protected by a sharded lock keyed by channel name / user ID. Global reads (e.g., `/LIST`) take a snapshot.
- Federation links have their own I/O goroutines and a shared routing table guarded by an RWMutex.
- `events` fan-out is lock-free (one goroutine per subscriber pulling from a bounded channel; slow subscribers are dropped with a metric incremented).
- Shutdown is coordinated through a single `context.Context` cancelled in main.

## Security surface

- TLS on IRC listener (port 6697 by default).
- TLS on dashboard/API listener (configurable; can be fronted by a reverse proxy).
- Operator passwords hashed. API tokens hashed at rest.
- Rate limits on the dashboard login and API token endpoints.
- Federation links require matching PASS on both sides and a TLS-pinned fingerprint *or* a shared secret — configurable per link.
- The Lua sandbox is defense-in-depth, not a security boundary against hostile operators; only trusted users should be able to upload bots.

## Observability

- `slog` JSON logs to stdout.
- `/metrics` Prometheus endpoint on the dashboard listener (stdlib `expvar` first; switch to `prometheus/client_golang` only if we need histograms).
- `/healthz` and `/readyz` for container orchestration.
- Audit log in the database, exposed via dashboard and API.

## Deployment shapes

1. **Single-node SQLite** — the zero-config happy path. Dashboard and IRC on the same host. File-backed DB.
2. **Single-node Postgres** — same binary, Postgres DSN in config.
3. **Federated mesh** — N ircat nodes, each with its own DB (SQLite or Postgres), linked via S2S.

There is no "shared DB, multiple ircat replicas" mode. State is in-memory and authoritative per node; scale via federation.
