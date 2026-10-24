---
name: pl-validate-agent-docs
description: "Validate every `AGENTS.md` against reality: runs the artifact validator, then checks referenced files, `~L<n>` landmark drift, marker-trapped content, Lessons Learned and proposal references, and Code Path Parity rows. A manual sweep; reports and suggests fixes, applies none."
argument-hint: "[subproject-path]"
---

# pl-validate-agent-docs

Validate the `AGENTS.md` documentation set of **Chatisimo** against the code it describes:
renamed files, moved landmark lines, parity rows that outlived their duplication.

`$ARGUMENTS` (optional): subproject path to restrict validation. When empty, validates all `AGENTS.md` files in the repository.

Output only. This skill applies no changes.

## Step 0: Run the artifact validator

```
node .claude/scripts/validate-artifacts.mjs
```

It checks frontmatter, agent and skill registration, hook wiring, AUTOGEN marker balance and duplicates, leftover tokens and maintenance
comments, and the root line budget. Record its error and warning counts and every finding verbatim for
the report. Exit 3 means at least one ERROR; exit 1 means the script itself failed: say so, and
continue with the steps below either way. Do not re-derive by hand what it already checked.

## Step 1: Discover

Find every `AGENTS.md`:

```
git ls-files | Select-String '(^|/)AGENTS\.md$'     # PowerShell
git ls-files | grep -E '(^|/)AGENTS\.md$'           # bash
```

Cross-check results against subprojects listed in root `AGENTS.md` under "Per-subproject guidance": any listed subproject missing an `AGENTS.md` is reported as a finding.

Include every cookbook fragment at `<subproject>/cookbooks/*.md`, which contain similar file references, and check each subproject's "Read before" table: every fragment is routed and every routed path exists (the validator's `cookbook-unrouted` / `cookbook-route-missing` WARNs).

## Step 2: Per-file checks

### 2a. Referenced files exist

Extract file references using a Markdown-aware scan (backticked paths such as `controllers/bot.controller.js`, paths in code blocks, and path-like table cells). For each:

- Verify the file exists at the target path.
- Flag missing files: "AGENTS.md references `<path>` which does not exist".

Ignore generic placeholders (`<resource>`, `<feature>`, `<subproject>`); template rows are not broken references.

### 2b. Line-number drift

Extract `~L<number>` references (e.g. `definitions/configuration.js ~L358`). For each:

- Read the referenced file at and around that line
- Heuristic: if the cited line and the surrounding 5 lines contain none of the keywords or symbols
  the sentence describes, flag drift
- Suggest a likely new line number when the same keyword appears nearby

### 2c. AUTOGEN markers

Balance and duplicates come from Step 0. Add only what its tolerant match accepts: a marker whose
spacing differs from `<!-- AGENTIC-SETUP:AUTOGEN:<name> START -->` byte for byte, or whose name
differs only in case from another marker's (names are **case-sensitive**, so a lowercased marker is a
second, unpaired section).

### 2d. Content trapped inside a marker

A matched marker pair marks a region the generator may regenerate, **discarding whatever is inside**.
A measured fact about this repo does not live inside a marker (`.claude/references/architecture.md`,
"`AGENTIC-SETUP:AUTOGEN` markers: what they mean now").

Inspect the body of each marked block and flag it as an **unwrap candidate** if it contains hand-measured content rather than template text. Any single indicator suffices:

- Cites a concrete `file:line` or `~L<n>` landmark
- Contains a table of measured values (environment variables, ports, timings, test counts)
- States a repro, a gotcha, or a root cause — "X is fatal because Y", "verified: `<command>` -> result"
- Names a specific service, branch, issue number or version that no template could have produced
- Asserts something is absent or broken as a finding ("`<command>` does not exist")

Report format: "`<file>`: block `<name>` contains hand-measured content (<the signal>) — candidate for unwrapping; a regeneration would discard it."

Do **not** unwrap anything. The decision to remove a marker pair belongs to the developer, and the
removal happens in the same edit as the next correction to that block.

Markers legitimately stay where the content really is a template: the `overview` block, the root
policy blocks, the lean `commit-message` / `pr-description` / `local-environment` templates, and the
`*-header` blocks that hold only a pointer to a format reference. The `Add entries below this line.
The skill never edits below.` / `Add parity rows below this line.` sentinel comments are not markers;
leave them alone.

### 2e. Lessons Learned and proposals freshness

For each Lessons Learned entry across root `AGENTS.md`, subproject `AGENTS.md` files and `.claude/references/troubleshooting.md`, and each proposal in `.claude/references/lesson-proposals.md`:

- Validate References (PR URL or issue number): verify PRs via `gh pr view <id> --json url` and issues via `gh issue view <n> --json url`. Report failures without retrying.
- Flag entries older than 12 months for review.
- Flag proposals older than 3 months as waiting for a promote-or-delete decision by the developer.

### 2f. Code Path Parity validity

For each parity row across subproject `AGENTS.md` files and `.claude/references/code-path-parity.md`:

- Verify every listed file exists
- Read each pair and check the duplicated logic is still present (heuristic: the same function names
  and patterns appear in each)
- Flag rows where the duplication has been removed: the parity entry is obsolete

## Step 3: Report

Group by file:

```
# AGENTS.md validation

## Artifact validator
- <errors> errors, <warnings> warnings (exit <code>); each finding verbatim

## <path>/AGENTS.md

### Missing files (<count>)
- referenced `<path>` not found

### Line-number drift (<count>)
- `<file> ~L<old>` likely now near L<new> (keyword: <keyword>)

### AUTOGEN markers
- All balanced
- (or) Unbalanced: <name> missing END at line <n>

### Marker-trapped content (<count> unwrap candidates)
- block `<name>`: hand-measured (<the signal>) — candidate for unwrapping; a regeneration would
  discard it

### Lessons Learned
- <count> entries, oldest <age>, <count> with broken references
- <count> proposals, <count> older than 3 months

### Code Path Parity
- <count> rows, <count> with missing files, <count> obsolete (duplication removed)
```

Report measured counts, never an impression. "Looks fine" is not a result.

## Step 4: Suggest fixes

For each finding, state the concrete fix: update the path, drop the obsolete row, refresh the line
number, restore the missing END marker. Output only — do not apply.

## What this skill does NOT do

- Modify `AGENTS.md` files. Fixes are applied by developers.
- Review agent definitions in `.claude/agents/`, skills in `.claude/skills/`, or hooks in `.claude/settings.json` beyond what the Step 0 validator reports. The rest of the sweep covers documentation only.
- Access remote systems beyond read-only reference checks (`gh pr view <id>` and `gh issue view <n>`). Report failures without retrying.
- Run on a schedule. It is a manual sweep, monthly or before a major refactor.
- Promote or delete lesson proposals. That is the developer's decision.
