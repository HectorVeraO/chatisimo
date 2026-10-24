# Architecture, workflow and conventions: full root text

Passive reference, reached from root `AGENTS.md` "Healthy Baseline Rule", "Library documentation lookup" and "Anti-degradation governance". It holds the full text behind condensed root sections and any section the owner moves out of root to stay within the line budget. Root stays the active rule; where the two disagree, fix both in the same edit.

---

## Healthy Baseline Rule

You begin work assuming the source branch is GREEN: tests pass, lint clean, build succeeds, type-check clean.

### When verification fails after your changes

1. **Failures are YOURS unless proven pre-existing.** No "pre-existing" claim without evidence.

2. **Acceptable evidence** (one of the following):
   - Captured baseline output before editing, with the failing case appearing identically.
   - An open issue or known-flake annotation in the repo.
   - Red CI status on the source branch (or latest integration merge) for the identical case (cite the run link).

3. **Without evidence, fix it.** It is yours.

### When evidence DOES prove pre-existence: STOP and ASK

You do NOT decide whether to fix a pre-existing failure. The developer does. Surface the failure using this format and await an explicit decision:

```
PRE-EXISTING FAILURE [n/N]
- Check: <test/lint/build name>
- Evidence: <baseline run | issue number | CI run link>
- Drift if fixed: <N> file(s) outside scope, <low|med|high> risk, <small|medium|separate-PR>
- Not investigated. Recommendation: <defer | investigate first | fix (only if trivial)>

Decision options:
  (a) fix now in this PR
  (d) defer; will file a follow-up issue
  (n) defer; mention in PR description only
  (i) investigate further before deciding

Default if no answer (headless mode): (n)
```

Rules for the surface:

- **Surface ASAP, before investigating.** Offer "investigate first" only when needed; the developer decides.
- **Default recommendation: defer.** Recommend "fix" only when the fix is trivially small in a file already being modified.
- **Default action when no answer: defer with PR note (n).** Never silently fix.
- **Apply same flow if YOUR change exposed a pre-existing bug** (a test now reaches a previously unreachable broken path, lint catches a long-existing violation, etc.). Treat it identically.

### Authorization marker (required when developer says "fix")

If the developer authorizes (a), the off-scope fix MUST carry an authorization marker in BOTH places:

**1. Commit message** with `chore(unscoped):` prefix, following the project commit message format (`unscoped` is a legal scope, not an exception):

```
chore(unscoped): Fix <the pre-existing failure>

Authorized during work on #<issue> to reach a healthy baseline.
Outside the issue's scope.

Refs: #<issue>
Co-Authored-By: <Model> <model noreply address>
```

The `Refs:` trailer names the issue the work belongs to; drop it when there is none.

The off-scope fix lives in its own commit, not bundled with the in-scope work.

**2. PR description** with a `## Pre-existing fixes` section:

```
## Pre-existing fixes
- <path>: <one-line description> (authorized during work on #<issue>)
```

The marker is a convention, not an automated gate: reviewers do not emit findings if it is omitted.

### Baseline capture strategy

Two valid strategies. Pick based on suite duration:

- **Lazy with stash** (preferred for slow suites): capture baseline only if verification fails after changes. Stash changes (`git stash -u`), run verification on the clean source branch, capture output, unstash (`git stash pop`), and compare.
- **Upfront** (preferred for fast suites, under ~30 seconds): run verification before editing, capture output, and compare after.

Either strategy provides acceptable evidence.

## Agent discipline

The full text lives in `.claude/references/agent-discipline.md`, copied verbatim from the scaffolding skill; root carries the short form. It is not duplicated here.

## Library documentation lookup

When you need authoritative, version-specific API docs for a library this repo uses, query the **context7** MCP, not memorized snippets.

- **Wired in**: see `.mcp.json` at the repo root. Claude Code prompts once for approval on the first session in this project; approve it and it stays connected. `.claude/settings.json` pre-allowlists the tool names, so individual lookups do not prompt.
- **Workflow**: call `resolve-library-id` to find the slug, then `query-docs` with the topic you care about. Older context7 versions name this tool `get-library-docs`.
- **Strong default**: if a change touches a library API and you have not opened the project's source for that area in this session, query context7 before writing the code.
- **When NOT to call it**: trivial questions, things already documented in this repo's docs, anything you can confirm by reading the project's own source.
- **Higher rate limits (optional)**: set `CONTEXT7_API_KEY` in your shell (or in `.claude/settings.local.json` env) for paid-tier limits.
- **Fallback: WebSearch on official docs, never blogs / SO / random gists.** If context7 returns "library not found" or the topic is too narrow for it, fall back to WebSearch scoped to the vendor's official documentation domain (`site:` operators). The official-doc table for this project's stack:

| Stack | Official docs |
|---|---|
| Go | `go.dev`, `pkg.go.dev` |

  Cite the source URL whenever you base a code decision on it. **Stack Overflow, Medium, dev.to, random blogs, gists and old GitHub issues are not authoritative.** If the only "answer" you find is on those, surface that uncertainty to the developer before writing code.

