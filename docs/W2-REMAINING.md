# W2 — Remaining Federation Sharpening Work

This document tracks the W2 items that are not yet implemented.
The three-node mesh soak harness and the federation link stats
API endpoint shipped; the items below need their own PRs.

## Burst compression (zlib flag)

### What the RFC says

RFC 2813 mentions compressed server-link bursts as an optional
capability. The idea is that during a link burst (the initial
state sync after two servers connect), the sending side wraps the
payload in zlib compression. This is most useful on slow or
bandwidth-constrained links carrying large channel state.

### Implementation sketch

1. Add a `compress` boolean to the per-link federation config.
2. During the link handshake (SVINFO exchange), advertise a
   `ZIP` capability flag if both sides have it enabled.
3. After SVINFO, wrap the link's write side in a
   `compress/zlib.Writer` for the duration of the burst. Switch
   back to raw writes after the burst completes (after the last
   channel mode line).
4. The receiving side detects the zlib header and wraps the read
   side in a `compress/zlib.Reader` for the same window.

### Scope estimate

Small — the compression window is bounded to the burst phase, so
the framing change is confined to `internal/federation/link.go`.
The tricky part is negotiating the capability cleanly during the
handshake without breaking the strict PASS -> SERVER -> SVINFO
ordering.

### Dependencies

None. Can be implemented independently of W4 or W5.

## Operator account federation

### Current limitation

A user who OPERs on node A is granted `+o` locally. Node B in the
same federation mesh does not know about this — the operator
status is not propagated. This means KILL, WALLOPS, and DIE
issued by the operator on node B will fail with 481
ERR_NOPRIVILEGES unless the operator also OPERs on node B.

The `OperatorStore` is per-node (same as the account store in W4).
The issue is not about replicating the store; it is about
propagating the runtime `+o` flag across the link.

### Proposed solution

Add a lightweight RPC over the federation link:

1. When a local user successfully OPERs (handleOper grants `+o`),
   send a new server-to-server message:
   ```
   :localserver OPER <nick> <operator-name>
   ```
   This is forwarded to every peer link.

2. On the receiving side, the federation handler looks up the user
   in the local World (they exist as a remote user from the burst)
   and sets `+o` on their mode string.

3. On SQUIT (netsplit), the remote user is removed anyway, so the
   `+o` propagation is naturally cleaned up.

### Edge cases

- **TS collision.** If the operator changes nick between the OPER
  grant and the message arriving at the peer, the OPER message
  targets the old nick. The receiver should resolve by nick (which
  may fail) rather than crashing. A no-op drop is acceptable.

- **De-oper.** If the user does `MODE nick -o` locally, send a
  corresponding `:localserver DEOPER <nick>` to peers.

- **Security.** The RPC trusts the link — it does not verify the
  operator against the remote store. This is consistent with how
  federation trusts NICK and JOIN from peers.

### Scope estimate

Medium. The wire protocol change is small (one new S2S verb), but
the handler needs to be careful about the nick-resolution race
and the de-oper path. Testing requires the three-node mesh
harness (now available).

### Dependencies

None, but benefits from W5's soak harness for integration testing.
