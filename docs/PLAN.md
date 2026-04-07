# Implementation Plan

Milestones are ordered. Do not start milestone N+1 until N's exit criteria are met. Each milestone should be shippable — the server runs, tests pass, something useful works.

---

## M0 — Scaffolding

**Goal:** the repo builds, tests run, a trivial binary starts and exits cleanly.

- `go.mod` initialized at `github.com/asabla/ircat` (or chosen path).
- Directory layout per `CLAUDE.md`.
- `cmd/ircat/main.go`: flag parsing, config load, signal-based shutdown skeleton.
- `internal/config`: `Config` struct, JSON + YAML loaders, `Validate()`, tests.
- `internal/logging`: `slog` setup, ring buffer.
- `.devcontainer/` working (VS Code open-in-container succeeds).
- `docker/Dockerfile` multi-stage build produces a <30 MB image.
- `docker-compose.dev.yml` runs the binary with `air`-style reload.
- `docker-compose.yml` runs the binary with Postgres.
- GitHub Actions (or placeholder) runs `go test ./... && go vet ./...`.

**Exit:** `docker compose -f docker-compose.dev.yml up` brings up an ircat that logs "ready" and responds to SIGINT.

---

## M1 — Protocol core

**Goal:** one user can connect, register, and get a welcome banner.

- `internal/protocol`:
  - `Message` type + `Parse([]byte)` + `(m Message) Bytes() []byte`.
  - Numeric reply constants (001–599 range, at least the ones we use).
  - Fuzz test for the parser (`go test -fuzz`).
- `internal/state`:
  - `World` with users + channels maps and sharded locks.
  - RFC 1459 case mapping.
- `internal/server`:
  - Plain TCP listener, per-connection goroutines.
  - Registration state machine: PASS → NICK → USER → RPL_WELCOME (001), RPL_YOURHOST (002), RPL_CREATED (003), RPL_MYINFO (004).
  - PING/PONG keepalive + timeout.
  - Graceful QUIT.
- Unit tests for parser, state mutations, registration.
- E2E test (`tests/e2e/registration_test.go`) that drives a real TCP connection against a started server and asserts on numerics.

**Exit:** `irssi -c localhost -p 6667` connects, sees the welcome banner, and `/quit` is clean. CI runs the e2e test.

---

## M2 — Channels & messaging

**Goal:** two users can join the same channel and talk.

- Commands: JOIN, PART, PRIVMSG, NOTICE, TOPIC, NAMES, LIST, WHO, WHOIS, KICK, INVITE, NICK (post-registration), QUIT.
- Channel modes: `+n +t +m +i +k +l +b +o +v`.
- User modes: `+i +w +o`.
- Flood control on PRIVMSG.
- Numeric replies for all the above, per RFC 2812.
- E2E tests for each command's happy path and the most common error paths.

**Exit:** two clients can join `#test`, chat, be kicked, be op'd, set a topic, and leave. Tests cover it.

---

## M3 — Persistence & storage

**Goal:** operators, API tokens, bots, persistent channel settings, and audit log survive restart.

- `internal/storage` interface + `sqlite` driver + migrations.
- `postgres` driver + migrations. Same SQL semantics, dialect-specific types.
- `OPER` command verifies against the operator store.
- Audit events (admin actions, opers, mode changes on persistent channels) written to the event store.
- Tests run against **both** drivers via a build matrix. No mocks.

**Exit:** kill the server mid-session, restart, operators still auth, persistent channels keep their modes/topic.

---

## M4 — Dashboard & admin API

**Goal:** an operator can log into the dashboard, see live activity, and take action.

- `internal/auth`: password hashing, session cookies, API tokens.
- `internal/api`: `/api/v1/` endpoints — see `API.md`.
- `internal/dashboard`:
  - Login page.
  - Overview (counts, uptime).
  - Users list with kick/kill actions.
  - Channels list with mode editor.
  - Settings page (hot-reloadable subset).
  - Log tail via SSE.
  - Live chat page (in-dashboard IRC client).
