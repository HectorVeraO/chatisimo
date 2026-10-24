---
name: pl-review-working-tree
description: "Review the LOCAL UNCOMMITTED change in this repo (`git diff HEAD` plus untracked files) with the concern-split reviewers before committing. Every finding stops the commit until resolved. Read-only; posts nothing; an empty change is a STOP. For a PR use pl-review-pr."
argument-hint: "[--profile default|full] [--with patterns,security] [--design <path>] [--no-secrets] [--concerns <list>] [--model <name>] [--destination <branch>]"
---

# pl-review-working-tree

Review the local, uncommitted change in **Chatisimo** before it becomes a commit. It runs the same reviewers `/pl-address-issue` Step 10 runs and reports what a real reviewer would flag.

**The review is not adversarial.** Each agent applies the emission filter from `.claude/references/reviewer-contract.md`. **Every finding stops the commit until it is resolved**; severity (`blocker` / `warning`) only measures impact, never whether a finding must be addressed.

`$ARGUMENTS` is parsed as: `[--profile default|full] [--with <list>] [--design <path>] [--no-secrets] [--concerns <list>] [--model <name>] [--destination <branch>]`.

## The concern set

The same selection rules as `pl-review-pr`:

| Concern | Agent | Selected |
|---|---|---|
| secrets | `secrets-reviewer` | **always**, on every non-empty change: documentation, config and data files included; `--no-secrets` turns it off |
| logic | `logic-reviewer` | when non-test code changes |
| test | `test-reviewer` | only when a test file changes (`digest.surface.test.hasSurface`) |
| patterns | `patterns-reviewer` | off; `--with patterns` or `--profile full`, and **only** when a touched subproject has a non-empty cookbook |
| security | `security-reviewer` | off; `--with security` or `--profile full` |
| architecture | `architecture-reviewer` | off; a design reference (`--design ...`) or `--profile full`, and **only** when design input resolves |

Profile **`default`** (the default) runs the "always" and "when" rows plus what `--with` and `--design` add; **`full`** runs all six. A documentation-only change runs `secrets` alone. Artifact paths carry the profile: `.me/review-pr/working.<profile>.report.*`.

## Argument parsing

**Scope**: every uncommitted change: `git diff HEAD` (staged and unstaged) plus every untracked, non-ignored file (`git ls-files --others --exclude-standard`), each shown as a new-file diff. Never bare `git diff`: it misses staged changes.

**Destination**: `--destination <branch>` names the branch this work will merge into, for lenient-mode evaluation. Without it, resolve it in this order:

1. The branch's upstream (`git rev-parse --abbrev-ref @{upstream}`, remote prefix removed) when it names a different branch than the current one.
2. `master`, the base of every `feature/` and `bugfix/` branch.

Name the resolved destination and its source in the report.

**Concern selection:**

- `--profile default|full`: default `default`. Never infer `full` on your own. The retired names `dev` and `arch` are rejected: say so and name the two profiles.
- `--with <list>`: `patterns` and/or `security`, added to `default`. Any other name is a usage error: say so and stop.
- `--no-secrets`: do not run `secrets`; report it as excluded by the flag.
- `--model <name>`: one model for every reviewer this run; without it each reviewer runs on the model its definition pins.
- `--design <path>`: the design artifact this change was agreed to follow. Selects the architecture concern. `--design issue` takes the design section of the issue the branch's PR closes (`Closes #N`, read with `gh pr view --json closingIssuesReferences` and `gh issue view <N> --json body`), as `pl-review-pr` does.
- `--concerns <list>`: comma-separated subset of the selected concerns, to re-run one concern after a fix.

**No PR scope.** To review a GitHub pull request, use `/pl-review-pr <PR-id>`.

## Step 1: Build the review packet, and stop if the diff is empty

```
node .claude/skills/pl-review-pr/scripts/build-working-input.mjs --destination <the resolved destination>
```

The script removes the previous working-tree packet and reports, then writes `.me/review-pr/working.input.json` in the same shape a fetched PR produces, with 25 lines of surrounding context per hunk (`--context <n>` to change it) and `head.readFrom: "working-tree"`: reviewers read context from disk.

**Exit 3 means there is no uncommitted change. STOP.** Report exactly what was checked, for example `no uncommitted changes: git diff HEAD is empty and there are no untracked files`, and end. Dispatch nothing, never fall back to `git log` / `git show` / the branch range, and never invent findings.

When invoked from `/pl-address-issue` Step 10, an empty diff also **stops the orchestrator**.

## Step 2: Build the manifest

```
node .claude/skills/pl-review-pr/scripts/digest.mjs working
```

Same output as the PR path: `files` with destination-side hunk ranges, `stats`, and the `digestMarkdown` block for each agent prompt. `lenientMode` is evaluated against the destination from Step 1.

## Step 3: Resolve the concern set

Start from the selected set (`default`: `secrets`, `logic`, `test`, plus `--with` and `--design`; `full`: all six; minus `secrets` under `--no-secrets`) and exclude what does not qualify:

