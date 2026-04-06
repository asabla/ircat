# Event Export

ircat emits a structured event stream for everything that happens on the network. Events can be consumed in-process (dashboard, audit log) or exported to external systems.

## Event shape

Every event is JSON with this envelope:

```json
{
  "id": "01HN8X9Y...",          // ULID
  "ts": "2026-04-06T10:12:33Z", // RFC 3339, UTC
  "server": "irc.example.org",
  "type": "message",             // see table below
  "actor": { ... },
  "target": { ... },
  "data": { ... }
}
```

## Event types

| Type | Trigger | `data` contents |
|------|---------|-----------------|
| `connect` | Client finishes registration | nick, user, host, tls |
| `disconnect` | Client quits or is killed | reason |
| `message` | PRIVMSG | channel or target nick, text |
| `notice` | NOTICE | channel or target nick, text |
| `join` | JOIN | channel |
| `part` | PART | channel, reason |
| `kick` | KICK | channel, victim, reason |
| `mode` | MODE change | channel or user, changes |
| `topic` | TOPIC change | channel, topic |
| `nick_change` | NICK post-registration | old_nick, new_nick |
| `oper_up` | OPER success | operator name, flags |
| `kill` | KILL | victim, reason |
| `netjoin` | Federation link established | peer |
| `netsplit` | Federation link lost | peer, reason |
| `admin_action` | Dashboard/API mutation | endpoint, params (secrets redacted) |
| `bot_event` | Lua bot emitted a log line | bot_id, level, message |
| `config_reload` | SIGHUP or API reload | sections_changed |

New types may be added; consumers must ignore unknown types gracefully.

## Sinks

Configured in `config.events.sinks`. Each sink is optional and independently enabled. All sinks receive the same events; filtering is at the sink level.

### JSONL file

Append-only file, rotated by size. Default sink. Good for "just give me the raw stream" and for piping into `jq`.

```yaml
- type: jsonl
  path: /var/log/ircat/events.jsonl
  rotate_mb: 100
  keep: 7
  types: []   # empty = all types
```

### Redis Streams

Uses `XADD` with a `MAXLEN ~` to cap stream size. Single Redis round-trip per event. Batched when the outbound queue is backed up.

```yaml
- type: redis
  address: redis:6379
  stream: ircat:events
  maxlen: 100000
  types: [message, join, part]
```

Implementation note: we speak the Redis protocol directly with a tiny in-tree client, not `go-redis`. Streams + XADD is all we need and the wire format is <150 lines of code.

### Webhook

POST JSON to a configured URL. Supports:
- HMAC-SHA256 signature in `X-Ircat-Signature` header (secret from env).
- Retry with exponential backoff.
- Dead-letter queue on disk after max attempts.
- Batching: up to 100 events per POST, or 1s age, whichever first.

```yaml
- type: webhook
  url: https://hooks.example.org/ircat
  secret_env: IRCAT_WEBHOOK_SECRET
  batch_max: 100
  batch_max_age_ms: 1000
  retry:
    max_attempts: 5
    backoff_seconds: [1, 2, 5, 15, 60]
  dead_letter_path: /var/lib/ircat/dlq/webhook.jsonl
  types: []
```

## Delivery semantics

- **Audit log** (DB): synchronous for `admin_action`, `oper_up`, `kill` — the action is not considered complete until logged. Everything else is fire-and-forget to the DB.
- **External sinks:** at-least-once for webhook (DLQ captures what we couldn't deliver), best-effort for redis and jsonl (they drop on backpressure with a metric incremented).
- **Ordering:** events are ordered per-actor within a single process. Across federation, ordering is not guaranteed — rely on the `ts` + `id` fields for merging.

## Privacy and redaction

- `admin_action` events redact password fields, API token secrets, and anything whose config key ends in `_env`, `_password`, `_secret`, or `_token`.
- PRIVMSG content is emitted verbatim. Operators who need privacy compliance must either disable `message` export or filter at the sink.

## Metrics

- `ircat_events_emitted_total{type}`
- `ircat_events_dropped_total{sink}`
- `ircat_events_dlq_size{sink}`
- `ircat_events_deliver_latency_seconds{sink}`

## Testing

- Unit tests verify the envelope shape and redaction.
- Integration test pipes to a `jsonl` sink in a temp dir and asserts on the produced file after a scripted session.
- Webhook sink is tested with `httptest.NewServer` including a forced failure scenario to exercise retry + DLQ.
- Redis sink is tested against a real Redis container in CI.
