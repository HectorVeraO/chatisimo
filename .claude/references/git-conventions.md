# Git Conventions & Commit Message Guidelines

Detailed guide, rationale, and worked examples for this repo's git commit conventions.

Passive reference, reached from root `AGENTS.md` "Commit message format" (and read during `/pl-address-issue` commit steps). The concise rules embedded in root `AGENTS.md` remain the active contract.

---

## 1. Rationale & Philosophy

Reviewers check commit messages against the project declaration convention. The standard is:
**Concise enough to stay readable, complete enough to answer "why" without opening the diff.**
A one-line `fix: stuff` fails completeness; a twelve-paragraph essay fails readability.

### Deviations from Plain Conventional Commits

1. **Full Type Words** (`feature`, not `feat`; `performance`, not `perf`):
   Full words remove ambiguity across teams and agent models.
2. **The Issue Goes in a `Refs:` Trailer, Not the Subject**:
   GitHub links `Refs: #N` to the issue, and the subject stays readable in `git log --oneline`.
3. **No Closing Keywords in Commits** (`Closes #N`, `Fixes #N`):
   A closing keyword in a commit closes the issue when the commit reaches the default branch, outside the PR that
   should close it. `Closes #N` belongs in the PR body only.

---

## 2. Commit Message Structure

Every commit message follows this structure:

```
<type>(<scope>): <Imperative subject, <=50 chars, no trailing period>

<Why: 1-3 lines. The problem, the reason or the trigger.>

- <what changed, 2-5 bullets, one per logical unit>

BREAKING CHANGE: <what breaks and what consumers must do>
Declared-change: <surface class>
Refs: #<issue>
Co-Authored-By: <Model> <model noreply address>
```

### Language Rules
- **Structural Tokens**: Always English and never translated: `feature`, `fix`, `refactor`, `BREAKING CHANGE:`, `Declared-change:`, `Refs:`, `Co-Authored-By:`.
- **Subject and Body**: Follow the `Docs language` root `AGENTS.md` declares.

---

## 3. Section-by-Section Specification

### The Subject Line
`<type>(<scope>): <Subject>`

- **`<type>`**: Closed vocabulary:
  - `feature`: new behaviour observable by a user or caller.
  - `fix`: corrected behaviour that was defective or incorrect.
  - `refactor`: internal structural change with identical observable behaviour.
  - `performance`: same behaviour, measurably faster or lighter.
  - `test`: test additions or adjustments only.
  - `documentation`: documentation changes only.
  - `chore`: maintenance with no behaviour change (dependencies, tooling, configuration).
  - `build`: build system, packaging, container configuration.
  - `ci`: pipeline or CI/CD definitions.
- **`(<scope>)`**: Optional. Either a subproject or module directory name, or one of two fixed scopes:
  - Use a directory name in a monorepo to indicate which subproject or service was modified; omit it in a single-project repo where it would be redundant.
  - `deps`: a dependency bump with no other change.
  - `unscoped`: the designated scope for Healthy Baseline Rule commits.
- **Subject Line Length & Mood**:
  - Imperative mood (e.g. `Add`, `Fix`, `Simplify`).
  - First letter capitalised.
  - **No trailing period**.
  - **50 characters or fewer** for the description part; line total <= 72 characters.

### The Body
- **When Required**: Required unless the change is self-evident (such as a pure rename or single dependency bump). If a reviewer would need to ask *"why?"*, a body is mandatory. Do not restate the subject line.
- **Structure**:
  - **First, the Why**: 1 to 3 lines explaining the underlying problem, reason, or trigger.
  - **Then, the What**: 2 to 5 bullet points naming logical units of change (not an exhaustive file list; `git show --stat` provides that).
- **Constraints**: Hard cap of 12 lines. Wrap at 72 columns.

### Trailers (After Blank Line)
- **`BREAKING CHANGE: <description>`**: Mandatory when consumers or callers must update code or configuration. State what broke and the required action. Must be the first trailer.
- **`Declared-change: <surface class>`**: Optional heads-up when touching designated shared surfaces (goose migrations under `internal/<name>/internal/store/`, a module's public API package (`internal/<name>/`), the import rules in `cookbooks/architecture.md`). Counterpart to PR `## Declared change`.
- **`Refs: #<issue>`**: The issue the commit belongs to (e.g. `Refs: #17`), one trailer per issue. Omit it only when the work genuinely has no issue; never write `Refs: none` or `N/A`.
- **`Co-Authored-By: <Model> <model noreply address>`**: Mandatory on every commit written by an AI agent; omitted on human commits.
  - Name the specific model (e.g. `Claude Sonnet 4.5`, `Claude Opus 5`), not just the harness.
  - Uses standard git trailer syntax so git parses and queries it natively (`git log --grep='^Co-Authored-By:'`).
  - Must be the final trailer in the list.

### What Never Goes in a Commit Message
- `WIP`, `fixes`, `misc changes`, `ajustes` or `cambios varios` as the entire subject.
- An issue number in the subject, or a closing keyword (`Closes #12`, `Fixes #89`): the issue goes in `Refs:`, the closing keyword in the PR body.
- Attributing failures as pre-existing without captured evidence.
- Author names, timestamps, or PR numbers in the body (managed by git metadata).

---

## 4. Multi-Scenario Worked Examples

### Example A: Normal In-Scope Fix in a Monorepo
```
fix(api): Reject tokens without a tenant claim

A token issued before the claims change passed validation and was
resolved against the default tenant, exposing another organization's
data.

- Validate that the `tid` claim is present before resolving the tenant.
- Return 401 instead of falling back to the default tenant.
- Add the negative case to the auth integration suite.

Refs: #17
Co-Authored-By: Claude Sonnet 4.5 <model noreply address>
```

### Example B: Breaking Change on a Designated Surface
```
feature(api): Move user signup to the v2 payload

The v1 signup cannot declare the second factor during onboarding,
which becomes mandatory.

- Add the required `mfa` block to the signup payload.
- Keep v1 answering 410 with a link to the migration guide.

BREAKING CHANGE: the shape of `/api/v1/register` changed. Consumers
must move to `/api/v2/register` and send the `mfa` block.
Declared-change: api-contract
Refs: #16
Refs: #15
Co-Authored-By: Claude Sonnet 4.5 <model noreply address>
```

### Example C: Self-Evident Commit (Subject Plus Trailer)
```
chore(deps): Bump express to 4.21.2

Refs: #15
Co-Authored-By: Claude Sonnet 4.5 <model noreply address>
```

### Example D: Healthy Baseline Rule Commit (Off-Scope Fix)
```
chore(unscoped): Fix broken import in report worker

The build failed before this change; fixed to reach a healthy
baseline, outside the issue's scope.

- Fix the import path after the module move.

Refs: #17
Co-Authored-By: Claude Sonnet 4.5 <model noreply address>
```