1. **secrets / logic / test / security**: drop when `digest.surface.<concern>.hasSurface` is `false` (every changed file is documentation, a lockfile or plain data; logic also when only test files changed; test when no test file changed). Report the digest's reason. A documentation-only change runs `secrets` alone. When `test` runs, hand the agent the touched subprojects' indexes and `digest.surface.testFiles`.
2. **architecture**: include only if `digest.surface.architecture.hasSurface` is `true` and design input resolves: the `--design <path>` file (must exist and be non-empty), else an SDD spec-delta for the in-flight change (`specs/changes/*/spec-delta.md`) when the repo uses SDD and the change is identifiable from the branch or issue. Without `--design` and without a spec-delta, leave it out of the set. When `--design` was given and does not resolve, report it as not run with the reason.
3. **patterns**: include only if `digest.surface.patterns.hasSurface` is `true` and a touched subproject has a non-empty cookbook: at least one non-empty `<subproject>/cookbooks/*.md` fragment, or a `<subproject>/AGENTS.md` carrying real pattern content. Hand the agent each touched subproject's index and its fragment paths; it reads the fragments the index's "Read before" table routes the touched files to.
4. **`--concerns`**: when given, intersect with it. A named concern that does not qualify is reported as unavailable with its reason.

Before dispatching, present the resolved set, the concerns dropped for surface or cookbook reasons, and any requested concern (`--design`, `--concerns`) that cannot run. Architecture without design input is simply absent.

## Step 4: Dispatch the reviewers

Dispatch the resolved concerns **in parallel**, as multiple `Agent` tool calls in one message.

Each agent gets the brief shape of `pl-review-pr` "Dispatch", with:

- review id: `working`
- input path: `.me/review-pr/working.input.json`
- report path: `.me/review-pr/working.<profile>.report.<concern>.json`
- the `digestMarkdown` block from Step 2
- **the profile**: e.g. `profile: default`. Do NOT list the sibling concerns.
- the clause in item 10 of the `pl-review-pr` "Dispatch" brief, verbatim
- concern-specific context: the design artifact for `architecture`, the resolved cookbook paths for `patterns`, the touched subprojects' indexes and `digest.surface.testFiles` for `test`

Pass `model` only when `--model` was given; otherwise omit it, so each reviewer runs on the model its definition pins (`secrets`, `logic`, `test`, `patterns`: `claude-haiku-5-5`; `security`, `architecture`: `sonnet`; all at medium effort). Never raise the model on your own.

## Step 5: Merge and verify

```
node .claude/skills/pl-review-pr/scripts/merge-reports.mjs working --profile <profile> --concerns <the resolved set, comma-separated>
node .claude/skills/pl-review-pr/scripts/verify-report.mjs working --profile <profile>
```

**Always pass `--concerns` with the exact set dispatched and `--profile` with this run's profile**; without `--profile` the scripts read the `default` paths.

A missing per-concern report means that concern **did not run**, never "reviewed, found nothing". `merge-reports.mjs` exit 2 means no concern produced a usable report: surface it and stop.

`verify-report.mjs` applies the same anti-fabrication gate as the PR path: comments whose path is not in the diff, whose line falls outside a hunk, or whose `evidence` is missing, too short, not in the diff (`evidence-not-in-diff`), or from a different file (`evidence-not-in-this-file`) are dropped. Nothing is published.

## Step 6: Run the project's verification commands

If lint, build, or type-check have not run on this diff, run them and record the output. Subprojects without test suites gate on lint plus a successful build or boot.

| Stack | Gate |
|---|---|
| Go (repository root `.`, one module) | `golangci-lint fmt --diff`, `golangci-lint run ./...`, `go build ./...`, `go test ./...` |

**A declared `test` script is not a suite.** Never run a placeholder `test` script (npm's default `echo "Error: no test specified" && exit 1`, or a runner pointed at a directory or glob that matches no files) as a gate, and never report it as a passed check.

Report failures as findings. A failure that predates the diff is a pre-existing condition: surface it under the Healthy Baseline Rule (root `AGENTS.md`), never silently fix it.

## Step 7: Report

Print one consolidated report:

```
# Working-tree review — <the exact diff command>

**Files**: <count> files, <count> lines added / removed
**Destination**: <branch> (<upstream | branch type | default | --destination>)
**Concerns run**: <list>
**Concerns NOT run**: <list with the reason for each; omit the line when empty>
**Verdict**: <block | comment | approve>

## Observaciones
<the verified comments, grouped by file, each labelled with its concern>

## Descartadas por el verificador
<path, line, severity, reason, body preview — omit the section when empty>

## Filtradas por los reviewers
<count per concern of findings each agent dropped at its own emission filter>

## Verification
- Lint / Build / Type-check: <result per command, or "not run">

## What to do next
<blockers to fix before committing; warnings to fix or explicitly accept; out-of-scope findings for the PR description>
```

**Concerns NOT run** lists a concern that was dispatched and produced no report, and a concern the developer requested (`--design`, `--concerns`) that could not run. A concern skipped in Step 3 for lack of input is not listed there.

**The verdict gates the commit.** `block`: fix the blockers before committing. `comment`: fix every observation before committing; a warning blocks like a blocker. `approve`: nothing was raised. Never report `approve` when a dispatched or requested concern did not run; say which one and why. A concern skipped in Step 3 for lack of input never blocks `approve`.

Communicate in the `Docs language` root `AGENTS.md` declares; the Spanish labels above are the default wording. Keep identifiers, file paths, and this document structure in English.

## What this skill does NOT do

- Fix anything. The agents only `Write` their own report file.
- Nitpick. Style, naming, formatting and equivalent-alternative findings are filtered out inside the agents.
- Commit, push, stage, or open a PR.
- Post to GitHub or any external system. `post-comments.mjs` is never invoked from here, and `lib/gh.mjs` rejects the `working` id.
- Review a pull request: that is `/pl-review-pr <PR-id>`.
- Run the architecture concern without design input, or the patterns concern without a cookbook.
- Restate the agents' review rules. They live in `.claude/agents/<concern>-reviewer.md` and `.claude/references/reviewer-contract.md`.
