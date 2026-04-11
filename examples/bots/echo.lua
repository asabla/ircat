-- echo.lua — mirror every channel message back with attribution.
--
-- Shows how on_message fires for every PRIVMSG in channels the
-- bot is in, and how to read event.sender / event.text. Skips
-- messages the bot itself sent so it doesn't echo itself in a
-- loop.

function init(ctx)
  ctx:join("#echo")
end

function on_message(ctx, e)
  if e.sender == ctx:nick() then
    return
  end
  ctx:say(e.channel, "<" .. e.sender .. "> " .. e.text)
end
