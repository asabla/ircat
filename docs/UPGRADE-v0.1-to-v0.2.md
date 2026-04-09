# Upgrading from v0.1 to v0.2

This guide walks through everything that changes when you move
an existing v0.1 ircat deployment to v0.2. The summary up
front: **most installs need no config changes**. The defaults
ship with safer values, the federation transport learns a few
new tricks while staying wire-compatible with v0.1 peers, and
operators get more knobs but no removals.

## Compatibility

| Surface | v0.1 → v0.2 status |
|---|---|
| Config schema | Backward-compatible. New fields are optional with safe defaults. |
| Persistent SQLite databases | No migration required. Existing audit logs and operator tables read unchanged. |
| Persistent Postgres databases | Same. No migration required. |
| Federation wire protocol | Backward-compatible. v0.2 peers happily speak to v0.1 peers, with some new features (mode burst, TS collision, KILL routing) gracefully degrading on the v0.1 side. |
| Lua bot API surface | Backward-compatible. `string.dump` was removed (it was already supposed to be), `string.format` is now bounded but accepts all reasonable inputs. |
| Admin API endpoints | Additive. The new `POST /api/v1/config/reload` does not affect existing routes. |
| Metrics endpoint | Unchanged. Same metric names and labels. |

There are no breaking changes. You can do an in-place upgrade.

## Upgrade steps

1. Take a backup. (See `OPERATIONS.md` for the SQLite + Postgres
   recipes.)
2. Pull the new image: `docker compose pull ircat`.
3. Restart: `docker compose up -d ircat`.
4. Verify `GET /readyz` returns 200 and `ircat_messages_in_total`
   keeps incrementing.

That's it. The rest of this document explains what changed under
the hood and why you might want to opt into a new feature.

## What changes automatically

### SQLite audit-log throughput (~46x faster)

The default SQLite DSN was using WAL mode with the implicit
`synchronous=FULL` setting, which fsync'd every commit. v0.2
switches to the upstream-recommended WAL pairing
`synchronous=NORMAL`, which fsyncs at WAL checkpoint
boundaries instead.

Measured improvement on a typical ext4 host:

| | v0.1 | v0.2 |
|---|---|---|
| Serial Append (1 writer) | 8.4 ms | 183 µs |
| Parallel Append (b.RunParallel) | 9.4 ms | 73 µs |

The trade-off is a tiny window of "lost writes on power loss"
between checkpoints. That window is acceptable for the audit
log because every event is also pushed through the jsonl +
webhook sinks at publish time, so the persistent store is not
the only durability path. See the comment on `buildDSN` in
`internal/storage/sqlite/sqlite.go` for the full reasoning.

**No action required.** Your existing SQLite database will pick
up the new pragma on the next startup.

### Federation channel mode burst

In v0.1 federation peers exchanged user state and channel
membership at link-up but did not exchange channel modes. A
remote peer's view of `+t`/`+i`/`+k` could drift from the home
server. v0.2 fixes this:

- The link burst now carries TOPIC, the canonical mode word
  (`+ntk key`), and per-member `+o`/`+v` lines for every
  channel with at least one local member.
- Runtime `MODE` messages are re-applied on the receiver via
  `applyRemoteChannelMode`, so a `MODE #x +i` on node A is
  reflected in node B's channel state immediately.

Both directions are wire-compatible with v0.1 peers — a v0.1
peer that receives a mode line just delivers it to local
clients without re-applying, which is the v0.1 behaviour.

**No action required.** The improvement happens automatically
on link-up.

### Federation routing: subscription instead of fanout

v0.1 fanned every channel event to every federation peer. v0.2
switches to a per-channel subscription set: a peer receives
events for channel X only if it has been told about X (via
burst or via a runtime JOIN). JOINs themselves still fan out to
every peer because they are the discovery message that
establishes the subscription.

This is a strict efficiency improvement: peers without members
in a channel no longer see PRIVMSGs for it. The behaviour is
identical at the IRC layer; only the wire traffic changes.

If you hit a regression and need the v0.1 behaviour back, set:

```yaml
federation:
  broadcast_mode: fanout
```

This knob is documented to live for one minor cycle (it will be
removed in v0.3). Please file an issue if you actually use it.

