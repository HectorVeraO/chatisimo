# Local Environment & Docker Compose Runbook

Operational runbook for the local stack. Passive reference, reached from root `AGENTS.md`
"Local environment". The concise policy embedded in root `AGENTS.md` remains the active rule.

---

## Local environment

### The split: infra in Docker, the component you are editing on the host

`compose.yaml` is the source of truth for the local stack. Two kinds of service live in it:

- **Infra** - databases: `postgres`. Always in Docker.
- **Sourced components** - none: `compose.yaml` builds nothing from this repository. The
  `chatisimo` binary always runs on the host (`go run ./cmd/chatisimo`). If a service with a `build:` stanza is
  ever added, the rule below applies to it.

**The rule for a sourced component:**

| Situation | Where it runs |
|---|---|
| You are **actively modifying** it | **Host-dev.** Its own dev command, with hot reload. |
| You are **not** expecting to change it this task | **Docker**, from the compose file. |

A containerised component must be rebuilt for every change; a host-dev one reloads in seconds.

**Boot everything with compose, then take down only the component you are about to edit and run
that one on the host.** Do not run the whole stack on the host because one service needs hot reload.

### Booting

```
docker compose -f compose.yaml up -d
```

**Always `-d`.** Follow logs on demand, into a file:

```
docker compose -f compose.yaml logs --no-color <service> > .me/<slug>/logs/<service>.log 2>&1
```

then grep that file. For a live stream use `--tail 80` or `--since 10m`. Never paste a whole log into the
conversation.

### Switching one component to host-dev

Get **addressing** right:

- Inside the compose network, services reach each other by **service name** (`postgres:5432`).
- From the host, the same service is reachable only on its **published port** (`localhost:<published-port>`),
  provided the compose file publishes it.

When moving a component to host-dev, its configuration must point at `localhost:<published-port>` for every
dependency, not at compose service names. If a dependency port is not published in `compose.yaml`,
publishing it is part of the change. It must use a port allocated from the project's assigned range
(documented alongside `compose.yaml`), not an arbitrary free port; ask when no port range is documented.

Sequence:

```
docker compose -f compose.yaml up -d
docker compose -f compose.yaml stop <the component you are about to edit>
<its host dev command, e.g. npm run dev>
```

Stopping rather than removing it keeps the rest of the stack's view of the network intact.

### Resetting

**`docker compose down -v` is allowed here without asking.** Local volumes are disposable: the stack recreates
their seeded fixtures and dev data on the next `up`.

```
docker compose -f compose.yaml down -v
docker compose -f compose.yaml up -d
```

This applies to **local** environments only. Nothing here authorises touching a shared, staging or production
stack, or any volume outside this compose project.

### Verifying

Verification runs against the stack as the reviewer would see it: unmodified sourced components in
Docker, the one you modified on the host, and infra in Docker. If a change spans several sourced components,
verify with all of them containerised at the end: a change that only works in host-dev is unverified.

### Cleaning up

**Stop everything you started before reporting done.** Compose stacks, host dev servers, watchers, test
runners, browsers. Report what was stopped.

## Migrations and data changes

Resetting a local volume is routine; changing data anywhere else is not. Migrations, data fixes and edits to
database-resident content follow `.claude/references/data-changes.md`.