## Anti-degradation governance

The root `AGENTS.md` budget is **<= 400 lines**, counted as `wc -l` counts them, and `node .claude/scripts/validate-artifacts.mjs` reports a WARN (`root-line-soft-budget`) above three quarters of it, a WARN (`root-line-tolerance`) above it up to the 10% tolerance (budget * 1.10, rounded down), and an ERROR (`root-line-budget`) past the tolerance. The budget is never above 500; a higher value is itself an ERROR (`root-line-budget-max`). Each subproject index (`<subproject>/AGENTS.md`) and each cookbook fragment (`<subproject>/cookbooks/*.md`) has a budget of **<= 120 lines**, reported as a WARN (`index-line-budget`, `cookbook-fragment-budget`).

- **Placement**: root holds project-wide rules and navigation: what each subproject is, where to read next, and rules that apply across subprojects. Anything specific to one directory lives in that subproject's index or cookbook. A subproject index routes each task to one fragment through its "Read before" table (`| Task | Fragment |`), kept outside AUTOGEN markers; every fragment is routed and every row names a fragment that exists. A fragment over budget is split by task, never trimmed.
- **Generated references are never hand-edited**: every file listed in `.claude/references/.generated.json` is emitted by the scaffolding skill, which records there the sha256 of what it wrote (computed with every CR byte removed, so an LF or CRLF checkout hashes the same). The validator WARNs `generated-reference-edited` when a listed file differs or is missing. Repo-specific facts go to an `AGENTS.md`; a wrong generic rule is fixed in the skill and regenerated.
- **Rule purity**: root and every reference declare active invariants only: schemas, closed vocabularies, regexes, failure criteria. No historical chronicles ("formerly X, now Y"), debate logs or conversational quotes. Transitions belong in git commits and PR descriptions; deliberations belong in ADRs/RFCs. Full rule: `.claude/references/agent-discipline.md`.
- **Author** (whoever edits root): keeps normative rules in root. Operational detail (runbooks, measured inventories, command catalogues, long examples) and history go to `.claude/references/`, leaving at most a one-line pointer in root. A new reference gets that inline pointer in the root section that uses it; only a reference no root section points at gets a row in root's "Passive references on demand" table, with the task that triggers reading it.
- **Hand edits go outside AUTOGEN markers**: a regeneration discards whatever is inside one (see the markers section below).
- **Validator** (`node .claude/scripts/validate-artifacts.mjs`): enforces balanced and unique AUTOGEN markers, no heading duplicated across a marker, the AI-generation disclosure in the commit and PR formats, and the root line budget, and warns on the soft root, index and fragment budgets, unrouted fragments and dangling routing rows, and edited generated files.
- **Owner** (the team member who owns the agent docs): approves every move between root and a reference. **Detail is never deleted to meet the budget: it moves**, verbatim, to the reference whose row matches the task that needs it. Where no reference fits, it goes below the "Moved from root" sentinel at the end of this file.

Where moved content goes:

| Content | Destination |
|---|---|
| Full text of a condensed root section | this file |
| Historical Lessons Learned entries | `.claude/references/troubleshooting.md` |
| Full Code Path Parity rows, with hash commands | `.claude/references/code-path-parity.md` |
| Anything else operational | this file, below the sentinel, under the root heading it came from |

### `AGENTIC-SETUP:AUTOGEN` markers: what they mean now

A matched pair of `AGENTIC-SETUP:AUTOGEN` HTML comments (one suffixed `START`, one `END`, both naming the same block) marks a region the scaffolding skill may regenerate, **discarding whatever is inside**.

The marker syntax is described here, not quoted: `validate-artifacts.mjs` and `pl-validate-agent-docs` count a literal example as a real marker. Read a real one in root `AGENTS.md` for the exact form.

- **Content that is a measured fact about this repo does not live inside a marker.** Root emits "Branch types", "Verification before push", "Cross-cutting patterns", "Scratchpad" and "Per-subproject guidance" without markers for that reason, and each subproject index emits its "Read before" routing table the same way. When you correct a block that is still wrapped and the correction is a measured fact, remove its two marker comments in the same edit.
- **Markers stay only where the content really is a template**: the overview, the policy blocks copied from the scaffolding skill's canonical formats, and the `*-header` blocks that hold nothing but a pointer to a format reference.
- **Both "Add ... below this line" sentinels are load-bearing** and stay: Lessons Learned entries and Code Path Parity rows go *below* them, outside any marker.
- **Opting out of regeneration**: a section whose heading is present but whose marker pair was removed is hand-owned; a re-run diffs it and rewrites only a sentence that contradicts a current generated rule, replacing it with that rule and reporting the rewrite. A section with neither heading nor markers is emitted again. Conditional sections are re-evaluated on every run.
- **A regeneration is not idempotent against these files.** If you run the scaffolding skill again, diff the result and keep the hand-authored blocks.

---

## Moved from root

<!-- Add entries below this line: moved sections, each under the root heading it came from. The skill only appends below (a new heading, skipped when it exists) and never edits existing entries. -->
