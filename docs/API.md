# Admin API

HTTP/JSON admin API. Used by the dashboard, by external automation, and by operators who prefer `curl` to the web UI.

## Base

- Base URL: `/api/v1/`
- Content type: `application/json; charset=utf-8`
- Auth: `Authorization: Bearer <token>` header. Tokens are minted from the dashboard or the CLI (`ircat token create`).
- Errors: `{ "error": { "code": "not_found", "message": "..." } }` with an appropriate HTTP status.

Every mutation writes an audit event (see `docs/EVENTS.md`).

## Endpoints

### Health

- `GET /healthz` — 200 if the process is up.
- `GET /readyz` — 200 once storage has migrated and the listener is accepting.

### Server

- `GET /api/v1/server` — version, uptime, server name, network, listener info.
- `POST /api/v1/server/reload` — reload hot-reloadable config sections.
- `POST /api/v1/server/rawsend` — body `{ "line": "WALLOPS :hello" }` — send a raw IRC line as the server. Requires `flags: [raw]` on the token.

### Users

- `GET /api/v1/users` — paginated list. Query params: `channel`, `server`, `limit`, `cursor`.
- `GET /api/v1/users/{nick}` — WHOIS-equivalent.
- `POST /api/v1/users/{nick}/kick` — body `{ "channel": "#x", "reason": "..." }`.
- `POST /api/v1/users/{nick}/kill` — body `{ "reason": "..." }`.

### Channels

- `GET /api/v1/channels` — list. Query params: `pattern`, `limit`, `cursor`.
- `GET /api/v1/channels/{name}` — detail incl. members, modes, topic, bans.
- `POST /api/v1/channels/{name}/mode` — body `{ "changes": "+o-v alice bob" }`.
- `POST /api/v1/channels/{name}/topic` — body `{ "topic": "..." }`.
- `DELETE /api/v1/channels/{name}/bans/{mask}` — remove a ban.

### Operators

- `GET /api/v1/operators` — list.
- `POST /api/v1/operators` — create. Body `{ "name": "...", "password": "...", "host_mask": "...", "flags": [...] }`.
- `PATCH /api/v1/operators/{name}` — update.
- `DELETE /api/v1/operators/{name}` — delete.

### Tokens

- `GET /api/v1/tokens` — list (metadata only, never the secret).
- `POST /api/v1/tokens` — create. Response contains the plaintext token exactly once.
- `DELETE /api/v1/tokens/{id}` — revoke.

### Bots

- `GET /api/v1/bots`
- `GET /api/v1/bots/{id}`
- `POST /api/v1/bots` — create. Body `{ "name": "...", "source": "...lua...", "enabled": true }`.
- `PUT /api/v1/bots/{id}` — replace source / settings. Triggers hot reload.
- `DELETE /api/v1/bots/{id}`
- `GET /api/v1/bots/{id}/logs` — last N lines.
- `GET /api/v1/bots/{id}/logs/stream` — `text/event-stream` tail.

### Events

- `GET /api/v1/events` — audit log. Query params: `since`, `type`, `limit`, `cursor`.
- `GET /api/v1/events/stream` — `text/event-stream` live tail.

### Federation

- `GET /api/v1/federation/links` — link status.
- `POST /api/v1/federation/links/{name}/connect` — initiate outbound.
- `POST /api/v1/federation/links/{name}/squit` — tear down.

## Pagination

Cursor-based. Responses include `{ "items": [...], "next_cursor": "..." }`. No cursor → last page.

## Rate limits

Per-token: 60 req/s burst, 600 req/min sustained. 429 with `Retry-After` on exceed. Mutations on `users/kill` and `operators` are additionally limited to 1/s.

## OpenAPI

An `openapi.yaml` will be generated from handler annotations in M4. Until then, this file is the reference.
