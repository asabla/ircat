# AGENTS.md

This file documents the "agents" that live inside and around ircat. There are two distinct meanings of *agent* in this project — don't confuse them.

## 1. Lua bots (in-server agents)

ircat ships a Lua runtime for user-defined bots that participate in IRC channels.

- **Runtime:** gopher-lua, sandboxed (no `io`, no `os`, no raw `require` beyond a vetted loader).
- **Lifecycle:** bots are registered via the admin API or dashboard, stored in the configured database, and started/stopped by the bot supervisor.
- **Event API (planned):**
  - `on_message(ctx, channel, sender, text)`
  - `on_join(ctx, channel, user)`
  - `on_part(ctx, channel, user, reason)`
  - `on_command(ctx, name, args)` — for `!name args` style commands
  - `on_tick(ctx)` — periodic, interval configured per-bot
- **Outputs:** `ctx:say(target, text)`, `ctx:notice(target, text)`, `ctx:join(channel)`, `ctx:part(channel)`, `ctx:kv_get(key)`, `ctx:kv_set(key, value)`.
- **Isolation:** each bot runs in its own goroutine with a bounded message queue and CPU/instruction budget. A misbehaving bot must not be able to stall the server.
- **Federation:** bots run on the server they are registered to and see messages from the entire federated network via normal IRC routing.

Full spec lives in [`docs/BOTS.md`](docs/BOTS.md).

## 2. Development agents (Claude Code, etc.)

This repo is designed to be friendly to coding agents. If you are such an agent, start here:

1. Read [`CLAUDE.md`](CLAUDE.md) — hard rules, conventions, what to avoid.
2. Read [`docs/PLAN.md`](docs/PLAN.md) — current milestone and exit criteria.
3. Read [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — module layout and interfaces.
4. When in doubt about protocol behaviour, consult [`docs/PROTOCOL.md`](docs/PROTOCOL.md) before guessing.

### Task shapes that work well

- "Implement command X per RFC 2812 §N" — scoped, testable, has a clear reference.
- "Add e2e test for scenario Y" — independently verifiable.
- "Wire storage driver Z behind the `Store` interface" — well-bounded.

### Task shapes to avoid

- "Refactor the state package" without a concrete goal.
- "Make it faster" without a benchmark.
- Cross-cutting changes that touch protocol + dashboard + storage in one PR — split them.

### Automation hooks

There are currently no CI bots in this repo. If you add one, document it here.
