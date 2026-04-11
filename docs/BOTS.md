# Lua Bots

ircat runs user-defined Lua bots inside the server process. Bots see IRC traffic and can act on it without needing a separate TCP connection.

## Runtime

- **Engine:** gopher-lua (pure-Go Lua 5.1 implementation).
- **Isolation:** each bot runs in its own goroutine with a dedicated `*lua.LState`. States are never shared.
- **Sandbox:**
  - Removed from the global env: `io`, `os`, `debug`, `package.loadlib`, `package.loaders`, `dofile`, `loadfile`, `require` (replaced with a whitelisted loader).
  - `string`, `table`, `math` are kept.
  - A `ctx` global is injected per event invocation.
- **Budgets:**
  - Instruction count: enforced via `SetContext` + hook, default 1,000,000 per event.
  - Memory: soft limit via gopher-lua's `RegistrySize` and periodic GC.
  - Wall clock: event handlers run with `context.WithTimeout(5s)`. Exceeding cancels the bot and logs an error.

## Lifecycle

1. Operator creates a bot via dashboard or API with a Lua source blob and a name.
2. The supervisor compiles the script (`ctx = lua.NewState(); ctx.DoString(source)`). Compilation errors are reported back and the bot is marked `errored`.
3. If compilation succeeds, the bot's `init` function (if present) is called once.
4. Events are dispatched to the bot as they happen on channels it participates in.
5. On source update, the bot is stopped, a new state is built, and `init` runs again.
6. On disable, the state is discarded.

## Event API

Each of these is a top-level function the script may define:

```lua
-- Called once on load/reload.
function init(ctx)
  ctx:log("info", "hello")
  ctx:join("#test")
end

-- Called for every PRIVMSG in a channel the bot has joined.
function on_message(ctx, event)
  -- event.channel, event.sender, event.hostmask, event.text
  if event.text == "!ping" then
    ctx:say(event.channel, "pong")
  end
end

-- Join and part events deliver the acting nick as event.sender.
function on_join(ctx, event)    end  -- event.channel, event.sender, event.hostmask
function on_part(ctx, event)    end  -- event.channel, event.sender, event.hostmask, event.text (part reason)
function on_command(ctx, event) end  -- event.name ("ping" for "!ping"), event.args (string), event.channel, event.sender
function on_tick(ctx)           end  -- periodic; interval configured per bot

function shutdown(ctx) end  -- called on disable/reload
```

`event.args` on `on_command` is a single string — everything after the command name. Split it with `string.match` or `string.gmatch` yourself if you need individual words.

## The `ctx` object

| Method | Purpose |
|--------|---------|
| `ctx:say(target, text)` | PRIVMSG |
| `ctx:notice(target, text)` | NOTICE |
| `ctx:join(channel)` | Join a channel |
| `ctx:part(channel, reason?)` | Leave a channel |
| `ctx:nick()` | Bot's current nick |
| `ctx:kv_get(key)` | Read from per-bot KV store; returns `nil` if unset |
| `ctx:kv_set(key, value)` | Write to per-bot KV store (string → string) |
| `ctx:kv_delete(key)` | Delete a KV entry |
| `ctx:log(level, msg)` | Log to bot logs (visible in dashboard); level is a string like `"info"` or `"error"` |
| `ctx:now()` | Unix timestamp (seconds) |

No raw socket, no filesystem, no shell. `ctx:channels()` and `ctx:http_get()` are **not** currently wired — track joined channels in a local Lua table if you need that, and don't try to call outbound HTTP yet.

## Persistence

- Source + settings: `bots` table in the configured store.
- KV: `bot_kv` table, keyed by `(bot_id, key)`.
- Logs: in-memory ring buffer per bot, 1000 lines. Optionally mirrored to an event sink.

## Scheduling

`on_tick` is driven by a per-bot ticker. Interval is set per bot (min 1s, default 60s). Ticks that overrun are dropped, not queued.

## Examples

Working examples live in [`examples/bots/`](../examples/bots/) — each file is a self-contained, copy-paste-ready bot:

- [`ping.lua`](../examples/bots/ping.lua) — smallest possible bot, replies `pong` to `!ping`.
- [`echo.lua`](../examples/bots/echo.lua) — mirrors channel messages with attribution, showing `on_message` and self-echo avoidance.
- [`8ball.lua`](../examples/bots/8ball.lua) — `!8ball <question>`, demonstrates `event.args` and `math.random`.
- [`greeter.lua`](../examples/bots/greeter.lua) — welcomes first-time joiners, uses the KV store for persistent state.
- [`wordcount.lua`](../examples/bots/wordcount.lua) — message counter per nick with a `!stats` command, mixes `on_message` and `on_command`.

See [`examples/bots/README.md`](../examples/bots/README.md) for installation instructions (dashboard and API).

## Security caveats

The sandbox is defense-in-depth. It is **not** a boundary that makes it safe to let untrusted third parties upload Lua. Only grant bot-edit permissions to operators you trust. An attacker with Lua access but without sandbox escapes can still spam, flood, or pivot via outbound HTTP if it's enabled.

## Testing

- Unit tests in `internal/bots` exercise the sandbox (e.g., ensure `os.execute` errors out).
- Integration test spins up a real server, registers the example 8ball bot, triggers `!8ball`, asserts on the reply.
