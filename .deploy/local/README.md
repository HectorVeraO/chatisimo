# Local stack

## What runs here

The default profile runs Postgres for development and tests. The `e2e` profile (the fake
Discord, the `-cover` build of chatisimo and two one-shot setup services) is added in milestone 2.

## Ports

Band 10000-10099.

| Port | Service | Profile | Bound to |
|---|---|---|---|
| 10000 | postgres | default | all interfaces (as today) |
| 10010 | reserved: fakediscord (REST, Gateway, web client, control API) | e2e (added later) | 127.0.0.1 |
| 10001-10009, 10011-10099 | free | - | - |

## Prerequisites

- Docker Compose v2.
- `cp .example.env .env`, then replace every `change-me`. Set `MASTER_KEY` to 32 random bytes in
  standard base64.

## Commands

Run from the repository root. `.env` stays at the root and is passed with `--env-file`.

```sh
# Start Postgres and wait for its healthcheck
docker compose --env-file .env -f .deploy/local/docker-compose.yaml up -d --wait

# Status
docker compose --env-file .env -f .deploy/local/docker-compose.yaml ps

# Logs into a file, then grep it
docker compose --env-file .env -f .deploy/local/docker-compose.yaml logs --no-color postgres > .me/<slug>/logs/postgres.log 2>&1

# Stop, keeping the data
docker compose --env-file .env -f .deploy/local/docker-compose.yaml stop

# Remove the containers, keeping the data
docker compose --env-file .env -f .deploy/local/docker-compose.yaml down
```

## Data

The compose project is named `chatisimo`, so the data lives in the `chatisimo_postgres-data`
volume. `down -v` deletes that volume, which holds the **development database**.

## Host-dev

Day to day the binary runs on the host (`go run ./cmd/chatisimo`) against Postgres on
`localhost:10000`.
