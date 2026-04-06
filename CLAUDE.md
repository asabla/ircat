# CLAUDE.md — guidance for Claude Code working on ircat

This file gives Claude Code (and compatible agents) the context needed to contribute to this repository without having to re-derive it every session.

## What this project is

`ircat` is a Go implementation of an IRC server targeting full RFC 1459 + RFC 2810/2811/2812/2813 compliance, with a built-in htmx dashboard, Lua bot runtime, admin HTTP API, and pluggable SQLite/PostgreSQL storage. It is distributed as a single binary and run primarily from containers.

Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/PLAN.md`](docs/PLAN.md) first — they are the source of truth for design and roadmap.

## Non-negotiable principles

1. **Standard library first.** Every external dependency must justify itself. Acceptable deps are listed in `docs/ARCHITECTURE.md#dependencies`. If you want to add one, update that doc in the same PR and explain the trade-off.
2. **Protocol correctness over cleverness.** When in doubt, match RFC 1459/2812 behaviour. Cite the RFC section in code comments where the behaviour is non-obvious.
3. **Single binary, single process.** The dashboard, admin API, IRC listener, and bot runtime all live in the same Go process. Do not split into microservices.
4. **Config parity.** Anything expressible in JSON config must also be expressible in YAML and vice-versa. Use a single Go struct + two decoders.
5. **Storage abstraction.** All persistence goes through the `storage.Store` interface (see `internal/storage`). Never reach into `database/sql` from handler code.
6. **Federation is first-class.** Do not add a feature that cannot be propagated across a server mesh. If it's genuinely local-only, document why.

## Repository layout (target)

```
cmd/ircat/           # main entrypoint
internal/
  server/            # TCP listener, connection lifecycle
  protocol/          # message parser, command dispatch, numeric replies
  state/             # users, channels, modes, nick registry
  federation/        # server-to-server link mgmt, state sync, routing
  storage/           # Store interface + sqlite/postgres drivers
  config/            # JSON+YAML loader, schema, validation
  dashboard/         # htmx UI, SSE streams, templates, static assets
  api/               # admin HTTP/JSON API
  bots/              # Lua runtime, sandboxing, event dispatch
  events/            # event bus, redis/webhook/jsonl sinks
  auth/              # operator accounts, API tokens, TLS
  logging/           # structured logger, log tail SSE source
pkg/                 # (only if something is genuinely reusable by third parties)
docs/                # all design and reference docs
tests/
  unit/              # colocated *_test.go is preferred; this holds cross-cutting helpers
  e2e/               # black-box tests that drive a running server
.devcontainer/
docker/              # Dockerfile(s) and entrypoints
docker-compose.yml         # production
docker-compose.dev.yml     # development
```

## Coding conventions

- Go 1.26+ (latest stable — currently 1.26.1). `go vet` and `gofmt` clean. `staticcheck` on CI.
- Package names are lowercase, no underscores. No stutter (`state.State` → `state.World` or similar).
- Errors: wrap with `fmt.Errorf("doing X: %w", err)`. Sentinel errors in `errors.go` per package.
- Logging: structured via `log/slog`. No `fmt.Println` in server code.
- Context: every blocking call takes `context.Context`. The connection's context is cancelled on disconnect.
- Concurrency: prefer message-passing over shared locks for the hot path (connection → state). Locks are fine for rarely-contended config and storage layers.
- Tests: table-driven. Fakes live next to the interface they fake.

## What to do before committing

1. `go test ./...`
2. `go vet ./...`
3. `gofmt -l . && test -z "$(gofmt -l .)"`
4. If behaviour changed: update the relevant doc under `docs/`.
5. If a new external dependency was added: justify in `docs/ARCHITECTURE.md`.

## What *not* to do

- Don't add a web framework. `net/http` + `html/template` + htmx is enough.
- Don't add an ORM. Hand-written SQL per driver.
- Don't add a logging framework. `log/slog`.
- Don't add YAML support via a heavy dep if a smaller one works; see `docs/ARCHITECTURE.md#dependencies`.
- Don't mock the database in integration tests — spin up the real SQLite/Postgres the test targets.
- Don't add features beyond the current milestone in `docs/PLAN.md` without updating the plan first.

## Working with the RFCs

- RFC 1459 is the baseline. RFC 2812 supersedes it for clients; RFC 2813 for servers. When they disagree, follow 2812/2813 and cite the section in a comment.
- Keep a running notes file at `docs/PROTOCOL.md` for ambiguities and the decisions we made.

## See also

- [`AGENTS.md`](AGENTS.md) — how the Lua bot runtime + in-repo automation agents are organized.
- [`docs/PLAN.md`](docs/PLAN.md) — current milestone, exit criteria, what's next.
