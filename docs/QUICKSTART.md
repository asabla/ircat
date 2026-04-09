# QUICKSTART

This guide takes you from a clean checkout to a logged-in
operator dashboard in five minutes. The destination is the same
production stack the rest of the docs talk about: ircat in a
distroless container, Postgres for persistence, the dashboard
on a host port, and an admin operator you can immediately log
in as.

If you only need a smoke test on your dev box, jump to [Single
binary, no Docker](#single-binary-no-docker).

## Container stack (recommended)

### 1. Copy `.env.example` to `.env` and set the secrets

```sh
cp .env.example .env
$EDITOR .env
```

The required fields are:

| Variable | What |
|---|---|
| `IRCAT_INITIAL_ADMIN_PASSWORD` | Plaintext password for the bootstrap admin operator. Hashed at first boot via argon2id; the plaintext never lives in the database. **Do not leave at the default `change-me`.** |
| `POSTGRES_PASSWORD` | Postgres role password. Must match the DSN compose builds for ircat. |
| `IRCAT_VERSION` | Image tag (default `dev` for source builds). |

Optional: `IRCAT_DASHBOARD_PORT` if `8080` is taken by another
service on the host (the example `.env` sets it to `9696`).

### 2. Bring the stack up

```sh
docker compose up -d
```

First-boot sequence in the logs:

```
ircat starting ... server_name=irc.example.org
event sink subscribed type=jsonl
bootstrapped initial admin username=admin
ircat ready
dashboard listener bound address=[::]:8080
listener bound address=[::]:6667
```

The line you want to see is **`bootstrapped initial admin
username=admin`**. If you do not see it, jump to [What if the
admin bootstrap is silent?](#what-if-the-admin-bootstrap-is-silent).

### 3. Log into the dashboard

Open `http://<host>:<dashboard-port>/dashboard` in a browser.
With the default `.env` that's `http://localhost:9696/dashboard`.

Sign in with:

| Field | Value |
|---|---|
| **Operator** | `admin` |
| **Password** | whatever you put in `IRCAT_INITIAL_ADMIN_PASSWORD` |

You should land on the overview page with live metric cards
and the sidebar nav. From here you can mint API tokens, create
more operators, manage bots, and inspect federation links —
all without ever touching `curl`.

### 4. Connect a real IRC client

Point any IRC client at the host on port `6667`:

```
/server <host> 6667
/nick mynick
```

The default config does **not** enable the `:6697` TLS
listener — there's no shipped certificate. To enable it,
uncomment the second listener in `config/production.yaml` and
mount a real cert chain at `/etc/ircat/tls/`.

## What if the admin bootstrap is silent?

If startup logs contain a WARN like:

```
WARN initial admin bootstrap skipped — operators table is empty and config is incomplete
  missing=auth.initial_admin.password (set IRCAT_INITIAL_ADMIN_PASSWORD or password_env)
  recovery=run `ircat operator add <username>` against this store, or set the missing field and restart
```

…then the container started but no operator exists, and the
dashboard sign-in form will reject every credential. Three
ways to fix it:

### A. Set the missing env and restart

The most common cause is forgetting to set
`IRCAT_INITIAL_ADMIN_PASSWORD` in `.env`. Set it, then:

```sh
docker compose down
docker compose up -d
```

The bootstrap path runs again on restart and will create
`admin` with the now-resolved password.

### B. Mint an operator via the CLI

The container ships with an `ircat operator` subcommand for
exactly this case. From the host:

```sh
docker compose exec ircat sh -c 'echo hunter2 | /app/ircat operator add admin --config /etc/ircat/config.yaml --flags all'
```

Output:

```
operator "admin" created
```

The new operator is immediately usable on the dashboard sign-
in form — no restart required.

### C. List operators to confirm what's there

```sh
docker compose exec ircat /app/ircat operator list --config /etc/ircat/config.yaml
```

Tab-separated `<name> host=<mask> flags=<csv>` per line. Use
this whenever you're unsure what credentials a deployed
instance has.

## `ircat operator` reference

Three verbs, all of them open the same store the server uses:

```sh
ircat operator add <username>     # mint or upsert
ircat operator list               # show every persisted operator
ircat operator delete <username>  # remove
```

Common flags:

| Flag | Meaning | Default |
|---|---|---|
| `--config <path>` | Config file to read storage settings from | `/etc/ircat/config.yaml` |

`add`-only flags:

| Flag | Meaning | Default |
|---|---|---|
| `--password-file <path>` | Read password from a file (one line, trailing newline stripped) | unset → reads stdin |
| `--host-mask <mask>` | Restrict the operator to a hostmask in IRC glob form (e.g. `*@10.0.0.*`) | empty → any host |
| `--flags <csv>` | Comma-separated flag list (`kill,kline,rehash,...`) | `all` |

The password is hashed via argon2id before being persisted.
The plaintext is never written to the database, the audit log,
or the process logs.

`add` is upsert: running it against an existing username
replaces the password hash, host mask, and flag set with the
new values. This is how you rotate an admin password from the
host without going through the dashboard.

## Single binary, no Docker

If you just want to poke at ircat on your dev box:

```sh
go build -o ./ircat ./cmd/ircat

cat > /tmp/ircat-dev.yaml <<'EOF'
version: 1
server:
  name: irc.dev
  network: DevNet
  listeners:
    - address: "127.0.0.1:6667"
      tls: false
storage:
  driver: sqlite
  sqlite:
    path: /tmp/ircat-dev.db
dashboard:
  enabled: true
  address: "127.0.0.1:8080"
auth:
  password_hash: argon2id
EOF

# Mint an admin operator before starting the server (or skip
# this and let auth.initial_admin in the config bootstrap one
# at first boot).
echo hunter2 | ./ircat operator add admin --config /tmp/ircat-dev.yaml --flags all

./ircat server --config /tmp/ircat-dev.yaml
```

Then open `http://localhost:8080/dashboard` and sign in as
`admin` / `hunter2`.

Cleanup: `Ctrl+C` the server and `rm /tmp/ircat-dev.db
/tmp/ircat-dev.yaml`.

## Next steps

- [`OPERATIONS.md`](OPERATIONS.md) for the day-2 surface:
  metrics, backup, restore, soak harness, incident response.
- [`CONFIG.md`](CONFIG.md) for the full config schema, every
  field documented.
- [`SECURITY.md`](SECURITY.md) for the trust boundaries and
  the Lua sandbox audit notes.
- [`DASHBOARD.md`](DASHBOARD.md) for the dashboard surface
  beyond the sign-in form.
