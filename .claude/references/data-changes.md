# Migrations and data changes

Passive reference, reached from root `AGENTS.md` "Data changes". The root
summary is the active rule; this file holds the requirements for migrations and ad-hoc data scripts.

---

## Who writes to which database

**An agent never writes to a non-local database.** No migration runs, no database shell scripts, no
exceptions to unblock. An agent may run a migration or data script only against a LOCAL database. On every
shared environment (QA, staging, production), the change is run **by the person doing the release** as part
of the release - never ad hoc by whoever is inspecting the data.

Database-resident content is data too. When configuration, templates, prompts, or any other documents read
by the application live in the database rather than the repository, editing them is a data change and
follows the same process as a migration.

## Migrations and ad-hoc data scripts

Only the migrations of the repository's own migration system (goose), run by that system, are
committed and reviewed in the PR. The generator never adds a migration system: a repository without one versions
no data change. An ad-hoc data script is never versioned in any repository: it lives in the developer's gitignored
scratchpad `.me/<slug>/`, together with the dry-run output and logs of its local runs. No committed
folder holds ad-hoc data scripts.

The PR description carries, for every data change:

- the vehicle: a goose migration in the diff, or an ad-hoc data script that is not in the diff;
- the tables it changes;
- the order relative to the deploy (before or after), including the rationale when "before".

Requirements for an ad-hoc data script:

1. **Idempotent.** Running it twice leaves the data exactly as running it once.
2. **Dry-run is the default.** Without an explicit `--apply` flag, it reads, reports planned changes
   (counts and a small sample of ids, never personal data), and writes nothing.
3. **Rollback point first.** When overwriting or deleting non-recomputable data, it stores prior
   values before modifying anything - see "Where the rollback record lives".

## Ordering: when the migration is a prerequisite

**When a release removes the reader's tolerance for a stored legacy value, the migration is a prerequisite,
not cleanup.** Deploying the code first has two costs, and only the first ends when the migration lands:

- While the window is open, records with legacy values match no branch in the new reader and drop out
  of filtered results **silently** - no error, no log.
- Past the window, new code can **overwrite** an active record's legacy value on the next write, destroying
  the data the migration was meant to convert; no migration repairs it.

So the migration runs **before** the code that stops reading the old value, and the PR description states
that ordering explicitly: the data change, that it must run before deploy, and the rationale.

## Where the rollback record lives

**A rollback point cannot live inside the records it touches.** Strict schemas (strict ORM and ODM models)
drop undeclared fields on the next write, so a marker field on the record is lost when it is needed.

The rollback record belongs in **its own table**, named after the task (its branch slug), and that table is
**never** the migration ledger maintained by goose. Name the rollback table in the PR description.

## When the versioned system is the vehicle

An ad-hoc data script never goes in goose's migrations directory: that directory holds only the migrations the tool runs. Migration files are **immutable** once merged: correct mistakes
with a new migration, never by editing an existing one.
Run goose only against the local database; on shared environments it runs as part of the
release, sequenced so schema or data changes land before code that depends on them.
