# ircat

A modern, portable IRC server written in Go with a built-in htmx dashboard, Lua bots, and an admin API. Aims for full RFC 1459 / RFC 2810–2813 compliance including server-to-server federation, while staying pleasant to run in 2026.

## Highlights

- **Full IRC protocol** — RFC 1459 core + RFC 2810/2811/2812/2813 federation. IRCv3 extensions on the roadmap.
- **Single binary** — Go stdlib first; minimal external dependencies.
- **Built-in dashboard** — htmx + server-sent events, live log streaming, chat, settings, user/channel admin.
- **Pluggable storage** — SQLite (default, zero-config) or PostgreSQL, selected via config.
- **Admin API** — HTTP/JSON, token-authenticated, usable for automation and external tooling.
- **Lua bots** — sandboxed bot runtime with an event API (messages, joins, commands).
- **Event export** — webhooks and raw JSONL sinks built in; additional transports can land as drop-in sinks later.
- **Config flexibility** — JSON *or* YAML, same schema.
- **Container-first** — devcontainer for development, multi-stage Dockerfile, dev and production Compose stacks.

## Status

v1.2.0 tagged. See [`docs/PLAN.md`](docs/PLAN.md) for what's
next, [`docs/PLAN-v1.2.md`](docs/PLAN-v1.2.md) for the v1.2
historical record, and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
for the design.

## Quick start

The full first-boot walkthrough lives in
[`docs/QUICKSTART.md`](docs/QUICKSTART.md). The TL;DR:

```sh
cp .env.example .env
$EDITOR .env                       # set IRCAT_INITIAL_ADMIN_PASSWORD
docker compose up -d
# open http://localhost:9696/dashboard, sign in as admin
```

If you forgot the password or never set it, recover via the
`ircat operator` subcommand without restarting:

```sh
docker compose exec ircat sh -c \
  'echo hunter2 | /app/ircat operator add admin --config /etc/ircat/config.yaml --flags all'
```

For dev work without Docker:

```sh
docker compose -f docker-compose.dev.yml up
```

## Documentation

- [Quickstart](docs/QUICKSTART.md) — five-minute first-boot walkthrough
- [Architecture](docs/ARCHITECTURE.md)
- [Implementation plan](docs/PLAN.md) (v1.3 forward plan)
- [Operations](docs/OPERATIONS.md) — day-2 surface, metrics, backup, soak
- [Security](docs/SECURITY.md) — trust boundaries, Lua sandbox audit
- [Contributing & commit convention](docs/CONTRIBUTING.md)
- [IRC protocol notes](docs/PROTOCOL.md)
- [Federation](docs/FEDERATION.md)
- [Configuration](docs/CONFIG.md)
- [Admin API](docs/API.md)
- [Dashboard](docs/DASHBOARD.md)
- [Lua bots](docs/BOTS.md)
- [Event export](docs/EVENTS.md)
- [Testing strategy](docs/TESTING.md)
- [Upgrade v1.0 → v1.1](docs/UPGRADE-v1.0-to-v1.1.md)
- [Upgrade v1.1 → v1.2](docs/UPGRADE-v1.1-to-v1.2.md)

## License

TBD.
