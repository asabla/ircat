# ircat

A modern IRC server written in Go, distributed as a single binary,
with a built-in htmx dashboard, a sandboxed Lua bot runtime, an
admin HTTP API, and first-class RFC 2813 federation. The full
RFC 1459 / 2810 / 2811 / 2812 / 2813 family is implemented, with
the IRCv3 capabilities modern clients expect negotiated on top.

<p align="center">
  <img src="docs/images/overview.png" alt="ircat dashboard — overview" width="860">
</p>

## Why ircat

- **Full RFC compliance.** Every command, mode, prefix, and
  numeric the RFC family defines is implemented and tested — all
  four channel-name prefixes (`#`, `&`, `+`, `!`), every channel
  mode (`+i +m +n +p +s +t +k +l +o +v +b +e +I +q +a +O +r`),
  all six user modes, the complete set of RFC 2812 commands, and
  the full RFC 2813 server-to-server handshake.
- **Modern client support.** `message-tags`, `server-time`,
  `echo-message`, `multi-prefix`, `userhost-in-names`,
  `away-notify`, `invite-notify`, `account-tag`, `extended-join`,
  `batch`, and `chghost` — all negotiated through standard
  `CAP LS` / `CAP REQ`.
- **Services built in.** NickServ (REGISTER, IDENTIFY, GHOST,
  DROP, INFO, GROUP, RELEASE), ChanServ (registration, auto-op,
  topic lock, KEEPTOPIC), and MemoServ with offline delivery and
  configurable retention — all in-process, no separate services
  package to run.
- **Federation is first-class.** RFC 2813 links over TLS with
  fingerprint pinning, SVINFO version negotiation, TS-based nick
  collision resolution, and runtime channel-mode propagation.
- **Built-in operator dashboard.** htmx + server-sent events,
  zero npm / webpack / React. Live log and event streams,
  operator and token management, channel and user admin, a
  federation panel, and an embedded IRC client that rides an
  internal virtual connection (works even if the IRC listener is
  firewalled from your browser).
- **Admin HTTP/JSON API.** Token-authenticated, scriptable,
  suitable for CI automation and external tooling.
- **Pluggable storage.** SQLite by default (zero-config) or
  PostgreSQL, selected from config. Channel state, bans,
  exceptions, invexes, and quiets all round-trip across
  restart.
- **Lua bots.** Sandboxed runtime with an event API covering
  messages, joins, commands, and timers. Scripts hot-reload from
  the dashboard.
- **Event export.** JSONL sinks and webhooks ship in the box;
  additional transports drop in as sinks.
- **Config parity.** JSON or YAML, same schema. Pick the one
  your tooling already speaks.
- **Container-first.** Devcontainer for development, multi-stage
  distroless production image, production and development
  Compose stacks.
- **Go standard library first.** `net/http` + `html/template` +
  htmx for the UI, `database/sql` + hand-written SQL for
  persistence, `log/slog` for structured logging. External
  dependencies have to justify themselves.

## Status

**Current release: v1.1.0.** The RFC-complete v1.0.0 base shipped
with the first four IRCv3 capabilities; v1.1.0 adds
`userhost-in-names`, `away-notify`, and `invite-notify`. The
post-1.1 line has added services (NickServ / ChanServ / MemoServ),
the remaining IRCv3 capabilities (`account-tag`, `extended-join`,
`batch`, `chghost`), and a series of dashboard and operations
improvements. See [`docs/PLAN.md`](docs/PLAN.md) for what is next
and [`docs/PROTOCOL.md`](docs/PROTOCOL.md) for the wire-level
reference.

## Five-minute start

The complete walkthrough lives in
[`docs/QUICKSTART.md`](docs/QUICKSTART.md). The short version is
three commands:

```sh
cp .env.example .env
$EDITOR .env                       # set IRCAT_INITIAL_ADMIN_PASSWORD
docker compose up -d
```

Then open `http://localhost:9696/dashboard` and sign in as
`admin` with the password you just set.

<p align="center">
  <img src="docs/images/signin.png" alt="ircat dashboard — sign-in" width="560">
</p>

Point any IRC client at port `6667` once the stack is up:

```
/server localhost 6667
/nick mynick
/join #lobby
```

### If the admin bootstrap was skipped

The container ships an `ircat operator` subcommand that writes
directly to the configured store, so you can recover without
restarting. Mint an admin from the host:

```sh
docker compose exec ircat sh -c \
  'echo hunter2 | /app/ircat operator add admin --config /etc/ircat/config.yaml --flags all'
```

