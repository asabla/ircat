-- greeter.lua — welcome first-time visitors to a channel.
--
-- On first join ever, the bot says hello and records the nick in
-- its per-bot KV store. Subsequent joins are silent. Shows how to
-- use on_join, ctx:kv_get / ctx:kv_set for persistent state across
-- reloads, and how to silently ignore KV "not found" errors.

function init(ctx)
  ctx:join("#lobby")
end

function on_join(ctx, e)
  if e.sender == ctx:nick() then
    return  -- don't greet ourselves
  end

  local key = "seen:" .. e.channel .. ":" .. e.sender
  local seen = ctx:kv_get(key)
  if seen ~= nil and seen ~= "" then
    return
  end

  ctx:say(e.channel, "Welcome to " .. e.channel .. ", " .. e.sender .. "!")
  ctx:kv_set(key, tostring(ctx:now()))
end
