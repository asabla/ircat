# Configuration

ircat is configured by a single file. JSON and YAML are both accepted; the file extension selects the decoder (`.json`, `.yml`, `.yaml`). You can also pipe JSON over stdin with `--config -`.

Schema changes are a breaking change; document them here *and* bump the config `version` field.

## File layout

```yaml
version: 1                     # config schema version
server:
  name: irc.example.org        # advertised server name
  description: "ircat node"
  network: ExampleNet
  motd_file: /etc/ircat/motd.txt
  admin:
    name: "Alice Admin"
    email: "admin@example.org"
  listeners:
    - address: "0.0.0.0:6667"
      tls: false
    - address: "0.0.0.0:6697"
      tls: true
      cert_file: /etc/ircat/tls/cert.pem
      key_file: /etc/ircat/tls/key.pem
  limits:
    max_clients: 10000
    max_channels_per_user: 50
    nick_length: 30
    channel_length: 50
    topic_length: 390
    kick_reason_length: 255
    away_length: 255
    sendq_bytes: 1048576
    recvq_messages: 64
    ping_interval_seconds: 120
    ping_timeout_seconds: 240

storage:
  driver: sqlite               # sqlite | postgres
  sqlite:
    path: /var/lib/ircat/ircat.db
    journal_mode: wal
  postgres:
    dsn: "postgres://ircat:secret@db:5432/ircat?sslmode=require"
    max_open_conns: 20
    max_idle_conns: 5

dashboard:
  enabled: true
  address: "0.0.0.0:8080"
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
  session:
    cookie_name: ircat_session
    max_age_hours: 24
    secure: true
    same_site: lax

api:
  enabled: true
  # API shares the dashboard listener; this section only toggles the router.
  allow_origins:
    - "https://admin.example.org"

auth:
  password_hash: argon2id      # argon2id | bcrypt
  argon2id:
    memory_kib: 65536
    iterations: 3
    parallelism: 2
  initial_admin:
    # Only used on first boot if the operator table is empty.
    username: admin
    password_env: IRCAT_INITIAL_ADMIN_PASSWORD

events:
  sinks:
    - type: jsonl
      path: /var/log/ircat/events.jsonl
      rotate_mb: 100
      keep: 7
    - type: webhook
      enabled: false
      url: https://hooks.example.org/ircat
      secret_env: IRCAT_WEBHOOK_SECRET
      timeout_seconds: 5
      retry:
        max_attempts: 5
        backoff_seconds: [1, 2, 5, 15, 60]

bots:
  enabled: true
  max_bots: 100
  per_bot_memory_mb: 32
  per_bot_instruction_budget: 1000000   # per tick/event

federation:
  enabled: false
  my_server_name: irc.example.org
  listen_address: "0.0.0.0:7000"  # bind for inbound peer connections; omit to disable accept
  links:
    - name: irc.peer.org
      accept: true
      connect: true
      host: 10.0.0.2
      port: 6667
      password_in_env: IRCAT_LINK_PEER_IN
      password_out_env: IRCAT_LINK_PEER_OUT
      tls: true
      tls_fingerprint: "sha256:..."

logging:
  level: info                  # debug | info | warn | error
  format: json                 # json | text
  ring_buffer_entries: 10000   # in-memory tail for dashboard SSE

operators:
  # Bootstrap operator blocks. These are merged with the DB-backed operators.
  # DB operators are authoritative for password auth; static entries here are
  # useful for immutable infra setups.
  - name: alice
    host_mask: "*@10.0.0.*"
    password_hash_env: IRCAT_OPER_ALICE
    flags: [kill, kline, rehash, die]
```

## Rules

- Any field taking a secret supports `<field>_env: NAME` to pull it from an environment variable. Prefer env vars in production.
- Paths are resolved relative to the config file's directory.
- `listeners` is a list — you can bind 6667 plain, 6697 TLS, and 7000 federation-only all at once.
- Hot-reloadable sections (on `SIGHUP` or via API `/api/v1/config/reload`): `server.motd_file`, `logging.level`, `operators`, `bots` (list, not the runtime itself), `events.sinks`. Everything else requires a restart.
- Validation errors must be actionable: mention the field path and what the expected value looks like.

## JSON form

The same schema, decoded by `encoding/json`. YAML maps become JSON objects. Sequences become arrays. No YAML-only features (anchors, merge keys, custom tags) are used.

## Environment variable reference

| Variable | Purpose |
|----------|---------|
| `IRCAT_CONFIG` | Path to config file. Overrides `--config`. |
| `IRCAT_INITIAL_ADMIN_PASSWORD` | First-boot admin password. Ignored after the operator table has rows. |
| `IRCAT_WEBHOOK_SECRET` | HMAC key for webhook sink. |
| `IRCAT_LINK_*` | Per-link passwords, as referenced by `*_env` fields. |
| `IRCAT_OPER_<NAME>` | Per-oper password hash, as referenced by `password_hash_env`. |

## Example minimal config

```yaml
version: 1
server:
  name: irc.local
  description: "dev"
  network: devnet
  listeners:
    - address: "0.0.0.0:6667"
      tls: false
storage:
  driver: sqlite
  sqlite:
    path: /tmp/ircat.db
dashboard:
  enabled: true
  address: "0.0.0.0:8080"
```
