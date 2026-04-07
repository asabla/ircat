# Federation

ircat federates via RFC 2813 server-to-server protocol. Multiple ircat nodes link into a spanning tree and appear to clients as a single network.

## Model

- **Spanning tree.** No cycles. Each server has one parent (except the root of any particular traversal) and zero or more children.
- **State is replicated, not centralized.** Every server knows every user and every channel on the network.
- **Messages route along the tree.** A PRIVMSG to a remote nick walks up to the common ancestor and down to the target.
- **Authority is local.** A user's "home" server is where it registered. KILL and nickname changes originate there.

## Link lifecycle

### 1. Handshake

Initiator sends:
```
PASS <password> <version> <flags> <options>
CAPAB <capabilities>
SERVER <servername> <hopcount> <token> :<description>
```

Responder validates the password against its link config, replies with its own `PASS` / `CAPAB` / `SERVER`, and the link transitions to `Bursting`.

### 2. Burst

Order matters (RFC 2813 §3.3):
1. **Servers behind me** — one `SERVER` line per known downstream server.
2. **Users** — one `NICK` line per known user with full metadata (nick, hopcount, username, host, server, usermode, realname, TS).
3. **Channels** — `NJOIN #chan @+alice,bob,...` plus their modes, bans, topic, and topic TS.

Bursts are full state; there is no delta.

### 3. Normal operation

Once both sides finish bursting, the link is `Active`. Every state-changing message (NICK, JOIN, PART, QUIT, MODE, TOPIC, KICK, KILL, PRIVMSG, NOTICE, AWAY) is propagated to all peers *except* the one it came from. Messages carry a prefix so every server can route replies back.

### 4. Netsplit

`SQUIT <server> :<reason>` tears the link down. Each side marks all users and channel memberships that traversed that link as gone and emits `QUIT :*.net *.split` locally. Channels that become empty are dropped.

### 5. Netjoin

When two previously-split segments reconnect, nickname and channel collisions are resolved by timestamp:
- **Nick collision (RFC 2813 §5.2.1):** same nick, different user@host → kill both. Same nick, same user@host → keep the older TS, kill the younger.
- **Channel collision:** take the union of members and bans. For modes, the older channel TS wins; newer modes are overridden.

This matches what every ircd in production does. When in doubt, copy solanum/charybdis's behaviour and cite it in code.

## Authentication

Each link is configured per-peer in `config.federation.links[]`:

```yaml
federation:
  my_server_name: irc.example.org
  links:
    - name: irc.other.org
      accept: true            # allow inbound
      connect: true           # initiate outbound
      host: 10.0.0.2
      port: 6667
      password_in: "shared-secret-1"   # what we expect from them
      password_out: "shared-secret-2"  # what we send
      tls: true
      tls_fingerprint: sha256:ABCDEF...
      zip: false
```

`tls_fingerprint` pins the peer's cert so a compromised CA cannot MITM the link. If both `tls_fingerprint` and passwords are configured, both must match.

## Routing

- `state` holds a `routing` table: `nick → server → next_hop_link`.
- When a message targets a nick:
  1. Look up the nick's home server.
  2. Find the next-hop link along the spanning tree.
  3. Enqueue the message on that link's writer.
- Broadcasts (e.g., `JOIN`, channel `PRIVMSG` to a mixed local/remote channel) fan out along every link except the source link.

## Timestamps

Every user has a `nickTS` (nickname set time). Every channel has a `channelTS` (creation time). Timestamps are Unix seconds. They are included in burst and in state-changing messages that can collide.

**Clock sync matters.** Document NTP as a hard operational requirement. The server logs a warning on link-up if the peer's clock differs by more than 30 seconds.

## What we deliberately don't do (v1)

- **TS6 SID routing.** Classic RFC 2813 uses server names as routing keys. TS6 uses 3-char server IDs and UIDs for performance. v1 sticks with names for simplicity; v2 may upgrade.
- **Burst compression.** `zip` flag in the handshake is reserved; v1 ignores it.
- **Services pseudo-server.** Atheme/Anope-style services require SJOIN/UID and TS6. Out of scope; Lua bots cover the use cases we care about.

## Measured latency

Source: `internal/server/federation_bench_test.go`. Rerun with
`go test -bench=BenchmarkFederation_PrivmsgRoundtrip ./internal/server/`.

| Metric | Value |
|---|---|
| Mean PRIVMSG roundtrip | ~38 µs |
| p50 | ~36 µs |
| p99 | ~89 µs |

Numbers from a single ext4 host on an Intel Xeon E-2286M
running both peers in-process bridged via `net.Pipe`. The
benchmark measures the wall-clock between `cAlice.Write` on
node A and the matching read on `cBob` on node B, averaged over
60k samples.

Real TCP loopback adds about 30-100 µs of kernel overhead per
direction. A two-host LAN deployment will see another
0.1-0.5 ms of network latency depending on the link. Treat the
in-process number as the **floor** of what the federation
broadcast + routing logic costs and add network round-trip on
top.

## Testing

- `tests/e2e/federation/` runs a Compose stack with two ircat nodes and a scripted test that:
  1. Links them.
  2. Joins clients on both.
  3. Verifies PRIVMSG delivery in both directions.
  4. Kills the link, verifies QUIT propagation.
  5. Restores the link, verifies burst reconciles state.

This test is part of CI and must pass before any federation change merges.
