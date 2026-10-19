# Chatisimo

Chatisimo is Chatolandia's platform, written in Go. The repository is public: everything
committed here is visible to anyone.

## Commands

| Task | Command |
|---|---|
| Install the toolchain | `mise install` |
| Install the git hooks | `lefthook install` |
| Start / stop local Postgres | `docker compose up -d` / `docker compose down` |
| Build | `go build ./...` |
| Test | `go test ./...` |
| Lint | `golangci-lint run ./...` |
| Format | `golangci-lint fmt` |

## Rules

**Secrets.** Never commit a credential, token, key or password, and that includes tests,
fixtures and examples. Local values go in `.env`, which git ignores; `.env.example` holds only
`change-me` placeholders. Run `lefthook install` once per clone so gitleaks checks every
commit; CI scans the full history again and GitHub push protection rejects known secret
formats. Never add a gitleaks allowlist entry. If a secret is ever pushed, rotate it first,
then clean the history and tell Héctor.

**Modules.** Each feature is a module under `internal/<name>/`. Other code may only import
the module's root package; everything else goes under `internal/<name>/internal/`, which Go
makes unreachable from outside. The Discord adapter and the shared platform packages follow
their own import rules. Details, diagram and import rules: `references/architecture.md`.

**Configuration.** Settings and app secrets live in the database, with secrets encrypted by
keys derived from `MASTER_KEY`. The environment holds only `DATABASE_URL`, `MASTER_KEY` and
`CHATISIMO_ENV` (`local` or `production`); adding another variable needs Héctor's approval.
Secrets are entered through `chatisimo config set`, which reads them from stdin.

**Code.** Read `references/coding.md` before writing Go code. It covers design, functions,
errors, naming, comments and logging. Lint must report zero issues; fix the code, not the
linter configuration.

**Tests.** Every change ships with tests. Test a module through its public API, so the tests
survive a rewrite of its internals, and replace only the boundaries: clock, randomness,
Discord. Read `references/testing.md` before writing tests.

**Local environment.** Tool versions come from `mise.toml`. Host ports come from the range
10000-10099; Postgres listens on 10000.

## Workflow

1. Open an issue from the `task` or `bug` template, give it a milestone (`1 Domain`,
   `2 Discord bot`, `3 Publish`) and add it to the
   [Chatisimo Project](https://github.com/users/HectorVeraO/projects/1).
2. Create a branch named `<issue>-<slug>`, for example `4-agents-workflow`, and move the
   card to `In Progress`.
3. Open a pull request to `main` from the template, with `Closes #<issue>` in the body.
4. Merge once `gitleaks`, `lint`, `build` and `test` pass against the latest `main`. Merging
   closes the issue; move its card to `Done`.

`main` accepts changes only through pull requests: no direct pushes, no force-pushes. Agents
commit, push and merge only with Héctor's explicit approval for that action. Design notes,
research and decisions stay out of this repository.
