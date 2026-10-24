# Agent discipline

Full text of Agent discipline rules; root carries the summary form. The summary for rule purity lives in root "Anti-degradation governance".

## Verify by reading; do not infer from absence

When you observe an asymmetry (such as a spec referencing a method missing from a class, a failing "should create" test, or a branding term absent from code), first **read the relevant source files in full and grep for the symbols involved**. Do not classify code as "orphaned", "abandoned", "missing", "half-built", or "should be deleted" based on surface signals.

A spec referencing `component.foo()` where `foo` is not defined can mean:

- The spec is orphaned (no traces in surrounding code) -> safe to delete the spec.
- The feature is planned but not built (surrounding code is healthy) -> the spec is a contract for an add-on requiring implementation.
- The method was renamed -> search the codebase for the new name; update the spec.
- The component was refactored -> the method moved into a child component or service; trace imports.

Each case needs a different action. Decide between them by reading the component, grepping for related symbols and tracing usage, not by pattern-matching ("the method is not here, so it is orphaned").

## Do not infer scope from naming or branding

Same discipline applies to scope-defining moves: deletions, restorations, package moves. If you are about to propose "delete X because of Y" (rebrand, rename, "dead" feature), first read X's call sites and verify Y is the actual goal. A rename is not a licence for feature surgery. When the user gives one instruction (rename, copy, refactor) and you find yourself proposing a list of related deletions, surface the list as a separate question rather than bundling it into the original ask.

## When the user pushes back, re-read

If the user questions your analysis ("are you checking the code?", "what about X?"), re-read the source before responding. Do not simply restate prior framing in different words. Cite the specific file and line re-read.

## Separate "wired" from "unwired" cleanly

When describing a partial implementation, state two distinct facts:

- "The surrounding feature is wired and works X way" (with file:line evidence).
- "This specific add-on is not yet built" (with the spec or contract that defines it).

Do not collapse them into "the feature is broken / half-built / orphaned".

## Rule purity: state invariants, not deliberation history

When documenting rules, architectural guidelines, or subproject conventions in `AGENTS.md`, cookbooks or `.claude/references/`, write purely in present-tense declarative form: schemas, closed vocabularies, regexes, and failure conditions.

- **No historical narratives**: State rules directly (never write "formerly we did X, but after encountering bug Y we decided to switch to Z"; state Z directly).
- **No conversational quotes**: Never quote human conversations, meeting transcripts, or chat dialogues ("as the user noted...").
- **Clear separation**: State transitions belong in git commit messages and PR descriptions (`Formerly X, now Y`). Deep architectural rationale and trade-off deliberations belong in project ADRs/RFCs. Rule files hold only the active machine contract.

## Placement: where a rule lives

- **Root** holds project-wide rules and navigation: what each subproject is, where to read next, and rules that apply across subprojects. Anything specific to one directory lives in that subproject's index or cookbook.
- **A subproject index** (`<subproject>/AGENTS.md`) holds commands, the file map, verification, a pitfalls summary and the "Read before" table that routes each task to one cookbook fragment. **A fragment** (`<subproject>/cookbooks/<topic>.md`) holds the detail for one task. A new fragment gets its routing row in the same edit.
- **Generated references are never hand-edited.** A file listed in `.claude/references/.generated.json` belongs to the scaffolding skill. Repo-specific facts go to an `AGENTS.md`; a generic rule that is wrong is fixed in the skill, then regenerated.
