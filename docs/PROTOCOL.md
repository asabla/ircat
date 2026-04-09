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

- Prefix `#` (network-wide) or `&` (server-local). Other prefixes (`+`, `!`) not implemented in v1.
- Max 50 characters (ISUPPORT `CHANNELLEN=50`).
- Cannot contain SPACE, BELL (`\x07`), `,`, or `:`.

## Modes

### Channel modes (v1)

| Mode | Takes param | Meaning |
|------|-------------|---------|
| `+o <nick>` | yes | Grant channel operator |
| `+v <nick>` | yes | Grant voice |
| `+b <mask>` | yes | Ban mask |
| `+e <mask>` | yes | Ban exception (overrides matching `+b`) |
| `+I <mask>` | yes | Invite exception (bypasses `+i`) |
| `+k <key>` | yes (set) | Channel key |
| `+l <n>` | yes (set) | User limit |
| `+i` | no | Invite-only |
| `+m` | no | Moderated (only +v/+o can speak) |
| `+n` | no | No external messages |
| `+p` | no | Private (hidden from `LIST` to non-members) |
| `+s` | no | Secret (hidden from `LIST` and `WHOIS`) |
| `+t` | no | Topic settable only by ops |

`+b`, `+e`, and `+I` are list-form: bare `MODE #chan +b` (or `+e` /
`+I`) returns the corresponding list via 367+368, 348+349, or
346+347. Match algorithm is the simple IRC glob (`*`, `?`,
case-insensitive under the world's case mapping).

`MODE #chan +o-v alice bob` must be supported (batched). Max modes per MODE command: advertise `MODES=4` in ISUPPORT.

### User modes (v1)

| Mode | Meaning |
|------|---------|
| `+i` | Invisible — hidden from `WHO`/`NAMES` except to channel members |
| `+w` | Receives wallops |
| `+o` | IRC operator (set by OPER, never by user) |
| `+s` | Receives server notices |

## Numerics we emit

Tracked in `internal/protocol/numeric.go`. At minimum: 001–005,
200–209, 211, 212, 219, 221, 242, 243, 251–255, 256–259, 262, 263,
301, 302, 303, 305, 306, 311–319, 321–323, 324, 329, 331–333, 341,
346, 347, 348, 349, 351, 352, 353, 364, 365, 366, 369, 371, 372,
374, 375, 376, 381, 391, 401–404, 406, 407, 409, 411, 412, 421,
422, 431, 432, 433, 436, 441, 442, 443, 451, 461, 462, 464, 465,
471, 472, 473, 474, 475, 476, 481, 482, 483, 491, 501, 502.

## Implemented commands

The dispatch table in `internal/server/handler.go` covers the
following RFC 2812 verbs (and where they live):

| Command | RFC | Notes |
|---------|-----|-------|
| PASS, NICK, USER | §3.1 | Registration burst |
| CAP | IRCv3 | LS/REQ/END negotiation, no caps advertised |
| QUIT | §3.1.7 | Broadcasts QUIT to shared-channel members |
| JOIN, PART | §3.2.1-2 | Including +i/+k/+l/+b/+e/+I gates |
| MODE | §3.2.3-4 | Channel + user, list-form `b`/`e`/`I` |
| TOPIC | §3.2.4 | 332/333 + RPL_TOPICWHOTIME |
| NAMES, LIST, WHO | §3.2.5-6, §3.6 | Standard replies + +s/+p hiding |
| WHOIS, WHOWAS | §3.6.2-3 | WHOIS honours remote home server; WHOWAS uses an in-memory ring |
| INVITE, KICK | §3.2.7-8 | Op required where applicable |
| PRIVMSG, NOTICE | §3.3 | Federation-aware delivery + AWAY echo |
| AWAY | §4.1 | 305/306 confirmations + 301 echo on PRIVMSG |
| USERHOST, ISON | §4.8-9 | Bulk lookups |
| LUSERS, VERSION, TIME, ADMIN, INFO, MOTD | §3.4.1-3, §4.5 | Stat-query family |
| WALLOPS | §4.7 | Operator-only +w broadcast, federated |
| STATS | §3.4.4 | l/m/o/u, operator-only |
| LINKS | §3.4.5 | Local view, open to all |
| TRACE | §3.4.8 | Operator-only, local view |
| SQUIT | §3.4.6 | Operator-only, drives existing recovery flow |
| CONNECT | §3.4.7 | Operator-only, requires wired Connector |
| REHASH, DIE, RESTART | §4.2-4 | Operator-only, host hooks |
| OPER, KILL | §3.1.4, §3.7.1 | Store-backed operator accounts |
| PING, PONG | §3.7.2-3 | Read-loop ping driver + client-issued PING |

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

## Open interpretation points

- **`LIST` during netsplit** — we freeze the list for the duration of the burst. Decision pending e2e testing.
- **`WHOIS` across federation** — we answer from the local World, which is the union of every node's burst plus runtime announces. The 312 line reports the target's `HomeServer`, the operator flag (313) is honoured for remote `+o` users, and 317 reports zero idle for remote users because we cannot see their per-message activity. No request is routed across the link; the timeout/synthetic-318 design is deferred until per-conn idle tracking lands on remote nodes too.
- **Nickname collisions on link** — TS-based resolution. Older TS wins. If TS is identical, both nicks are killed (RFC 2813 §5.2.1).
- **Trailing parameter with no leading `:`** — some clients omit the `:` when the trailing has no spaces. We accept on input; always emit with `:` for safety.

Log any new interpretation decision here with a date.
