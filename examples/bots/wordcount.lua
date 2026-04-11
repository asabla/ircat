-- wordcount.lua — count messages per user, report on demand.
--
-- Increments a per-(channel,nick) counter on every message, and
-- responds to `!stats [nick]` with how many messages that nick
-- has sent in the current channel. Without an argument, reports
-- the caller's own count.
--
-- Shows: persistent counters in the KV store, mixing on_message
-- and on_command in a single bot, reading e.args to make a
-- command argument optional.

function init(ctx)
  ctx:join("#chat")
end

local function key_for(channel, nick)
  return "count:" .. channel .. ":" .. nick
end

local function read_count(ctx, channel, nick)
  local v = ctx:kv_get(key_for(channel, nick))
  if v == nil or v == "" then
    return 0
  end
  return tonumber(v) or 0
end

function on_message(ctx, e)
  if e.sender == ctx:nick() then
    return
  end
  -- Don't count commands themselves toward the message total.
  if e.text:sub(1, 1) == "!" then
    return
  end
  local n = read_count(ctx, e.channel, e.sender) + 1
  ctx:kv_set(key_for(e.channel, e.sender), tostring(n))
end

function on_command(ctx, e)
  if e.name ~= "stats" then
    return
  end
  local target = e.args
  if target == "" then
    target = e.sender
  end
  local n = read_count(ctx, e.channel, target)
  ctx:say(e.channel, target .. " has sent " .. tostring(n) .. " messages in " .. e.channel)
end
