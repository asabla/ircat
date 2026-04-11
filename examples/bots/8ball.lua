-- 8ball.lua — magic 8-ball.
--
-- `!8ball will it rain?` → random answer from the table below.
-- Shows how on_command receives the command name in e.name and
-- everything after it in e.args (empty string if no args).

local answers = {
  "It is certain.",
  "Without a doubt.",
  "Yes, definitely.",
  "You may rely on it.",
  "As I see it, yes.",
  "Most likely.",
  "Outlook good.",
  "Yes.",
  "Signs point to yes.",
  "Reply hazy, try again.",
  "Ask again later.",
  "Better not tell you now.",
  "Cannot predict now.",
  "Concentrate and ask again.",
  "Don't count on it.",
  "My reply is no.",
  "My sources say no.",
  "Outlook not so good.",
  "Very doubtful.",
}

function init(ctx)
  math.randomseed(ctx:now())
  ctx:join("#fun")
end

function on_command(ctx, e)
  if e.name ~= "8ball" then
    return
  end
  if e.args == "" then
    ctx:say(e.channel, e.sender .. ": ask a question, e.g. !8ball will it rain?")
    return
  end
  local answer = answers[math.random(#answers)]
  ctx:say(e.channel, e.sender .. ": " .. answer)
end
