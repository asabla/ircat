# ircat

A modern, portable IRC server written in Go with a built-in htmx dashboard, Lua bots, and an admin API. Implements the full IRC RFC family (1459 / 2810 / 2811 / 2812 / 2813) plus the most-used IRCv3 capabilities, while staying pleasant to run in 2026.

## Highlights

- **100% RFC compliance** — every command, mode, prefix, and numeric the RFC family defines is implemented and tested. All four channel-name prefixes (`#`, `&`, `+`, `!`), every channel mode (`+i +m +n +p +s +t +k +l +o +v +b +e +I +q +a +O +r`), all six user modes, all 46 RFC 2812 commands, and the full RFC 2813 server-to-server handshake (PASS → SERVER → SVINFO → burst).
- **IRCv3 modern client support** — `message-tags`, `server-time`, `echo-message`, `multi-prefix` negotiated via standard `CAP REQ`.
- **Single binary** — Go stdlib first; minimal external dependencies.
- **Built-in dashboard** — htmx + server-sent events, live log streaming, channel/user/operator admin, federation panel.
- **Pluggable storage** — SQLite (default, zero-config) or PostgreSQL, selected via config. Channel state, bans, exceptions, invexes, and quiets all round-trip across restart.
- **Admin API** — HTTP/JSON, token-authenticated, usable for automation and external tooling.
- **Lua bots** — sandboxed bot runtime with an event API (messages, joins, commands).
- **Event export** — webhooks and raw JSONL sinks built in; additional transports can land as drop-in sinks later.
- **Federation** — full RFC 2813 server-to-server with TLS, fingerprint pinning, SVINFO version negotiation, TS-based nick collision resolution, and runtime channel-mode propagation.
- **Config flexibility** — JSON *or* YAML, same schema.
- **Container-first** — devcontainer for development, multi-stage Dockerfile, dev and production Compose stacks.

## Status

**v1.0.0 tagged** — the RFC-complete release. See the v1.0.0 tag
annotation for the full catalog of what's implemented and
[`docs/PROTOCOL.md`](docs/PROTOCOL.md) for the wire-level reference.

The pre-1.0 development arc shipped under the v0.x tags. For
what's next see [`docs/PLAN.md`](docs/PLAN.md). For historical
context see [`docs/PLAN-v0.1.md`](docs/PLAN-v0.1.md),
[`docs/PLAN-v0.2.md`](docs/PLAN-v0.2.md), and
[`docs/PLAN-v0.3.md`](docs/PLAN-v0.3.md). For the design see
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

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
- [Implementation plan](docs/PLAN.md) (post-v1.0 forward plan)
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
- [Pre-1.0 plans](docs/PLAN-v0.1.md), [v0.2](docs/PLAN-v0.2.md), [v0.3](docs/PLAN-v0.3.md)
- [Upgrade v0.1 → v0.2](docs/UPGRADE-v0.1-to-v0.2.md)
- [Upgrade v0.2 → v0.3](docs/UPGRADE-v0.2-to-v0.3.md)

## License

TBD.
