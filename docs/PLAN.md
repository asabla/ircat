# PLAN — ircat v1.1.0

The v1.0.0 release shipped a feature-complete IRC server: full
client surface (RFC 1459/2812), persistent operators and channels,
htmx dashboard, admin API, sandboxed Lua bots, jsonl + webhook
event sinks, two-node federation with TLS, and a hardened
production compose stack. See [`PLAN-v1.0.md`](PLAN-v1.0.md) for
the historical record of M0 → M8.

v1.1.0 picks up the items that were explicitly deferred from v1.0
plus the polish work that the v1.0 audit surfaced. The cuts are
deliberately small — v1.1 is a hardening + polish release, not a
new-feature release. New surfaces (IRCv3 caps beyond CAP END,
SERVICE pseudo-server, full SQUIT recovery) live in v1.2+.

## Theme

> "Make the v1.0 surface boring to operate at scale, and close the
> obvious follow-ups from the v1.0 federation MVP."

## Milestones

### M9 — Federation hardening

**Goal:** the federation transport stops being "MVP" and starts
being production.

Shipped:
- Channel mode burst on link-up (TOPIC + canonical mode word +
  per-member +o/+v) and runtime MODE re-application on the
  receiver via a new `applyRemoteChannelMode` helper that
  forwards each toggle to the existing `state.Channel` setters.
- SQUIT recovery on link drop and remote SQUIT propagation:
  `Server.HandleSquit` walks the world, removes every user
  homed on the dropped peer, fans synthetic QUITs to local
  channel members, and forwards SQUIT to remaining links.
- Subscription-aware channel routing with the v1.0 fanout
  available behind `federation.broadcast_mode`. Subscriptions
  are tracked explicitly via `Server.SubscribePeerToChannel`,
  populated by `sendBurst` (when we tell a peer about a
  channel) and by `handleRemoteJoin` (when a peer tells us
  about one). JOIN remains fanned out so it can establish new
  subscriptions.
- KILL routing across links: local operator KILL forwards over
  every link; receivers fan a synthetic QUIT and either
  disconnect the local conn via the new `Host.DropLocalUser`
  hook or drop the remote-user record. KILL is one-shot per
  node so >2-node meshes do not loop.
- TS-based nick collision resolution per RFC 2813. Burst NICK
  shape carries TS as a positional param (with backward-
  compatible v1.0 sniffing); incoming-lower wins, incoming-
  higher gets KILL'd back to the peer. Channel JOIN burst lines
  carry channel TS and `AdoptOlderTS` lowers the local anchor.

Deferred to v1.2:
- Equal-TS nick collision (RFC says kill both — current code
  keeps the existing record on equal TS, which is conservative
  but rare at nanosecond resolution).
- Channel TS collision *behaviour* — v1.1 propagates the TS but
  does not yet drop op state on a peer's older claim. The next
  cycle wires AdoptOlderTS into the membership reset path.
- Ban list (+b) propagation across links.

**Exit:** every shipped item above is in main with at least one
integration test exercising it through two real Server
instances. ✅

---

### M10 — Lua sandbox tightening

**Goal:** stop relying on the wallclock + container memory cap as
the sandbox's only guard rails.

Shipped:
- **Per-call instruction budget via the existing wallclock.**
  Audit discovered that gopher-lua's VM dispatch loop already
  checks `L.ctx.Done()` between every Lua bytecode instruction,
  so the wallclock budget already enforces at instruction
  granularity — there is no separate hook API to attach. The
  `Budget.Instructions` field maps to a wallclock proxy at a
  conservative 10M-instructions/second rate via
  `Runtime.effectiveDeadline`, taking the lower of the two as
  the actual ceiling. Five new tests cover the four runaway
  vectors (tight loop, pcall-shielded loop, recursion, doubling
  string concat) and the instruction-proxy mapping.
- **Per-bot registry cap as a partial memory ceiling.**
  `Budget.RegistrySlots` wires through to gopher-lua's
  `RegistryMaxSize`, capping the data stack at a configurable
  number of LValue slots (default 65536 ≈ 1 MiB). When a
  script grows past the cap gopher-lua raises a Lua error
  which the runtime turns into a clean handler exit. Partial
  rather than a true allocator hook (gopher-lua doesn't expose
  one), so a script can still allocate large strings or
  userdata outside the registry — the compose stack's 1 GiB
  process cap is the outer envelope.
- **`string.format` hardening.** The default gopher-lua
  `string.format` is replaced with `safeStringFormat` which
  refuses widths and precisions above 1024, refuses calls with
  more than 16 args, and clamps the rendered output to 8192
  bytes. Five tests cover each rejection path plus the happy
  path. Closes the
  `string.format("%999999999s", "x")` allocation vector.
- **Fuzz target.** `FuzzSandboxNeverPanics` drives random Lua
  source through compile + dispatch and asserts no panic, no
  budget overrun. Verified end-to-end with a 10s real fuzz run
  (369k executions, 215 new corpus entries, zero failures).
  Doubles as a regression test in CI via the seed corpus.

Deferred to v1.2:
- True per-allocation memory hook (waiting on a gopher-lua
  release that exposes one).
- Soak fuzz beyond 60s as part of the M11 nightly job.

---

### M11 — Operational validation

**Goal:** stop guessing about the operating envelope. Replace
"conservative defaults" with measured numbers.

