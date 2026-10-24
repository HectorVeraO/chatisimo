# Chatisimo

[![CI](https://github.com/HectorVeraO/chatisimo/actions/workflows/ci.yml/badge.svg)](https://github.com/HectorVeraO/chatisimo/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Chatisimo is Chatolandia's platform, written in Go.

## Requirements

| Tool | Version | Notes |
|---|---|---|
| [mise](https://mise.jdx.dev/) | Any recent release | Installs the tools below from `mise.toml`, except Docker |
| Go | 1.27.3 | |
| golangci-lint | 2.15.0 | Lint and format |
| gitleaks | 8.30.1 | Secret scanning |
| lefthook | 2.2.2 | Git hooks |
| goose | 3.29.0 | Database migrations |
| sqlc | 1.31.1 | Query code generation |
| govulncheck | 1.9.0 | Vulnerability scan |
| Docker | Compose v2 | Runs Postgres 18.6 locally |

Activate mise in your shell ([instructions](https://mise.jdx.dev/getting-started.html)) so the
pinned versions are used inside the repository.

## Getting started

1. Install the toolchain:
   ```sh
   mise install
   ```
2. Set your commit email to your GitHub noreply address, then install the git hooks, which check
   that email and run gitleaks, the formatter and the linter before every commit:
   ```sh
   git config user.email <id>+<login>@users.noreply.github.com
   lefthook install
   ```
3. Create your local environment file (Docker Compose reads `.env`) and replace every
   `change-me` value:
   ```sh
   cp .example.env .env
   ```
4. Start Postgres, wait for its healthcheck, and check that it accepts connections:
   ```sh
   docker compose up -d --wait
   docker compose exec postgres pg_isready -U chatisimo
   ```
   Expected: `/var/run/postgresql:5432 - accepting connections`. Postgres is published on
   `localhost:10000`.
5. Build and run the binary:
   ```sh
   go run ./cmd/chatisimo version
   ```
   Expected: `dev`.

## Configuration

The environment holds only startup values. Every other setting, and every app secret, is stored
in the database.

| Variable | Purpose | Example |
|---|---|---|
| `DATABASE_URL` | Postgres connection string | `postgres://chatisimo:change-me@localhost:10000/chatisimo?sslmode=disable` |
| `MASTER_KEY` | Root key for encrypting app secrets in the database | `change-me` |
| `CHATISIMO_ENV` | Environment name: `local` or `production` | `local` |

## Development

| Task | Command |
|---|---|
| Build | `go build ./...` |
| Test | `go test ./...` |
| Test with the race detector (needs cgo) | `go test -race ./...` |
| Lint | `golangci-lint run ./...` |
| Vulnerability scan | `govulncheck ./...` |
| Format | `golangci-lint fmt` |
| Run the pre-commit checks without committing | `lefthook run pre-commit` |
| Stop Postgres | `docker compose down` |

## Continuous integration

Every push and pull request to `master` runs six jobs. All six are required before a pull request
can merge; `govulncheck` becomes required after its first green run on `master`.

| Job | What it checks |
|---|---|
| `gitleaks` | Secrets and email addresses anywhere in the full git history |
| `identity` | Every commit author and committer email is a GitHub noreply address |
| `lint` | `golangci-lint`, including the import rules between packages |
| `build` | `go build ./...` |
| `test` | `go test -race ./...` |
| `govulncheck` | Known vulnerabilities reachable from the code (`govulncheck ./...`) |

## Repository layout

```text
cmd/chatisimo/        binary entry point
cookbooks/            architecture, coding and testing rules
.claude/              Claude Code agents, skills, hooks and settings
.github/              CI workflow, Dependabot, issue and pull request templates
compose.yaml          local Postgres
mise.toml             pinned toolchain
.golangci.yml         linters, formatters, import rules
lefthook.yml          pre-commit hooks
.gitleaks.toml        gitleaks rules, including the email rule
AGENTS.md             rules for contributors and agents
ONBOARDING.md         first-day setup for the agent tooling
```

## Contributing

Read [AGENTS.md](AGENTS.md) before opening a pull request: it covers the rules for secrets,
modules, configuration and tests, and the issue-to-merge workflow. Details are in
[cookbooks/architecture.md](cookbooks/architecture.md),
[cookbooks/coding.md](cookbooks/coding.md) and [cookbooks/testing.md](cookbooks/testing.md).

## License

[MIT](LICENSE)
