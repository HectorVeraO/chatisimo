# Local stack

## What runs here

The default profile runs Postgres for development and tests. The `e2e` profile adds the fake
Discord (`fakediscord`), a `-cover` build of chatisimo (`chatisimo`) and two one-shot setup
services (`e2e-db`, `chatisimo-setup`), so the browser suite and manual exploration run the real
binary.

## Ports

Band 10000-10099.

| Port | Service | Profile | Bound to |
|---|---|---|---|
| 10000 | postgres | default | all interfaces (as today) |
| 10010 | fakediscord: REST, Gateway, web client, control API | e2e | 127.0.0.1 |
| 10001-10009, 10011-10099 | free | - | - |

## Prerequisites

- Docker Compose v2.
- `cp .example.env .env`, then replace every `change-me`. Set `MASTER_KEY` with
  `go run ./cmd/chatisimo keygen`. The Postgres password must be URL-safe: the e2e profile
  interpolates it into `DATABASE_URL`.

## Commands

Run from the repository root. `.env` stays at the root and is passed with `--env-file`.

```sh
# Start Postgres and wait for its healthcheck
docker compose --env-file .env -f .deploy/local/docker-compose.yaml up -d --wait

# Start the E2E stack (also starts Postgres); builds both images
docker compose --env-file .env -f .deploy/local/docker-compose.yaml --profile e2e up -d --build --wait

# Status
docker compose --env-file .env -f .deploy/local/docker-compose.yaml ps

# Logs into a file, then grep it
docker compose --env-file .env -f .deploy/local/docker-compose.yaml logs --no-color postgres > .me/<slug>/logs/postgres.log 2>&1

# Stop, keeping the data
docker compose --env-file .env -f .deploy/local/docker-compose.yaml stop

# Remove the containers, keeping the data
docker compose --env-file .env -f .deploy/local/docker-compose.yaml down
```

The fake Discord's web client is at `http://127.0.0.1:10010`. The bot is ready when
`curl -s "http://127.0.0.1:10010/_control/healthz?bot=1"` reports `"bot":"ready"`.

Stop the bot first so it flushes coverage, then remove the E2E stack:

```sh
docker compose --env-file .env -f .deploy/local/docker-compose.yaml --profile e2e stop chatisimo
docker compose --env-file .env -f .deploy/local/docker-compose.yaml --profile e2e down
```

`down` removes every container of the project, Postgres included; the data stays in the volume.

## Token handoff

The fake Discord creates a synthetic token when it starts. `chatisimo-setup` pipes
`GET /_control/token` into `chatisimo config set discord.token` and sets
`discord.api_base_url` to the fake. Nothing is written to a file. The fake's token is accepted
only with `CHATISIMO_ENV=local`.

## Data

The compose project is named `chatisimo`, so the data lives in the `chatisimo_postgres-data`
volume. The e2e profile uses its own database, `chatisimo_e2e`, in the same Postgres; `e2e-db`
drops and recreates it on a fresh e2e boot (it keeps the database while a client is connected,
so a repeated `up` does not pull it from a running bot) and never touches the development
database. `down -v` deletes the volume, which holds the **development database too**.

## Host-dev

Day to day the binary runs on the host (`go run ./cmd/chatisimo`) against Postgres on
`localhost:10000`.

To use the fake Discord from the host, run `go run ./cmd/fakediscord` (listens on
127.0.0.1:10010). A bot on the host then needs `discord.token` and `discord.api_base_url` set in
the development database from the fake (`GET /_control/token`); this overwrites any token stored
there.

## Coverage

`chatisimo` runs with `GOCOVERDIR=/cover`, bind-mounted to `.cover/playwright/`. The `covmeta.*`
and `covcounters.*` files appear when the container stops gracefully (`stop chatisimo`).