Shipped:
- **Flood-control benchmark suite.** `internal/server/floodcontrol_bench_test.go`
  has three benchmark families: uncontended floor (~55 ns/op),
  N-senders-on-shared-bucket worst case (caps at ~200 ns at
  N=1000), and per-connection production model (~4 ns/op at
  N≥10 thanks to parallel scaling). Numbers and operator
  guidance in `docs/OPERATIONS.md`.
- **Storage backend benchmarks.** `internal/storage/sqlite/events_bench_test.go`
  and the matching Postgres file. The audit found that the
  default SQLite DSN was using WAL with implicit
  `synchronous=FULL`, which fsyncs every commit at ~8.4 ms.
  Switched the default to `synchronous=NORMAL` (the upstream-
  recommended WAL pairing), bringing serial Append down to
  ~183 µs and parallel Append to ~73 µs — a 46x speedup with
  no API change. Numbers in `docs/OPERATIONS.md`.
- **Federation latency benchmark.** `internal/server/federation_bench_test.go`
  brings up two real Server instances bridged via `net.Pipe`
  and measures the wall-clock between `cAlice.Write` and the
  matching read on `cBob`. Reports per-message latency plus
  p50 / p99 metrics. Reference numbers (~38 µs mean / 36 µs
  p50 / 89 µs p99) in `docs/FEDERATION.md`.
- **Soak test harness.** `tests/soak/` Go binary using the
  existing `ircclient` helper. Per-conn sender + drain
  reader; reports sent / received / drops / rate; exits
  non-zero when the drop rate exceeds the configurable
  threshold (default 1%). Smoke test with `go run ./tests/soak`
  takes < 10s on a dev box; the v1.1 reference soak (10k conns
  / 1k channels / 24h) is documented in `docs/OPERATIONS.md`.

The required helper refactor moved `dialClient`, `expectNumeric`,
`readUntil`, and `linkTwoServers` from `*testing.T` to
`testing.TB` so the federation latency benchmark could share
them with the integration tests. Production code is unchanged.

Deferred to v1.2:
- A 24h reference soak run on a dedicated box and a CI nightly
  job that gates merges on its result. The harness exists; the
  schedule is the missing piece.
- A storage benchmark against a real Postgres on tuned hardware
  (the existing bench Skips cleanly without `IRCAT_TEST_POSTGRES_DSN`).

---

### M12 — Polish & release plumbing

**Goal:** clean up everything that v1.0 left scuffed.

Shipped:
- **Dashboard in-process TLS termination.** Wires the existing
  `dashboard.tls.{cert_file,key_file}` fields so the operator
  can opt out of the reverse-proxy layer. Fail-closed on a
  misconfigured cert path. Both the proxy and in-process modes
  remain supported.
- **SIGHUP reload + `POST /api/v1/config/reload`.**
  `internal/logging` gains `NewWithLevel` returning a
  `*slog.LevelVar` so the cmd path can flip the level at
  runtime. The reload entry point lives in `cmd/ircat/reload.go`
  and rewires `logging.level`, statically configured operators,
  and the MOTD file. The api package gains a small `Reloader`
  interface that the cmd path satisfies, plus a bearer-token
  POST handler that runs through the same code path SIGHUP
  triggers. A misconfigured reload leaves the previous state
  intact and surfaces the parse error in both the response body
  and the audit log.
- **Migration guide.** `docs/UPGRADE-v1.0-to-v1.1.md` walks
  through every change a v1.0 operator will see, organised by
  what changes automatically vs what they can opt into. The
  headline message: in-place upgrade, no config changes
  required, no migrations.
- **Release plumbing.** `.goreleaser.yaml` drives the cross-
  platform build (linux + darwin, amd64 + arm64), the multi-
  arch container image push to ghcr.io, syft SBOM generation,
  and cosign keyless signing of both the checksums.txt blob
  and the container manifest. `scripts/install.sh` is a
  POSIX-sh bootstrap that resolves the latest tag from the
  GitHub API, verifies the SHA-256 against checksums.txt, and
  installs the binary; optional cosign signature verification
  via `IRCAT_VERIFY=1`. `.github/workflows/release.yml` runs
  goreleaser on every `v*` tag with the qemu/buildx/cosign
  action chain and uses GitHub OIDC for keyless signing so no
  key material lives in the repo. A follow-up install-smoke
  job runs the just-published install.sh inside an alpine
  container so a broken release script surfaces immediately.

**Exit:** `git tag v1.1.0` cuts a release whose pipeline
produces signed cross-platform archives, a multi-arch container
image on ghcr.io, syft SBOMs, and a cosign keyless signature
over checksums.txt and the container manifest. ✅

---

## Cross-cutting

- Every commit still follows Conventional Commits — see
  `CLAUDE.md` for the type table.
- Every new feature still ships with at least one test, and
  every cross-API surface still ships with an e2e test.
- `docs/PROTOCOL.md` continues to absorb RFC ambiguities as
  they come up.
- `docs/CONFIG.md` is updated in the same commit as any new
  config field.

## Out of scope for v1.1

These were considered and explicitly cut. They live in a future
v1.2+ plan.

- IRCv3 capabilities beyond `CAP END` (`message-tags`,
  `account-tag`, `chghost`, ...).
- SERVICE pseudo-server.
- Multi-DC federation routing tables.
- Webhook v2 event payload schema (the v1 jsonl payload stays
  compatible for the whole 1.x line).
- Operator account federation (operator records are still
  per-node).
