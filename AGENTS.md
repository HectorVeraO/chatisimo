# Chatisimo - Agent Rules

Chatisimo is a Discord bot platform written in Go. The first feature is a gift exchange
(draw who gives a gift to whom, on any date). Music, a web app and a mobile app come later.
This repository is **public**: read "Secrets" before touching any file.

## Architecture: modular monolith

- One binary: `cmd/chatisimo`. Modules talk through in-process Go interfaces. No message broker.
- Each module lives in `internal/<module>/`. Its root package is the module's **public API**
  (interfaces, request/response types, errors). Everything else goes under
  `internal/<module>/internal/`, so the Go compiler blocks other modules from reaching it.
- A module may import another module's root package only. Never its `internal/` packages,
  never its database tables.
- Domain modules (e.g. `giftexchange`) contain no Discord, HTTP or UI code. Client adapters
  (`internal/discord`, later web and mobile APIs) translate between a client and the module
  API; they hold no domain rules.
- Clients provide the interactive means to view an exchange's rules and operations and to
  configure what is configurable. The rules themselves are defined and enforced by the module.
- `internal/platform/` holds shared technical pieces (config loading, database pool, crypto,
  logging, job runner). It never imports a module.
- `depguard` in `.golangci.yml` enforces these imports. Do not weaken a depguard rule to make
  code compile; fix the dependency direction instead.

| Package | May import | Must not import |
|---|---|---|
| `internal/<domain module>` | `internal/platform/...`, its own `internal/` | other modules' `internal/`, `internal/discord`, `github.com/disgoorg/...` |
| `internal/discord` | module root packages, `internal/platform/...`, disgo | any module's `internal/` |
| `internal/platform/...` | standard library, third-party infra libs | any module, `internal/discord` |

## Data

- Postgres 18. Each module owns one Postgres **schema** named after the module
  (`giftexchange`). No queries or foreign keys across schemas.
- Queries with sqlc, migrations with goose, both kept inside the owning module
  (`internal/<module>/internal/store/`).
- Timed jobs (deadline draw, reminders, reveal) use River on the same Postgres. No
  hand-written schedulers.

## Configuration

- Configuration and app secrets live **in the database**. App secrets (Discord token, later
  identity provider and email keys) are stored encrypted with keys derived from `MASTER_KEY`.
- The environment holds only startup values: `DATABASE_URL` (required), `MASTER_KEY`, and
  `CHATISIMO_ENV` (`local`, `production`). Adding any other environment variable needs
  Héctor's sign-off.
- The first Discord token is set with `chatisimo config set` reading from stdin, never from
  a command-line argument (shell history) or a file in the repo.

## Secrets (public repository)

A secret that reaches a public repository is leaked the moment it is pushed, even if the
commit is removed later. Treat every one of these as mandatory:

- No credential, token, key, password or connection string with a real password in any
  tracked file. That includes local-dev values, tests, fixtures, docs and examples.
- `.env` and `.env.*` are gitignored; only `.env.example` is tracked, with obvious
  placeholders (`change-me`).
- Gate 1, local: `lefthook install` once per clone; the pre-commit hook runs gitleaks on
  staged changes. It is per-clone, so CI is the enforced gate.
- Gate 2, CI: gitleaks scans the full history on every push and pull request. It is a
  required status check on `main`.
- Gate 3, GitHub: secret scanning and push protection are on for the repository.
- Do not add gitleaks allowlist entries. If a file needs one, the file is wrong.
- If a secret is ever committed, stop: rotate it first, then clean history. Report it to
  Héctor immediately.

## Code and tests

- Go 1.27 (pinned in `mise.toml`). `golangci-lint` must pass with zero warnings.
- Test public module APIs and observable behavior. Mock only at the boundary: database,
  clock, randomness, Discord. Tests must survive a full rewrite of a module's internals.
- Randomness (the draw) and time come in through interfaces so tests are deterministic.
- Validation accumulates all errors and returns them together.

## Local development

- `mise install` installs the pinned toolchain.
- `docker compose up -d` starts Postgres 18. Ports come from this project's band
  **10000-10099**: Postgres is published on host port `10000`.
- Credentials for the local database come from the gitignored `.env`.

## Work tracking

- GitHub Issues and the Chatisimo GitHub Project. One milestone per roadmap step:
  1 Domain, 2 Discord bot, 3 Publish.
- Work happens on a branch per issue and merges into `main` through a pull request. `main`
  accepts no direct pushes and no force-pushes; required checks: gitleaks, lint, build, test.
- Design, research and decisions live in Héctor's notes, not in this repository.