### TS-based nick collision resolution

The federation burst NICK shape gains a TS positional param at
position 7 (with the realname trailing param moving to
position 8). When two users on different nodes claim the same
nick, the lower TS wins per RFC 2813 §5.2: the higher-TS user
gets killed.

v0.1 peers omit the TS, in which case v0.2 falls back to the
legacy 7-param parse and treats the missing TS as zero — the
incoming claim wins by default, which is the v0.1 behaviour.

**No action required.** The improvement happens automatically
on the v0.2 side of any link.

### Lua sandbox tightening

Three changes, all on the safer side:

1. `string.dump` is now stripped from the sandbox. It was
   already supposed to be unreachable but the v0.1 audit found
   it was still exposed by gopher-lua's `OpenString`. Bots that
   tried to call it (which would have been a bug anyway) now
   get a "nil value" error.
2. `string.format` is wrapped with a `safeStringFormat` that
   refuses width and precision specifiers above 1024, refuses
   calls with more than 16 arguments, and clamps the rendered
   output to 8192 bytes. Normal use cases (e.g. `string.format("%s
   joined %s", nick, channel)`) are unaffected.
3. A new `Budget.RegistrySlots` field caps the gopher-lua data
   stack at a configurable number of LValue slots (default
   65536, ≈1 MiB). A bot that tries to grow a runaway table
   hits a Lua error rather than exhausting host memory.

If you have a bot that legitimately renders multi-kilobyte
output via `string.format` (e.g. building large NOTICE
payloads), bump `bots.per_bot_memory_mb` and consider raising
`maxFormatOutput` in source — but please file an issue first
because that almost always means your bot should be paginating.

## What you can opt into

### `POST /api/v1/config/reload`

A new bearer-token endpoint that triggers the same code path as
SIGHUP. Hot-reloads:

- `logging.level` — flipped via the new `slog.LevelVar`
  plumbing in `internal/logging`.
- Statically configured operators (`operators[]` in the config
  file) — re-synced into the operator store.
- `server.motd_file` — re-read into the server's MOTD cache.

Example:

```sh
curl -X POST -H "Authorization: Bearer ircat_xxx_yyy" \
  https://dashboard.example.org/api/v1/config/reload
```

Returns `{"status":"reloaded"}` on success or a JSON error
envelope on failure. A misconfigured reload (e.g. typo in
`logging.level`) leaves the previous state intact and surfaces
the parse error in both the response body and the audit log.

Sending `SIGHUP` to the running ircat process has the same
effect.

### Dashboard in-process TLS termination

v0.1's recommended dashboard deployment fronted the listener
with a reverse proxy (caddy/nginx/cloudflare). v0.2 also lets
ircat terminate TLS in-process via the existing `dashboard.tls`
config block:

```yaml
dashboard:
  enabled: true
  address: "0.0.0.0:8443"
  tls:
    enabled: true
    cert_file: /etc/ircat/dash-cert.pem
    key_file: /etc/ircat/dash-key.pem
```

Both deployment modes remain supported. The reverse-proxy
recipe still works unchanged because `tls.enabled` defaults to
false.

### Federation TLS listener

v0.1 supported outbound federation over TLS via `links[].tls`
plus optional fingerprint pinning. v0.2 adds the inbound
counterpart via two new `federation` config fields:

```yaml
federation:
  enabled: true
  listen_address: "0.0.0.0:7000"
  listen_cert_file: /etc/ircat/fed-cert.pem
  listen_key_file: /etc/ircat/fed-key.pem
  links:
    - name: peer.example.org
      accept: true
      ...
```

Set both `listen_cert_file` and `listen_key_file` to enable TLS
for inbound peers. Plain TCP keeps working when the fields are
empty.

## Removed

Nothing. v0.1 → v0.2 has no removals.

## Deprecated

`federation.broadcast_mode: fanout` is documented to live for
one minor cycle (until v0.3). The v0.2 default is
`subscription`, which is strictly better in every measured
case.

## Need help

- Check `docs/OPERATIONS.md` for the day-2 surface, including the
  measured envelope numbers from the v0.2 benchmark suites.
- Check `docs/SECURITY.md` for the Lua sandbox audit notes.
- Open an issue if a v0.2 default surprises you in a bad way.
