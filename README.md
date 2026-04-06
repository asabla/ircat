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

Pre-alpha. See [`docs/PLAN.md`](docs/PLAN.md) for the implementation roadmap and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design.

## Quick start (planned)

```sh
# dev (hot reload, sqlite, seeded admin)
docker compose -f docker-compose.dev.yml up

# production profile
docker compose -f docker-compose.yml up -d
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Implementation plan](docs/PLAN.md)
- [Contributing & commit convention](docs/CONTRIBUTING.md)
- [IRC protocol notes](docs/PROTOCOL.md)
- [Federation](docs/FEDERATION.md)
- [Configuration](docs/CONFIG.md)
- [Admin API](docs/API.md)
- [Dashboard](docs/DASHBOARD.md)
- [Lua bots](docs/BOTS.md)
- [Event export](docs/EVENTS.md)
- [Testing strategy](docs/TESTING.md)

## License

TBD.
