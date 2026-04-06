# Dashboard

A built-in web UI for running and observing ircat. Designed to be useful without JavaScript frameworks, heavy build tooling, or a separate SPA.

## Stack

- **`net/http`** — routing and listeners.
- **`html/template`** — server-rendered HTML.
- **htmx** — progressive interactivity (swap fragments on click). Single vendored JS file in `internal/dashboard/static/htmx.min.js`.
- **Server-Sent Events** — live log tail, live chat, live event stream. No WebSockets.
- **Pico.css** (or equivalent classless CSS) — vendored, single file, no build step.

No npm, no webpack, no TypeScript, no React. Everything ships inside the binary via `embed.FS`.

## Pages

| Path | Purpose |
|------|---------|
| `/` | Redirect to `/dashboard` or `/login`. |
| `/login` | Username + password. Sets session cookie. |
| `/dashboard` | Overview: counts (users, channels, bots), uptime, link status, recent events. |
| `/dashboard/users` | Users table, search, kick/kill actions. |
| `/dashboard/channels` | Channels table, drill-down to members + mode editor. |
| `/dashboard/operators` | CRUD on operator accounts. |
| `/dashboard/tokens` | CRUD on API tokens. |
| `/dashboard/bots` | Bot list, inline Lua editor, enable toggle, log tail. |
| `/dashboard/events` | Audit log with filters. |
| `/dashboard/logs` | Live log tail (SSE). |
| `/dashboard/chat` | In-dashboard IRC client. |
| `/dashboard/settings` | View config. Editable subset is hot-reloadable. |
| `/dashboard/federation` | Link status, manual connect/squit. |

## Live updates

All live views are SSE endpoints under `/dashboard/sse/`:

- `/dashboard/sse/logs` — streams from the logging ring buffer.
- `/dashboard/sse/events` — streams audit events.
- `/dashboard/sse/chat/{channel}` — streams PRIVMSG/NOTICE/JOIN/PART for a channel (filtered to what the logged-in operator is allowed to see).
- `/dashboard/sse/bots/{id}/logs` — bot stdout.

Client-side htmx binds these via `hx-ext="sse"`.

## Chat page

Effectively an embedded IRC client. The dashboard opens an internal virtual connection on behalf of the logged-in operator — it does not go over TCP, it hooks straight into the server's message bus. This means the chat page works even if the IRC listener is firewalled off.

Features:
- Joined channels in a sidebar.
- Input box; htmx POST to `/dashboard/chat/send`.
- Backlog: last 200 messages per channel from the event store.
- Nick mentions highlighted.

## Settings page

Read-only rendering of the current config, with editable controls *only* for the hot-reloadable subset. Editing a non-reloadable field shows a "restart required" notice and offers to download a patched config file.

## Authentication

- Session cookie (`ircat_session`), HttpOnly, Secure, SameSite=Lax.
- CSRF: double-submit token on all POSTs. Stored in a meta tag, sent via `hx-headers`.
- Failed logins rate-limited: 5 attempts per minute per IP.
- Dashboard users are a superset of IRC operators by default; configurable.

## Accessibility

- Semantic HTML. Forms have labels. Tables have headers.
- Keyboard navigable.
- Dark mode follows `prefers-color-scheme`.

## Testing

- Template rendering: golden-file tests under `internal/dashboard/testdata/`.
- HTTP handlers: `httptest.NewServer` + table tests.
- SSE: assert on the first N events received within a timeout.
- Browser-level e2e is *not* in scope for v1. If we add it, use `chromedp` and keep it as an opt-in CI job.
