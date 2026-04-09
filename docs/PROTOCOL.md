# IRC Protocol Notes

Running notes on RFC 1459 / 2810 / 2811 / 2812 / 2813 — what we implement, what we interpret, and what we deliberately differ on. Cite sections when writing code.

## References

- [RFC 1459](https://www.rfc-editor.org/rfc/rfc1459) — original IRC protocol (1993). The baseline.
- [RFC 2810](https://www.rfc-editor.org/rfc/rfc2810) — IRC architecture.
- [RFC 2811](https://www.rfc-editor.org/rfc/rfc2811) — channel management.
- [RFC 2812](https://www.rfc-editor.org/rfc/rfc2812) — client protocol (updates 1459).
- [RFC 2813](https://www.rfc-editor.org/rfc/rfc2813) — server-to-server protocol.
- IRCv3 — https://ircv3.net — deferred to v2.

Precedence: **2812 > 1459** for client commands, **2813 > 1459** for S2S, **2811** for channel semantics.

## Message format

```
message    = [":" prefix SPACE] command [params] crlf
prefix     = servername / (nickname [["!" user] "@" host])
command    = 1*letter / 3digit
params     = *14(SPACE middle) [SPACE ":" trailing]
           / 14(SPACE middle) [SPACE [":"] trailing]
middle     = nospcrlfcl *(":" / nospcrlfcl)
trailing   = *(":" / " " / nospcrlfcl)
crlf       = %x0D %x0A
```

Hard limits:
- **512 bytes** including the trailing `\r\n`.
- **15 parameters** max (14 "middle" + 1 "trailing" per RFC 2812 §2.3.1).
- Command is case-insensitive; normalize to uppercase internally.

Implementation rules:
- Parser must accept both `\r\n` and bare `\n` (legacy clients). Always emit `\r\n`.
- Trailing parameter is introduced by `SPACE :` and consumes the rest of the line, including spaces and `:`.
- Empty trailing is valid.
- A middle parameter cannot start with `:` (that's what forces the trailing syntax).

## Registration

Per RFC 2812 §3.1: PASS (optional) → NICK → USER → welcome burst. Order of NICK and USER is flexible; registration completes when both have been seen (and PASS has been verified if a server password is configured).

Welcome burst order:
1. `001 RPL_WELCOME`
2. `002 RPL_YOURHOST`
3. `003 RPL_CREATED`
4. `004 RPL_MYINFO`
5. `005 RPL_ISUPPORT` — one or more lines advertising capabilities (modern convention; not in 1459, but every client expects it).
6. MOTD (`375`, `372`*, `376`) or `422 ERR_NOMOTD`.

## Case mapping

RFC 1459 case mapping: `{` == `[`, `}` == `]`, `|` == `\`, `~` == `^`. Nickname and channel name comparisons use this map.

We advertise `CASEMAPPING=rfc1459` in `RPL_ISUPPORT`. Operators may switch to `ascii` via config; `rfc7613` deferred.

## Nickname rules

- 1–9 characters (we advertise `NICKLEN` in ISUPPORT; may raise to 30 by config).
- First character: letter or `[]\\`_^{|}`.
- Subsequent: letter, digit, `[]\\`_^{|}`, or `-`.

## Channel rules

All four RFC 2811 §2.1 channel-name prefixes are accepted:

| Prefix | Scope | Notes |
|--------|-------|-------|
| `#` | network-wide | Standard channel; persisted, federated. |
| `&` | server-local | Same machinery as `#` but never federated. |
| `+` | modeless | RFC 2811 §4.2.1. Accepts all members, no operators, MODE mutations rejected with 482. |
| `!` | safe (timestamped) | RFC 2811 §3. `JOIN !!short` allocates a fresh 5-char uppercase-alphanumeric ID; `JOIN !short` resolves an existing safe channel by suffix. The canonical wire form is always `!IDshort`. |

Advertised in ISUPPORT as `CHANTYPES=#&+!`.

- Max 50 characters (ISUPPORT `CHANNELLEN=50`).
- Cannot contain SPACE, BELL (`\x07`), `,`, or `:`.

## Modes

### Channel modes

| Mode | Takes param | Meaning |
|------|-------------|---------|
| `+o <nick>` | yes | Grant channel operator |
| `+v <nick>` | yes | Grant voice |
| `+b <mask>` | yes | Ban mask |
| `+e <mask>` | yes | Ban exception (overrides matching `+b`) |
| `+I <mask>` | yes | Invite exception (bypasses `+i`) |
| `+q <mask>` | yes | Quiet (matched users cannot speak; +o/+v override) |
| `+k <key>` | yes (set) | Channel key |
| `+l <n>` | yes (set) | User limit |
| `+a` | no | Anonymous channel — every prefix rewritten to `anonymous!anonymous@anonymous.` (RFC 2811 §4.2.1) |
| `+r` | no | Server reop — safe (`!`) channels only. When set, the channel survives going empty and the next rejoiner is auto-opped (RFC 2811 §4.2.5). Rejected with 472 on non-safe channels. |
| `+i` | no | Invite-only |
| `+m` | no | Moderated (only +v/+o can speak) |
| `+n` | no | No external messages |
| `+p` | no | Private (hidden from `LIST` to non-members) |
| `+s` | no | Secret (hidden from `LIST`, `NAMES`, `WHO`) |
| `+t` | no | Topic settable only by ops |

`+b`, `+e`, `+I`, and `+q` are list-form: bare `MODE #chan +b` /
`+e` / `+I` / `+q` returns the corresponding list via the
appropriate numerics:

| List mode | Entry numeric | End numeric |
|-----------|---------------|-------------|
| `+b` | 367 RPL_BANLIST | 368 RPL_ENDOFBANLIST |
| `+e` | 348 RPL_EXCEPTLIST | 349 RPL_ENDOFEXCEPTLIST |
| `+I` | 346 RPL_INVITELIST | 347 RPL_ENDOFINVITELIST |
| `+q` | 728 RPL_QUIETLIST | 729 RPL_ENDOFQUIETLIST |

The match algorithm is the simple IRC glob (`*`, `?`,
case-insensitive under the world's case mapping). Modeless (`+`)
channels reject every MODE mutation with 482; on `!` safe channels
all modes work normally on the canonical `!IDshort` name.

The first joiner of a `!`-safe channel receives `MemberCreator`
(rendered with the `!` NAMES prefix per RFC 2811 §4.3.5) instead
of regular `MemberOp`. The creator status implies operator
privileges and cannot be removed by anyone, including via
`MODE -o` — the channel founder is immortal until the channel
is destroyed. The full PREFIX advertisement is `PREFIX=(Oov)!@+`.

`MODE #chan +o-v alice bob` must be supported (batched). Max modes
per MODE command: advertise `MODES=4` in ISUPPORT. The full
CHANMODES advertisement is `CHANMODES=beIq,k,l,aimnpst`.

### User modes

| Mode | Meaning |
|------|---------|
| `+i` | Invisible — hidden from `WHO`/`NAMES` except to channel members |
| `+w` | Receives wallops |
| `+o` | IRC operator (set by OPER, never by user) |
| `+s` | Receives server notices |
| `+r` | Restricted (RFC 2812 §3.1.5) — server-set; blocks NICK changes and OPER attempts. Cannot be set or unset by the user. |

## Numerics we emit

Tracked in `internal/protocol/numeric.go`. At minimum: 001–005,
200–209, 211, 212, 219, 221, 242, 243, 251–255, 256–259, 262, 263,
301, 302, 303, 305, 306, 311–319, 321–323, 324, 329, 331–333, 341,
346, 347, 348, 349, 351, 352, 353, 364, 365, 366, 369, 371, 372,
374, 375, 376, 381, 382, 391, 401–408, 411, 412, 416, 421, 422,
431, 432, 433, 436, 437, 441, 442, 443, 445, 446, 451, 461, 462,
464, 465, 471, 472, 473, 474, 475, 481, 482, 483, 484, 485, 491,
501, 502, 728, 729.

## Implemented commands

The dispatch table in `internal/server/handler.go` covers the
following RFC 2812 verbs (and where they live):

| Command | RFC | Notes |
|---------|-----|-------|
| PASS, NICK, USER | §3.1 | Registration burst. PASS gates registration when `Server.ClientPassword` is set; the federation form parses version and flags fields. |
| CAP | IRCv3 | LS/REQ/END negotiation. Advertises `message-tags`; per-conn `capsAccepted` set tracks negotiated caps. |
| QUIT | §3.1.7 | Broadcasts QUIT to shared-channel members; sends ERROR before disconnect. |
| JOIN, PART | §3.2.1-2 | Including +i/+k/+l/+b/+e/+I/+q gates. JOIN supports `!!short` (create safe channel) and `!short` (resolve existing). |
| MODE | §3.2.3-4 | Channel + user, list-form `b`/`e`/`I`/`q`. Modeless (`+`) channels reject MODE mutations. |
| TOPIC | §3.2.4 | 332/333 + RPL_TOPICWHOTIME |
| NAMES, LIST, WHO | §3.2.5-6, §3.6 | +s/+p hiding for non-members; `+a` channels return synthetic anonymous entries. WHO supports glob masks. |
| WHOIS, WHOWAS | §3.6.2-3 | WHOIS honours remote home server and reports time-since-last-PRIVMSG. WHOWAS uses an in-memory ring. |
| INVITE, KICK | §3.2.7-8 | Op required where applicable. |
| PRIVMSG, NOTICE | §3.3 | Federation-aware delivery + AWAY echo. Operator-only `$servermask` and `#hostmask` broadcast forms. |
| AWAY | §4.1 | 305/306 confirmations + 301 echo on PRIVMSG. |
| USERHOST, ISON | §4.8-9 | Bulk lookups. |
| LUSERS, VERSION, TIME, ADMIN, INFO, MOTD | §3.4.1-3, §4.5 | Stat-query family. |
| SUMMON, USERS | §4.5-6 | Disabled stubs returning 445 / 446 per the RFC. |
| WALLOPS | §4.7 | Operator-only +w broadcast, federated. |
| STATS | §3.4.4 | l/m/o/u, operator-only. |
| LINKS | §3.4.5 | Local view, open to all. |
| TRACE | §3.4.8 | Operator-only, local view. |
| SQUIT | §3.4.6 | Operator-only, drives the federation recovery flow. |
| CONNECT | §3.4.7 | Operator-only, requires a wired `Connector` host hook. |
| REHASH, DIE, RESTART | §4.2-4 | Operator-only, host hooks. REHASH drives the same reloader the SIGHUP loop uses. |
| OPER, KILL | §3.1.4, §3.7.1 | Store-backed operator accounts. |
| SERVICE | §3.1.6 | Service registration form. Creates a `state.User` with Service=true. Reserved fields ignored per the RFC. |
| SQUERY | §3.3.3 | Service-targeted message variant of PRIVMSG. Returns 408 if the target is not a registered service, even if a regular user with that nick exists. |
| SERVLIST | §3.5.1 | Lists registered services as 234 RPL_SERVLIST + 235 RPL_SERVLISTEND. Optional `<mask>` and `<type>` filters. |
| PING, PONG | §3.7.2-3 | Read-loop ping driver + client-issued PING. |

## Encoder canonical form

Our encoder always emits the *last* parameter of every message in
the trailing form (with a leading colon), even when the parameter
is a single token without spaces. So `004 alice irc.example.org
ircat-0.0.1 iow biklmnopstv` is encoded as `:irc.example.org 004
alice irc.example.org ircat-0.0.1 iow :biklmnopstv`.

This is RFC-conformant per RFC 2812 §2.3.1 (the trailing form is
allowed for any parameter, not just ones containing spaces) and
matches what most production ircds do, but it is unusual on certain
numerics — 004 in particular is conventionally rendered with the
mode-list as a middle parameter. Real clients (irssi, weechat,
hexchat, mIRC) accept either form.

We chose this approach to dodge the "is this a middle or a trailing"
ambiguity for parsers that rely on the colon as a rest-of-line
marker, and to keep the encoder simple — there is no special-casing
for empty params, embedded colons, or commands that traditionally
prefer the bare form. If a specific client compatibility issue
forces the change, the right move is to add an explicit
"force-middle for last param" flag on Message rather than reverting
to per-byte heuristics.

## Federation handshake

Each link follows the RFC 2813 §4.1.1-§4.1.3 sequence in strict
order. The dispatcher will not transition the link out of
`Handshaking` state until every step has succeeded:

1. **PASS** — `<password> <version> <flags> <description>`. The
   `version` and `flags` fields (RFC 2813 §4.1.1) are parsed and
   stashed on the conn even though we do not yet act on them; the
   federation Link will need them when the v2.0 capability work
   ships.
2. **SERVER** — `<name> <hopcount> <token> :<info>`. The peer name
   is matched against the configured `PeerName` if any.
3. **SVINFO** — `<TS_VERSION> <TS_MIN> 0 :<unix-ts>`. We send TS6
   on both fields and refuse any peer offering a TS version below
   3. The receive of SVINFO is what triggers the burst — both
   sides must have exchanged it before any state crosses the link.
4. **Burst** — Servers → Users → Channels (RFC 2813 §5.2 burst
   order). For each channel we emit one JOIN per local member, the
   topic, and a MODE line carrying the full mode word.

A failure at any step closes the link cleanly; partial state is
never accepted. Loss of the SVINFO step in particular is the gate
that prevents an incompatible peer from poisoning the local World.

## Open interpretation points

- **`LIST` during netsplit** — we freeze the list for the duration of the burst. Decision pending e2e testing.
- **`WHOIS` across federation** — we answer from the local World, which is the union of every node's burst plus runtime announces. The 312 line reports the target's `HomeServer`, the operator flag (313) is honoured for remote `+o` users, and 317 reports zero idle for remote users because we cannot see their per-message activity. No request is routed across the link; the timeout/synthetic-318 design is deferred until per-conn idle tracking lands on remote nodes too.
- **Nickname collisions on link** — TS-based resolution. Older TS wins. If TS is identical, both nicks are killed (RFC 2813 §5.2.1).
- **Trailing parameter with no leading `:`** — some clients omit the `:` when the trailing has no spaces. We accept on input; always emit with `:` for safety.

Log any new interpretation decision here with a date.
