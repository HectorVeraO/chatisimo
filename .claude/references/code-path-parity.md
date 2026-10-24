# Code Path Parity: full matrix

Passive reference, reached from root `AGENTS.md` "Code Path Parity". It holds the full cross-subproject parity rows; root keeps one summary bullet per group naming its files. Read the full row here before touching any file named in root's Code Path Parity section.

Each row states:

- **Files**: every copy or twin, by repo-relative path. No "and similar": a file not listed is not checked.
- **What duplicates**: the logic that is shared and the shape the duplication has (byte-identical copy, matching halves of a contract, same literal across layers).
- **Check**: for byte-identical copies, the command that recomputes the hash of every copy and the expected value; a hash nobody can recompute is not a baseline. For copies that have already diverged, say so and name how to establish the good version before propagating anything.
- **What breaks silently**: the failure a missed update produces, so the reader knows what to look for.

Format and examples: `.claude/references/code-path-parity-format.md`.

---

<!-- Add parity rows below this line. The skill never edits below. -->

### Go and Postgres versions

| Field | Value |
|---|---|
| Files | `go.mod`, `mise.toml` (`go`), `.deploy/local/Dockerfile` (`golang:` tag); `.deploy/local/docker-compose.yaml`, `.github/workflows/ci.yml` (`postgres:` tag) |
| What duplicates | The Go version and the Postgres version are written in several files, each by hand. |
| Check | `go.mod` `go`, `mise.toml` `go` and the `golang:<version>-alpine` tag in `.deploy/local/Dockerfile` name the same Go version; the `postgres:<version>-alpine` tag is identical in `docker-compose.yaml` (the `postgres` and `e2e-db` services) and `ci.yml`. |
| What breaks silently | The E2E images or CI test a Go or Postgres version that production code is not built against; a feature works locally and fails in CI, or the reverse. |
