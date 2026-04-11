# Example Lua bots

These are ready-to-run bots you can copy into the dashboard or POST to the
admin API. Each is self-contained, uses only wired `ctx` methods, and is a
starting point — not a final product.

See [`../../docs/BOTS.md`](../../docs/BOTS.md) for the full runtime reference.

## Catalogue

| File | What it does | Teaches |
|------|--------------|---------|
| [`ping.lua`](ping.lua) | Replies `pong` to `!ping` | Smallest possible bot: `init`, `on_command`, `ctx:say` |
| [`echo.lua`](echo.lua) | Mirrors every channel message | `on_message`, reading `event.sender` / `event.text`, avoiding self-echo with `ctx:nick()` |
| [`8ball.lua`](8ball.lua) | Magic 8-ball: `!8ball <question>` | Command arguments via `event.args`, `math.random` |
| [`greeter.lua`](greeter.lua) | Welcomes first-time joiners | `on_join`, persistent state via `ctx:kv_get` / `ctx:kv_set` |
| [`wordcount.lua`](wordcount.lua) | Counts messages per nick, `!stats [nick]` | Mixing `on_message` and `on_command`, numeric counters in KV |

## Installing an example

### Via the dashboard

1. Open `/dashboard/bots` and click **New bot**.
2. Give it a name (e.g. `ping`) and paste the file's contents into the source box.
3. Click **Validate** to confirm the script parses, then **Save**.
4. Toggle the bot **enabled** — `init` runs and `ctx:join` fires.

### Via the admin API

```bash
curl -X POST http://localhost:8080/api/v1/bots \
  -H "Authorization: Bearer $IRCAT_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$(jq -Rn --rawfile src examples/bots/ping.lua \
        '{name: "ping", source: $src, enabled: true}')"
```

## Editing tips

- Hit **Validate** before **Save** — the validate endpoint runs the script
  through the Lua parser without touching storage, so a typo won't clobber
  a running bot.
- Watch the **Logs** pane on the bot detail page; `ctx:log("info", "...")`
  shows up there in real time over SSE.
- KV entries are namespaced per bot. Two bots with the same key do not
  collide. If you want to reset state during development, delete the bot
  and recreate it — that wipes its KV rows.

## What examples deliberately don't cover

- **Outbound HTTP.** `ctx:http_get` is referenced in older notes but is
  not wired up yet. Don't use it.
- **Listing channels.** `ctx:channels()` is not implemented. Track channels
  you've joined in a local Lua table if you need to.
- **Timers other than `on_tick`.** There is no `setTimeout`. Use a single
  `on_tick` handler with a configured interval and do the bookkeeping
  yourself.
