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
  ctx:log("hello")
  ctx:join("#test")
end

-- Called for every PRIVMSG/NOTICE in a channel the bot has joined.
function on_message(ctx, event)
  -- event.channel, event.sender, event.text, event.tags
  if event.text == "!ping" then
    ctx:say(event.channel, "pong")
  end
end

function on_join(ctx, event)    end  -- event.channel, event.user
function on_part(ctx, event)    end  -- event.channel, event.user, event.reason
function on_command(ctx, event) end  -- event.name ("ping" for "!ping"), event.args, event.channel, event.sender
function on_tick(ctx)           end  -- periodic; interval configured per bot

function shutdown(ctx) end  -- called on disable/reload
```

## The `ctx` object

| Method | Purpose |
|--------|---------|
| `ctx:say(target, text)` | PRIVMSG |
| `ctx:notice(target, text)` | NOTICE |
| `ctx:join(channel)` | Join a channel |
| `ctx:part(channel, reason?)` | Leave a channel |
| `ctx:nick()` | Bot's current nick |
| `ctx:channels()` | List of channels the bot is in |
| `ctx:kv_get(key)` | Read from per-bot KV store |
| `ctx:kv_set(key, value)` | Write to per-bot KV store (string → string) |
| `ctx:kv_delete(key)` | Delete a KV entry |
| `ctx:log(level, msg)` | Log to bot logs (visible in dashboard) |
| `ctx:now()` | Unix timestamp |
| `ctx:http_get(url)` | Vetted HTTP GET (allow-listed by config, rate-limited) |

No raw socket, no filesystem, no shell.

## Persistence

- Source + settings: `bots` table in the configured store.
- KV: `bot_kv` table, keyed by `(bot_id, key)`.
- Logs: in-memory ring buffer per bot, 1000 lines. Optionally mirrored to an event sink.

## Scheduling

`on_tick` is driven by a per-bot ticker. Interval is set per bot (min 1s, default 60s). Ticks that overrun are dropped, not queued.

## Example

```lua
-- examples/bots/8ball.lua
local answers = {
  "It is certain.", "Ask again later.", "Outlook not so good.",
  "Cannot predict now.", "Without a doubt.", "Very doubtful.",
}

function init(ctx)
  ctx:join("#fun")
end

function on_command(ctx, e)
  if e.name == "8ball" then
    math.randomseed(ctx:now())
    ctx:say(e.channel, e.sender .. ": " .. answers[math.random(#answers)])
  end
end
```

## Security caveats

The sandbox is defense-in-depth. It is **not** a boundary that makes it safe to let untrusted third parties upload Lua. Only grant bot-edit permissions to operators you trust. An attacker with Lua access but without sandbox escapes can still spam, flood, or pivot via outbound HTTP if it's enabled.

## Testing

- Unit tests in `internal/bots` exercise the sandbox (e.g., ensure `os.execute` errors out).
- Integration test spins up a real server, registers the example 8ball bot, triggers `!8ball`, asserts on the reply.
