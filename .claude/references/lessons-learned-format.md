# Lessons Learned entry format

Every Lessons Learned entry and lesson proposal follows this format, so agents can grep for an entry that matches a current symptom.

## Entry template

```markdown
### <one-line title in imperative or symptom-first form>

- **Symptom**: what the developer / user / production system observed (one or two sentences)
- **Root cause**: the actual underlying issue (one or two sentences, no hand-waving)
- **What to check**: concrete files, functions, or patterns to look at when this symptom appears (bullet list)
- **Reference**: link or path to the PR / commit / issue where this was learned (optional, prefer linking)
```

## Example (from a hypothetical multi-tenant API)

```markdown
### Cross-tenant data leak when `query.company` is omitted

- **Symptom**: User from company A receives records owned by company B in a list endpoint response
- **Root cause**: Controller built the Mongo query from `req.query` directly without forcing `query.company = req.session.company`, so the filter inherited only the URL params
- **What to check**:
  - Every controller in `controllers/*.controller.js` calls `query.company = req.session.company` before any `find` / `findOne`
  - After loading a single record, the controller verifies `item.company !== req.session.company` and throws `RESOURCE_ANOTHER_COMPANY`
  - Branch isolation: non-admin sessions also pin `query.branch = req.session.branch`
- **Reference**: issue #17, PR #42
```

## Style rules

- **Symptom-first titles** when the symptom is the searchable thing. Otherwise an imperative.
- **No prose paragraphs**. Bullets and short sentences.
- **Concrete file paths in "What to check"**. A vague rule ("be careful with sessions") does not qualify.
- **Past tense for the root cause**, present tense for "what to check".
- **Reference is optional but preferred**. Mark a lesson from team memory with `(team memory)` instead of a link.

## The bar

An entry qualifies only when it is durable (it will recur), non-obvious (no linter, type check or existing rule catches it) and checkable (2-3 concrete checks). Typical sources:

- A bug shipped to staging / production and required an urgent fix
- A reviewer flagged the same issue twice across different PRs
- A regression that an existing rule should have prevented
- A subtle invariant violated and discovered later (e.g., audit log skipped, immutable headers mutated, race condition on a Mongo cursor)

Never an entry for:

- Style preferences with no behavioral impact (the linter handles those)
- One-off mistakes made under time pressure
- Behavior of external libraries documented upstream (link the upstream doc instead)
- Speculation ("we might want to watch for X"); wait until X happens

## Where entries live

1. **Proposals** go to `.claude/references/lesson-proposals.md`, below its sentinel. `/pl-draft-lesson` writes them there; agents do not treat them as rules.
2. **Promoted entries** go to the `## Lessons Learned` section, below its "Add entries below this line" sentinel and outside any AUTOGEN marker, of:
   - the relevant subproject `AGENTS.md` when the lesson is subproject-specific;
   - root `AGENTS.md` when it is cross-cutting (list the affected subprojects in "What to check").

Promotion is an explicit developer decision, one proposal at a time. The entry moves verbatim and leaves the proposals file.

### When the root section outgrows the line budget

Root `AGENTS.md` has a line budget (default 400, enforced by `.claude/scripts/validate-artifacts.mjs`). Older cross-cutting entries then move, verbatim and in the same format, to `.claude/references/troubleshooting.md`. Root keeps, below the sentinel, only the entries still in active use plus one line naming the moved titles. An entry is never deleted to meet the budget: it moves. Subproject `AGENTS.md` files follow the same rule with their own `cookbooks/` or a subproject-level troubleshooting file.
