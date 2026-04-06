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
| `+k <key>` | yes (set) | Channel key |
| `+l <n>` | yes (set) | User limit |
| `+i` | no | Invite-only |
| `+m` | no | Moderated (only +v/+o can speak) |
| `+n` | no | No external messages |
| `+p` | no | Private (hidden from `LIST` to non-members) |
| `+s` | no | Secret (hidden from `LIST` and `WHOIS`) |
| `+t` | no | Topic settable only by ops |

`MODE #chan +o-v alice bob` must be supported (batched). Max modes per MODE command: advertise `MODES=4` in ISUPPORT.

### User modes (v1)

| Mode | Meaning |
|------|---------|
| `+i` | Invisible — hidden from `WHO`/`NAMES` except to channel members |
| `+w` | Receives wallops |
| `+o` | IRC operator (set by OPER, never by user) |
| `+s` | Receives server notices |

## Numerics we emit

Tracked in `internal/protocol/numeric.go`. At minimum: 001–005, 221, 251–255, 263, 301, 305, 306, 311–319, 321–323, 324, 331–333, 341, 346, 347, 348, 349, 351, 352, 353, 366, 372, 375, 376, 381, 391, 401–404, 406, 407, 409, 411, 412, 421, 422, 431, 432, 433, 436, 441, 442, 443, 451, 461, 462, 464, 465, 471, 472, 473, 474, 475, 476, 481, 482, 483, 491, 501, 502.

## Open interpretation points

- **`LIST` during netsplit** — we freeze the list for the duration of the burst. Decision pending e2e testing.
- **`WHOIS` across federation** — RFC 2812 routes it to the target's server. We do the same, with a timeout; if no reply in 10s we synthesize a `318 RPL_ENDOFWHOIS`.
- **Nickname collisions on link** — TS-based resolution. Older TS wins. If TS is identical, both nicks are killed (RFC 2813 §5.2.1).
- **Trailing parameter with no leading `:`** — some clients omit the `:` when the trailing has no spaces. We accept on input; always emit with `:` for safety.

Log any new interpretation decision here with a date.
