# Code Path Parity table format

The Code Path Parity section lists files whose shared logic should stay identical, so an agent editing one file updates the others.

## Section template

```markdown
## Code Path Parity

When you change one file in a row below, check ALL the others in the same row.
Drift between these is the most common source of "fixed in dev, broken in prod"
bugs.

| Group | Files | What duplicates | Check | What breaks silently |
|---|---|---|---|---|
| <name> | `<path A>`, `<path B>`, `<path C>` | <one line on what logic is duplicated and what shape the duplication has> | <for byte-identical copies, the hash command and the expected value; otherwise the comparison to make> | <the failure a missed update produces> |
```

Rows go below the section's "Add parity rows below this line" sentinel, outside any AUTOGEN marker, so regeneration never overwrites them. List every file explicitly: unlisted files are not checked.

## Example (hypothetical subproject `service_a`)

```markdown
## Code Path Parity

| Group | Files | What duplicates | Check | What breaks silently |
|---|---|---|---|---|
| Dual-flavor routers | `routes/orders.js`, `routes/ordersInternal.js` | Public + internal routers expose paired handlers (`listX` vs `internalListX`) | Every handler in one file has its twin in the other | An internal caller gets 404 for a method the public router already serves |
| Wrapper pattern parity | `lib/billingApi.js`, `lib/notificationApi.js`, `lib/emailSender.js`, `lib/storage.js` | All HTTP wrappers; new ones use `{ ok, status, data }`, old ones return `false` on error | A new wrapper returns `{ ok, status, data }` | A caller reads `result.ok` from a wrapper that returns `false`, and a failure passes as success |
| Auth middleware variants | `routes/orders.js`, `routes/ordersInternal.js`, `routes/customers.js` | Each route file declares its own `router.use()` auth check | A new caller class (api-key, session token, service-key) is handled in every file listed | A route is reachable without the auth check its callers need |
| Audit log on mutations | `controllers/order.controller.js`, `controllers/customer.controller.js` | Every create/update/delete writes an audit record with before/after snapshots | Every CUD handler in the files listed writes the audit record | A mutation leaves no audit trail; nothing fails until an audit asks for it |

Run `/pl-review-working-tree`; `patterns-reviewer` flags missing parity.
```

## Example (hypothetical monorepo root, cross-cutting)

```markdown
## Code Path Parity

| Group | Files | What duplicates | Check | What breaks silently |
|---|---|---|---|---|
| `job-queue` library | `service_a/lib/job-queue.js`, `service_b/lib/job-queue.js`, `service_c/lib/job-queue.js` | Same library copy-pasted into 3 services; bug fixes propagate by hand until it is extracted into a shared package | `for f in service_a/lib/job-queue.js service_b/lib/job-queue.js service_c/lib/job-queue.js; do git hash-object "$f"; done` -> every line prints `<baseline hash>` | A fix lands in one service and the others keep the bug |
| `tenantResolver` | `service_a/lib/tenantResolver.js`, `service_b/lib/tenantResolver.js` | Multi-tenant resolution | Already diverged: establish the good version with `git log` before propagating anything | A request resolves to the wrong tenant in one service only |
```

## When to add a parity row

- When two or more files contain the SAME logic from manual copying or codegen
- When two or more files MUST stay in sync because they implement matching halves of a contract (request handler vs response shape, public vs internal, dev vs prod)
- When a refactor that "moved logic" actually duplicated it across multiple callers without consolidation

## When NOT to add a parity row

- Logic that is similar but intentionally different (e.g. two endpoints with different validation rules): that is similarity, not parity. Do not add a row for it.
- Pure boilerplate already enforced by linters or codegen.
- Single-instance code with no twin.

## Byte-identical copies: the hash command

When a row's files are meant to be byte-identical copies (such as a library copied into multiple services), the row carries a baseline hash AND the command to recompute it. Keep the command portable (POSIX shell, `git hash-object` or `sha256sum`), list every copy explicitly, and state the expected value:

```markdown
| `job-queue` library | `service_a/lib/job-queue.js`, `service_b/lib/job-queue.js`, `service_c/src/lib/job-queue.js` | Byte-identical copies; a fix in one applies to all | `for f in service_a/lib/job-queue.js service_b/lib/job-queue.js service_c/src/lib/job-queue.js; do git hash-object "$f"; done` -> every line prints `<baseline hash>`, the value recorded when the row was added | A differing value means drift: a fix reached only some copies |
```

A group whose copies have ALREADY diverged states so explicitly ("already diverged: establish the good version with `git log` before propagating anything") instead of listing a hash that no copy matches.

## When the root section outgrows the line budget

Root `AGENTS.md` has a line budget (default 400, enforced by `.claude/scripts/validate-artifacts.mjs`). When the matrix exceeds this budget, the full rows (files, hash command, invariants, what breaks silently) move verbatim to `.claude/references/code-path-parity.md`. Below its sentinel, root retains one summary bullet per group naming the files. The agent reads the full row in the reference before touching any file named in root. A row is never deleted to meet the budget: it moves.

## How an agent uses this section

1. When editing a file, the agent (or `/pl-review-working-tree`) checks whether the file is listed in any parity group.
2. If listed, it reads the other files in that group and checks for the same change pattern.
3. If a needed change is missing in a sibling file, the agent flags it explicitly: "this change touches `routes/orders.js`; the parity group includes `routes/ordersInternal.js` which appears to need the same update".
4. The agent applies the parallel change in the same PR (preferred) or notes it in the PR description if unable to apply it.
