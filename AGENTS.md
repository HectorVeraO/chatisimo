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

**Secrets and personal data.** Never commit a credential, token, key, password or connection
string with a real password, or personal data: real names tied to accounts, email addresses,
Discord user or guild IDs, or anything else that identifies a person. That includes tests,
fixtures, docs and examples, and issue and pull request text, which is public too. Env files are
named `.<name>.env` (`.local.env`) or `.env`, and git ignores them all except `.example.env`,
which holds only `change-me` placeholders. Commit with your GitHub noreply email
(`git config user.email <id>+<login>@users.noreply.github.com`); the pre-commit hook and the
`identity` CI job reject any other. Run `lefthook install` once per clone so gitleaks checks every
commit; CI scans the full history again and GitHub push protection rejects known secret
formats. The only gitleaks allowlist is the fixed list of example and noreply addresses in the
`email-address` rule of `.gitleaks.toml`; never add an entry to silence a finding. If a secret
or personal data is ever pushed, rotate any secret first, then clean the history and tell Héctor.

**Modules.** Each feature is a module under `internal/<name>/`. Other code may only import
the module's root package; everything else goes under `internal/<name>/internal/`, which Go
makes unreachable from outside. The Discord adapter and the shared platform packages follow
their own import rules. Details, diagram and import rules: `references/architecture.md`.

**Configuration.** Settings and app secrets live in the database, with secrets encrypted by
keys derived from `MASTER_KEY`. The environment holds only `DATABASE_URL`, `MASTER_KEY` and
`CHATISIMO_ENV` (`local` or `production`); adding another variable needs Héctor's approval.
Secrets are entered through `chatisimo config set`, which reads them from stdin; never pass a
secret as a command-line argument or keep it in a file in the repo.

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
2. Create a branch named `feature/<task>` for a `task` issue or `bugfix/<task>` for a `bug`
   issue, where `<task>` is a short slug of the issue title, for example
   `feature/branch-naming`, and move the card to `In Progress`.
3. Review the branch against the rules before the pull request: in Claude Code run
   `/pl-review-branch`; elsewhere, check the change against each file in `references/` and the
   rules above. The agent running the review decides which findings to apply, without asking
   Héctor: it fixes every mandatory finding and applies a polish finding when it improves the
   change. Every finding left unapplied goes in the pull request body with the reason.
4. Open a pull request to `master` from the template, with `Closes #<issue>` in the body.
5. Merge once `gitleaks`, `identity`, `lint`, `build` and `test` pass against the latest
   `master`. Merging closes the issue; move its card to `Done`.

`master` accepts changes only through pull requests: no direct pushes, no force-pushes. Agents
commit, push and merge on their own once every required check passes and `/pl-review-branch`
left no mandatory finding. A change that touches `.github/`, `AGENTS.md`, `references/`,
`.claude/`, `lefthook.yml`, `.gitleaks.toml` or the GitHub ruleset still needs Héctor's explicit
approval before merging. Design notes, research and decisions stay out of this repository.
