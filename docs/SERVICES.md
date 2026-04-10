# Services Daemon — W4 Design

This document describes the W4 workstream: a first-party services
daemon built on top of the SERVICE/SQUERY/SERVLIST framework that
shipped in v1.0. The goal is to let operators run a usable network
without bringing their own ChanServ or NickServ.

## Goals

1. **Account framework.** Per-user accounts with password, optional
   email, and SASL PLAIN authentication at connection time. This is
   the precondition for NickServ, ChanServ, MemoServ, and the W1
   account-aware IRCv3 caps (`account-tag`, `extended-join`).
2. **NickServ.** Nickname reservation, imposter enforcement, and
   owner reclaim.
3. **ChanServ.** Channel registration, founder restoration after
   empty, op grants on join, persistent settings.
4. **MemoServ.** Offline message delivery.

## How services connect

Each service is an in-process goroutine that registers via the
existing SERVICE wire command (RFC 2812 section 3.1.6). The
`handleService` handler in `internal/server/handler_service.go`
creates a `state.User` with `Service=true`. From the perspective
of every other user and of federation, a service looks like a
normal connection that happens to:

- appear in SERVLIST (RPL_SERVLIST 234 / RPL_SERVLISTEND 235),
- receive traffic via SQUERY instead of PRIVMSG,
- skip channel broadcast delivery.

Services register after the server's TCP listener is up and after
the storage layer has been migrated. They hold a direct reference
to `*Server` (or a narrow interface thereof) so they can inject
protocol messages without a real TCP socket — the same trick the
Lua bot runtime uses.

Each service owns one write loop and processes inbound SQUERYs
sequentially. There is no shared mutable state between services
beyond the `storage.Store` and `state.World` they all read from.

## Account framework

### Record

```go
// Account is one user account. Accounts are local to the node
// that created them; federation does not replicate them.
type Account struct {
    ID           string    // ULID
    Username     string    // unique, case-mapped like nicknames
    PasswordHash string    // argon2id PHC string (same as Operator)
    Email        string    // optional; used for recovery, not verified by default
    Verified     bool      // future: email verification gate
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

### AccountStore interface

Follows the same shape as `OperatorStore` in
`internal/storage/storage.go`:

```go
type AccountStore interface {
    Get(ctx context.Context, username string) (*Account, error)
    GetByID(ctx context.Context, id string) (*Account, error)
    List(ctx context.Context) ([]Account, error)
    Create(ctx context.Context, acct *Account) error
    Update(ctx context.Context, acct *Account) error
    Delete(ctx context.Context, username string) error
}
```

`Store` gains an `Accounts() AccountStore` method. Both the SQLite
and Postgres drivers implement it.

### Storage schema

```sql
CREATE TABLE accounts (
    id            TEXT PRIMARY KEY,          -- ULID
    username      TEXT NOT NULL UNIQUE,      -- case-mapped
    password_hash TEXT NOT NULL,             -- argon2id PHC string
    email         TEXT NOT NULL DEFAULT '',
    verified      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,             -- RFC 3339
    updated_at    TEXT NOT NULL              -- RFC 3339
);