Passwords are hashed with argon2id before they touch the
database; the plaintext never lives in the store, the audit log,
or the process logs. See
[`docs/QUICKSTART.md`](docs/QUICKSTART.md#what-if-the-admin-bootstrap-is-silent)
for every recovery path.

### Running from source

No Docker, no containers — just the binary:

```sh
go build -o ./ircat ./cmd/ircat
./ircat server --config ./config/dev.yaml
```

The single-binary path and a copy-pasteable dev config live in
[`docs/QUICKSTART.md`](docs/QUICKSTART.md#single-binary-no-docker).

### Hacking on ircat

For a hot-reload dev loop (air + Docker, devcontainer friendly):

```sh
docker compose -f docker-compose.dev.yml up
```

## Dashboard tour

The dashboard is server-rendered HTML plus htmx and SSE — no
front-end build step, no JavaScript bundle to ship. A walk
through the pages:

### Overview

Live metric cards, sparklines, federation link health, and a
tail of the most recent audit events.

<p align="center">
  <img src="docs/images/overview.png" alt="Overview page" width="860">
</p>

### Channels and users

Browse every channel, inspect topics and modes, kick or ban from
the UI, and drill into individual users to see their session,
channels, and operator flags.

<p align="center">
  <img src="docs/images/channels.png" alt="Channels page" width="860">
</p>

### Embedded chat client

A full IRC client in the browser that rides an internal virtual
connection — useful for smoke-testing a fresh deployment and as
an operator-only back channel when the public listener is
firewalled.

<p align="center">
  <img src="docs/images/chat.png" alt="Embedded chat client" width="860">
</p>

### Federation panel

Every linked server with its TLS fingerprint, SVINFO version,
per-link byte counters, and a link / unlink button that speaks
RFC 2813 underneath.

<p align="center">
  <img src="docs/images/federation.png" alt="Federation panel" width="860">
</p>

### Live logs and events

`tail -f` in the browser, backed by server-sent events. Filter
by level, component, or free-text search.

<p align="center">
  <img src="docs/images/logs.png" alt="Live log stream" width="860">
</p>

The full surface — operators, API tokens, bot management, event
sinks, and settings — is documented in
[`docs/DASHBOARD.md`](docs/DASHBOARD.md).

## Documentation

| Topic | File |
|---|---|
| Quickstart (5-minute walkthrough) | [`docs/QUICKSTART.md`](docs/QUICKSTART.md) |
| Architecture | [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) |
| Configuration schema | [`docs/CONFIG.md`](docs/CONFIG.md) |
| IRC protocol notes | [`docs/PROTOCOL.md`](docs/PROTOCOL.md) |
| Federation | [`docs/FEDERATION.md`](docs/FEDERATION.md) |
| Services (NickServ, ChanServ, MemoServ) | [`docs/SERVICES.md`](docs/SERVICES.md) |
| Dashboard | [`docs/DASHBOARD.md`](docs/DASHBOARD.md) |
| Admin API | [`docs/API.md`](docs/API.md) |
| Lua bots | [`docs/BOTS.md`](docs/BOTS.md) |
| Event export (webhooks, JSONL) | [`docs/EVENTS.md`](docs/EVENTS.md) |
| Operations (day-2, metrics, backup, soak) | [`docs/OPERATIONS.md`](docs/OPERATIONS.md) |
| Security (trust boundaries, Lua sandbox) | [`docs/SECURITY.md`](docs/SECURITY.md) |
| Testing strategy | [`docs/TESTING.md`](docs/TESTING.md) |
| Contributing and commit convention | [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) |
| Roadmap (post-v1.0) | [`docs/PLAN.md`](docs/PLAN.md) |
| Historical plans | [`docs/PLAN-v0.1.md`](docs/PLAN-v0.1.md) · [`docs/PLAN-v0.2.md`](docs/PLAN-v0.2.md) · [`docs/PLAN-v0.3.md`](docs/PLAN-v0.3.md) |
| Upgrade notes | [`docs/UPGRADE-v0.1-to-v0.2.md`](docs/UPGRADE-v0.1-to-v0.2.md) · [`docs/UPGRADE-v0.2-to-v0.3.md`](docs/UPGRADE-v0.2-to-v0.3.md) |

## Contributing

Bug reports, RFC nits, and pull requests are welcome. Read
[`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) first — it covers
the coding conventions, the test matrix, and the Conventional
Commits rules the history follows.

Before sending a patch:

```sh
go test ./...
go vet ./...
gofmt -l . && test -z "$(gofmt -l .)"
```

## License

[MIT](LICENSE) © 2026 asabla.
