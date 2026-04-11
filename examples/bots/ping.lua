-- ping.lua — the smallest useful bot.
--
-- Joins #test on load and replies "pong" to anyone who types "!ping".
-- Demonstrates: init, ctx:join, on_command, ctx:say.

function init(ctx)
  ctx:join("#test")
end

function on_command(ctx, e)
  if e.name == "ping" then
    ctx:say(e.channel, "pong")
  end
end