CREATE UNIQUE INDEX idx_accounts_username ON accounts (username);
```

Postgres uses `BOOLEAN` for `verified` and `TIMESTAMPTZ` for the
timestamp columns; the Go struct is the same.

### Password hashing

Reuses `internal/auth.HashPassword` and `internal/auth.Verify`,
which already implement argon2id with the OWASP-recommended
parameters (memory=64 MiB, iterations=3, parallelism=4). No new
crypto code.

### SASL PLAIN

SASL PLAIN wires into CAP negotiation. The flow:

1. Client sends `CAP LS`. Server advertises `sasl` among its caps.
2. Client sends `CAP REQ :sasl`. Server ACKs.
3. Client sends `AUTHENTICATE PLAIN`.
4. Server responds `AUTHENTICATE +`.
5. Client sends `AUTHENTICATE <base64(authzid\0authcid\0password)>`.
6. Server looks up `authcid` in AccountStore, verifies the password
   hash. On success: `903 RPL_SASLSUCCESS`. On failure:
   `904 ERR_SASLFAIL`.
7. Client sends `CAP END` and continues registration as normal.

The authenticated account name is stashed on the `Conn` and
propagated to `state.User.Account`. This field is what
`account-tag` and `extended-join` (W1) read at message time.

If a client authenticates via SASL and the account owns the
requested NICK, NickServ enforcement is skipped — the user is
already identified.

## NickServ

Service name: `NickServ`. Registers as
`SERVICE NickServ * * NickServ * :Nickname services`.

### Nick ownership table

```sql
CREATE TABLE nick_owners (
    nick       TEXT PRIMARY KEY,    -- case-mapped
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    primary_   INTEGER NOT NULL DEFAULT 0,  -- 1 if this is the account's primary nick
    created_at TEXT NOT NULL
);
```

An account may own multiple nicks (GROUP). One nick is marked
primary.

### Commands

All commands are sent via `SQUERY NickServ :<command> [args...]`
or, for client convenience, `/msg NickServ <command> [args...]`
(NickServ accepts both SQUERY and PRIVMSG).

| Command | Syntax | Effect |
|---------|--------|--------|
| REGISTER | `REGISTER <password> [email]` | Creates an account using the caller's current nick as username. Hashes the password, stores the record, marks the nick as owned. |
| IDENTIFY | `IDENTIFY [nick] <password>` | Authenticates against the account that owns the given nick (or the caller's current nick). On success, sets `User.Account`. |
| DROP | `DROP [nick]` | Releases a nick from the caller's account. If it is the primary nick, the account is deleted. |
| INFO | `INFO <nick>` | Shows public account info: registration date, last seen, whether the nick is currently online. |
| GROUP | `GROUP` | Links the caller's current nick to their already-identified account. |
| GHOST | `GHOST <nick> [password]` | Disconnects a session using a nick the caller owns. Requires either prior IDENTIFY or inline password. |
| RELEASE | `RELEASE <nick> [password]` | Releases a held nick (after GHOST or a timeout) so it can be used. |

### Enforcement

When a user connects or changes nick to a registered nick:

1. NickServ sends a NOTICE: "This nickname is registered. You have
   60 seconds to identify via `/msg NickServ IDENTIFY <password>`
   or SASL."
2. After 60 seconds (configurable), if the user has not identified,
   NickServ force-renames them to a guest nick (`Guest<random>`)
   using a server-issued NICK change.

If the account owner is already online (SASL-authenticated or
identified) and someone else takes the nick during a brief
disconnect, the owner can GHOST the imposter.

## ChanServ

Service name: `ChanServ`. Registers as
`SERVICE ChanServ * * ChanServ * :Channel services`.

### Channel registration table

```sql
CREATE TABLE registered_channels (
    channel    TEXT PRIMARY KEY,     -- case-mapped
    founder_id TEXT NOT NULL REFERENCES accounts(id),
    guard      INTEGER NOT NULL DEFAULT 1,  -- auto-op founder on join
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE channel_access (
    channel    TEXT NOT NULL REFERENCES registered_channels(channel) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    flags      TEXT NOT NULL DEFAULT '',  -- "o" = auto-op, "v" = auto-voice
    created_at TEXT NOT NULL,
    PRIMARY KEY (channel, account_id)
);
```

### Commands

| Command | Syntax | Effect |
|---------|--------|--------|
| REGISTER | `REGISTER <#channel>` | Registers the channel to the caller's account. Caller must be opped and identified. |
| DROP | `DROP <#channel>` | Unregisters. Founder only. |
| OP | `OP <#channel> [nick]` | Grants +o. Founder or anyone with `o` access flag. Defaults to caller. |
| DEOP | `DEOP <#channel> [nick]` | Removes +o. Same access rules. |
| INFO | `INFO <#channel>` | Shows founder, registration date, guard status. |
| SET | `SET <#channel> <option> <value>` | Configures channel options. See below. |

SET options:

| Option | Values | Effect |
|--------|--------|--------|
| `GUARD` | `ON` / `OFF` | Auto-op the founder on join. Default ON. |
| `KEEPTOPIC` | `ON` / `OFF` | Restore the topic after the channel empties and is re-created. Default ON. |
| `FOUNDER` | `<account>` | Transfer channel ownership. |

### Behaviour

- **Founder restoration.** When a registered channel empties and a
  new user joins, ChanServ joins the channel, sets the persisted
  topic (if KEEPTOPIC), and ops any joining user who has the `o`
  access flag. ChanServ then parts.
- **Auto-op on join.** If GUARD is ON, ChanServ watches JOIN events.
  When a user with `o` access joins a registered channel, ChanServ
  issues `MODE #chan +o nick`.

ChanServ listens on the event bus for JOIN events rather than
polling. This avoids coupling ChanServ to the connection read loop.

## MemoServ

Service name: `MemoServ`. Registers as
`SERVICE MemoServ * * MemoServ * :Memo services`.

### Memo table

```sql
CREATE TABLE memos (
    id          TEXT PRIMARY KEY,   -- ULID
    sender_id   TEXT NOT NULL REFERENCES accounts(id),
    recipient_id TEXT NOT NULL REFERENCES accounts(id),
    body        TEXT NOT NULL,
    read        INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);

CREATE INDEX idx_memos_recipient ON memos (recipient_id, read);
```

### Commands

| Command | Syntax | Effect |
|---------|--------|--------|
| SEND | `SEND <nick> <message>` | Queues a memo to the account owning `<nick>`. Both sender and recipient must have accounts. |
| LIST | `LIST` | Lists the caller's memos (id, sender, date, read status). |
| READ | `READ <id>` | Displays the memo body and marks it read. |
| DELETE | `DELETE <id>` | Deletes a memo. |

### Delivery

When a user identifies (SASL or NickServ IDENTIFY), MemoServ
checks for unread memos. If any exist, it sends a NOTICE:
"You have N unread memo(s). Use `/msg MemoServ LIST` to read
them."

Memos are not pushed automatically — the user must LIST/READ.
This keeps the protocol surface simple and avoids flooding a user
who logs in after a long absence.

## Federation considerations

**Accounts are node-local.** An account created on node A does not
exist on node B. This is the same model operators use: the
`OperatorStore` is per-node, not replicated.

Rationale: replicating accounts across the federation mesh requires
a consensus protocol or conflict resolution for concurrent
registrations. That complexity is not justified for the initial
implementation. An operator running a federated mesh can either:

1. Direct all users to register on one "hub" node (the common
   real-world pattern for Atheme/Anope), or
2. Run independent account databases per node and accept that an
   account on node A is unknown on node B.

Services themselves are visible across federation via the normal
SERVICE propagation (the `handleService` handler calls
`announceUserToFederation`). A client on node B can SQUERY a
service on node A; the SQUERY routes through the spanning tree
like any targeted message. The service processes the command and
replies via NOTICE, which routes back.

The `ServiceDistribution` field on `state.User` (the `<distribution>`
mask from the SERVICE registration) is stored but not enforced in
v1. A future pass can use it to limit which nodes see a given
service — useful for running NickServ on only the hub node of a
mesh.

## Configuration

Services are enabled in the config file:

```yaml
services:
  enabled: true
  nickserv:
    enabled: true
    enforce_timeout: 60   # seconds before guest-rename
    guest_prefix: "Guest"
  chanserv:
    enabled: true
    guard_default: true
  memoserv:
    enabled: false         # opt-in; not part of minimum viable
```

The `ircat services` subcommand is a convenience that starts the
server with `services.enabled=true` and brings up whichever
services are individually enabled. It is not a separate process —
it is the same `ircat` binary with a flag preset.

## Implementation phasing

### Phase 1 — AccountStore + NickServ (minimum useful)

Deliverables:
- `Account` record and `AccountStore` interface in
  `internal/storage/storage.go`.
- SQLite and Postgres driver implementations with migrations.
- `sasl` CAP negotiation and AUTHENTICATE handler in
  `internal/server/`.
- `User.Account` field on `state.User`.
- NickServ service in `internal/services/nickserv/`.
- REGISTER, IDENTIFY, DROP, INFO, GHOST commands.
- Nick enforcement (60s timeout, guest rename).
- `ircat services` subcommand wiring.
- Tests: unit tests for AccountStore, integration tests for SASL
  flow, e2e test for NickServ REGISTER + IDENTIFY + enforcement.

This is the exit criterion for the minimum useful W4 deliverable.

### Phase 2 — ChanServ

Deliverables:
- `registered_channels` and `channel_access` tables + store
  interface.
- ChanServ service in `internal/services/chanserv/`.
- REGISTER, DROP, OP, DEOP, INFO, SET commands.
- Founder restoration and auto-op on join.
- Tests: channel registration round-trip, founder restore after
  empty, access flag enforcement.

### Phase 3 — MemoServ

Deliverables:
- `memos` table + store interface.
- MemoServ service in `internal/services/memoserv/`.
- SEND, LIST, READ, DELETE commands.
- Unread notification on identify.
- Tests: send/receive round-trip, offline delivery, deletion.

### Phase 4 — GROUP, RELEASE, and polish

Deliverables:
- NickServ GROUP and RELEASE commands.
- ChanServ KEEPTOPIC persistence.
- `account-tag` and `extended-join` IRCv3 caps (W1 crossover).
- Retention policy for memos (configurable max age / max count).

## Exit criteria (from PLAN.md)

> `ircat services` is a real subcommand that brings up ChanServ +
> NickServ on the local node, registered with the SERVICE form,
> persisting accounts to the same store.

This is satisfied at the end of Phase 2. Phase 3 and 4 are
follow-on work that can ship in separate tagged releases.

## Open questions

1. **Should NickServ accept PRIVMSG in addition to SQUERY?**
   Practically every IRC client sends `/msg NickServ` rather than
   `/squery NickServ`. The simplest answer is yes — NickServ
   listens for both. The handler dispatches identically regardless
   of how the message arrived.

2. **Account name vs. primary nick.** In the design above, the
   account username is the nick used at REGISTER time, and
   additional nicks are linked via GROUP. An alternative is to
   decouple usernames from nicks entirely (like Atheme). The
   coupled model is simpler and matches what users expect from
   traditional NickServ. Revisit if operators request decoupling.

3. **Email verification.** The `verified` column exists but nothing
   sets it to true in Phase 1. A future pass can add an
   operator-configurable verification flow (send a code, require
   it before the account is fully active). For now, registration
   is immediate.

4. **Rate limiting on REGISTER.** To prevent abuse, NickServ should
   enforce a per-IP cooldown on REGISTER (e.g., one account per IP
   per hour). This can reuse the existing flood-control token bucket
   infrastructure.
"},"caller":{"type":"direct"}}],"stop_reason":"tool_use