- htmx served as a single vendored JS asset in `internal/dashboard/static/`.
- `/metrics`, `/healthz`, `/readyz`.
- E2E: a Go test drives the HTTP API to create an oper, kick a user, and verify via IRC.

**Exit:** dashboard is usable end-to-end on a fresh install. API has token auth. Docs published.

---

## M5 — Lua bots

**Goal:** an operator can register a Lua bot that reacts to messages and can be hot-reloaded.

- `internal/bots`: supervisor, sandboxed runtime, event dispatch, per-bot KV.
- Dashboard: bot list, editor (textarea is fine for v1), enable/disable, logs.
- Example bots shipped in `examples/bots/`: echo, 8ball, scheduled announcer.
- CPU + instruction budget enforced via gopher-lua hooks.

**Exit:** an operator pastes a Lua script into the dashboard, it joins a channel, it reacts to `!ping` with `pong`.

---

## M6 — Event export

**Goal:** external systems can consume ircat events.

- Webhook sink (POST JSON, retry with exponential backoff, dead-letter to disk).
- JSONL file sink (one event per line, size-based rotation).
- Each sink is optional and independently configurable.
- Additional transports (Redis, Kafka, NATS, ...) can land later as drop-in `Sink` implementations without touching the bus.

**Exit:** a webhook endpoint receives events with at-least-once semantics; a jsonl file accumulates events and rotates on size.

---

## M7 — Federation

**Goal:** two ircat servers link and behave as one network.

Shipped:
- `internal/federation`:
  - PASS + SERVER handshake (outbound dial + inbound accept).
  - State burst (users + channel memberships).
  - Runtime propagation: NICK (registration + rename), QUIT, JOIN,
    PART, KICK, TOPIC, MODE, PRIVMSG, NOTICE.
  - Per-link OnActive/OnClosed callbacks so the broadcast hot path
    only sees fully-handshaked links.
- `cmd/ircat`:
  - Outbound dial supervisor with exponential-backoff reconnect
    (1s floor, 60s ceiling, reset on a clean run).
  - Inbound listener bound to `federation.listen_address`.
- Integration tests in `internal/server` and `cmd/ircat` that
  exercise both the static burst and runtime propagation paths
  end-to-end through real `*server.Server` instances.

Deferred to M8 / post-1.0:
- TLS on the federation transport (config field is in place).
- Channel-mode burst + ongoing MODE propagation re-application on
  the receiver (currently MODE is forwarded but not re-applied).
- TS-based nick/channel collision resolution.
- SQUIT recovery beyond "drop the link and forget remote users".
- Subscription-aware routing (currently every channel event is
  fanned out to every peer).
- KILL routing, SERVICE pseudo-server, WHOIS over link.

---

## M8 — Production hardening

- TLS on all listeners.
- Flood control tuning + benchmarks.
- Soak test (10k connections, 1k channels, 24h) on a reference machine; document the result.
- Security review of the Lua sandbox.
- Prometheus metrics for: connection count, message rate, link state, bot CPU, DB latency.
- Finalize `docker-compose.yml` (the production one) with healthchecks, resource limits, log rotation.
- Backup/restore docs for SQLite and Postgres.

**Exit:** v1.0.0 tagged. Release notes cover upgrade path from… well, nothing, but the docs are in place.

---

## Cross-cutting work (touch every milestone)

- Keep `docs/PROTOCOL.md` updated with ambiguities and decisions.
- Keep `docs/CONFIG.md` up to date with every new config field.
- Keep `AGENTS.md` current if the bot API surface grows.
- Every new feature ships with at least one unit test and, where it crosses an API boundary, an e2e test.

## Open questions (to resolve before M1 wraps)

1. Go module path — pick one and commit it.
2. License — MIT, Apache-2.0, or AGPL? Affects contribution appetite.
3. Do we ship a minimal YAML loader in-tree, or take the `sigs.k8s.io/yaml` dependency? Defer decision to M2.
4. IRCv3 `message-tags` — adopt in M2 or push to v2? Current plan: push to v2, but leave room in the parser.
5. Nickname case mapping: RFC 1459 only, or also `rfc7613`/`ascii`? Current plan: RFC 1459 default, `ascii` opt-in.
