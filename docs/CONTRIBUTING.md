# Contributing

Whether you are a person or an agent, the workflow is the same.

## Before you start

- Read [`CLAUDE.md`](../CLAUDE.md) for the hard rules: stdlib-first,
  single binary, no web framework, no ORM, etc.
- Read [`PLAN.md`](PLAN.md) to find the current milestone and what is
  in/out of scope for it.
- Read [`ARCHITECTURE.md`](ARCHITECTURE.md) for the module layout and
  the interfaces between packages.

## The work loop

1. Make the change.
2. Run the local checks (see below).
3. Update the relevant doc in `docs/` if behaviour changed.
4. Commit using the [commit convention](#commit-message-convention).
5. Open a PR (or push to your branch); CI runs the same checks plus
   `staticcheck`.

## Local checks

```sh
go test -race -count=1 ./...
go vet ./...
gofmt -l . | tee /dev/stderr | (! grep -q .)
```

The whole suite (unit + e2e) should run in under 15 seconds on a
modern laptop. If your change makes it noticeably slower, that is
worth a sentence in the PR description.

## Commit message convention

Every commit subject line follows
[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <imperative summary, lowercase, no trailing period>
```

### Types

| type       | use for                                                     |
|------------|-------------------------------------------------------------|
| `feat`     | a new user-visible capability                               |
| `fix`      | a bug fix                                                   |
| `docs`     | documentation only                                          |
| `test`     | adding or updating tests                                    |
| `refactor` | code change that is neither a feature nor a fix             |
| `perf`     | performance work backed by a benchmark                      |
| `chore`    | repo plumbing, scaffolding, dep bumps, anything else        |
| `build`    | Dockerfiles, compose stacks, build scripts                  |
| `ci`       | GitHub Actions / pipeline config                            |
| `style`    | pure formatting (rare; gofmt usually catches it before)     |

### Scopes

`<scope>` is optional but encouraged when one package or layer is the
obvious subject of the change. Use the package name (not the
directory path):

| scope          | covers                                          |
|----------------|-------------------------------------------------|
| `protocol`     | `internal/protocol`                             |
| `state`        | `internal/state`                                |
| `server`       | `internal/server`                               |
| `config`       | `internal/config`                               |
| `logging`      | `internal/logging`                              |
| `cmd`          | `cmd/ircat`                                     |
| `storage`      | `internal/storage` (and its drivers)            |
| `dashboard`    | `internal/dashboard`                            |
| `api`          | `internal/api`                                  |
| `bots`         | `internal/bots`                                 |
| `events`       | `internal/events`                               |
| `federation`   | `internal/federation`                           |
| `auth`         | `internal/auth`                                 |
| `e2e`          | `tests/e2e`                                     |
| `devcontainer` | `.devcontainer/`                                |
| `docker`       | `docker/` and the Dockerfiles                   |
| `compose`      | `docker-compose.yml` / `docker-compose.dev.yml` |
| `ci`           | `.github/workflows/`                            |
| `dev`          | dev-loop convenience: `.air.toml`, `.env.example`, dev-only compose tweaks |
| `prod`         | production-only fixes that are not isolated to one package |

### Subject rules

- Imperative mood, lowercase after the colon ("add", not "added"
  or "Adds").
- No trailing period.
- Aim for under 72 characters.
- One commit, one type. If a change is genuinely both a `feat` and a
  `fix`, split it. If splitting is impractical, pick the dominant
  type and call out the secondary work in the body.

### Body rules

- Explain the *why* before the *what*.
- Bullet lists are fine when they aid scanning; prose is fine when
  the change is straightforward.
- Hard-wrap around 72 columns.
- Agent-authored commits keep the
  `Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>`
  trailer.

### Examples

Real commits from this repo's history:

```
feat(server): tcp listener, registration state machine, ping/pong
feat(protocol): rfc 1459/2812 message parser and encoder
feat(state): rfc 1459 case mapping and world user index
fix(dev): env-overridable host ports, buildvcs off in air, config path via env
fix: teeHandler clone return, yaml tab rejection, dispatch test contract
test(e2e): spawn-the-binary registration tests and ircclient helper
docs: add architecture, plan, protocol, federation design
chore(devcontainer): set up go 1.26 toolchain with staticcheck, air, sqlite, psql
build(docker): multi-stage production build and dev hot-reload image
ci: add github actions workflow for fmt, vet, test, build, staticcheck
```

### Anti-examples

Do **not** use bare directory prefixes — they are not Conventional
Commits types and break the convention:

```
docker: ...               -> build(docker): ...
internal/server: ...      -> feat(server): ...
m0 review: ...            -> fix(prod): ... (m0 review)
```

### Why we care

Conventional Commits gives:

- Greppable history (`git log --grep '^feat'`).
- A stable input for changelog automation when M8 lands releases.
- Implicit semver hints (`feat` -> minor, `fix` -> patch, `feat!` /
  `BREAKING CHANGE:` -> major).
- A fast review of "what kind of change is this?" without opening
  the diff.

## Adding a new dependency

Don't, unless you really have to. If you do, update
[`ARCHITECTURE.md`](ARCHITECTURE.md#dependencies) in the same PR with
a one-paragraph justification: what does it give us, why isn't the
stdlib enough, what is the maintenance and binary-size cost.

## Adding a new commit scope

If your change introduces a new package or layer that doesn't fit
any of the scopes above, add a row to the [scopes table](#scopes) in
this file as part of the same PR. Keeping the table comprehensive is
the main maintenance burden of this convention.
