# Chatisimo

Chatisimo is a Discord bot platform written in Go, built as a modular monolith. The first
feature is a gift exchange: draw who gives a gift to whom, on any date. Music, a web app and
a mobile app are planned later.

## Prerequisites

- [mise](https://mise.jdx.dev/) (installs the pinned Go toolchain and dev tools)
- Docker (for the local Postgres 18)

## Quick start

```sh
mise install
cp .env.example .env        # then replace every change-me value
docker compose up -d
go run ./cmd/chatisimo version
```

Install the git hooks once per clone (they run gitleaks and Go checks before each commit):

```sh
lefthook install
```

## Contributing

Architecture, secret-handling and testing rules are in [AGENTS.md](AGENTS.md). They apply to
every contributor, human or automated.
