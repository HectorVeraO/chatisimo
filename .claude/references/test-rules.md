# Testing discipline (stack-agnostic rules)

These rules apply to every automated test suite in this repo.

## Test philosophy (the rule above all the rules)

Aligned with Martin Fowler's [practical test pyramid](https://martinfowler.com/articles/practical-test-pyramid.html). The core tenets every agent must internalize:

- **Test public behavior, contracts, and observable side effects.** What the caller sees, what the database/queue/API ends up containing, what the response envelope says.
- **Do NOT lock in internal implementation.** No spies on the unit under test. No `verify(mock).privateLooking()` chains. No assertions on call counts of internal helpers.
- **Mock at the boundary, not inside the unit.** External HTTP, database (when slice tests apply), the clock, randomness, message queues — yes. The collaborators inside the class you are testing — no.
- **One reason to fail per test.** Multiple unrelated assertions in one test give no signal when it goes red.
- **Keep tests simple.** No clever DSLs, no test-only inheritance hierarchies, no metaprogrammed assertions. Arrange, act, assert.
- **Tests must FAIL on the integration branch without your fix and PASS with it.** If both pass, the test is not testing your fix.
- **If a class is hard to test without spying on internals, that is a design signal**, not a testing problem. Extract a collaborator with a clear contract and test that.

**The refactor litmus.** Ask of every test: *if internals were rewritten completely (alternative helpers, reordered calls, restructured private logic) while preserving identical observable behavior, would this test still pass?*

- **Yes** -> it tests behaviour. Keep it.
- **No** -> it is a change-detector test. Delete it or rewrite it against the public contract.

Apply it to your own tests and when reviewing someone else's.

**Naming the failures.** `test-anti-patterns.md` (same directory) names each way this
philosophy gets broken - Change-Detector Test, Overspecification, Tautological Test, Mocking
Internal Boundaries, Tight Coupling to Presentation, and the rest. Cite them by name in review
comments and agent reports.

Every numbered rule below is a corollary of this philosophy. In any conflict, the philosophy prevails.

## 1. Test what behavior, not what shape

Do not write a test that only asserts "function X exists and returns a value". Tests must exercise the actual behavior:

- HTTP endpoints: invoke the endpoint, asserting status codes, body payloads, and side effects (database records, queue messages, logs).
- Pure functions: supply inputs exercising logic branches and error handling.
- UI components: render and interact, asserting user-visible output.

## 2. Import from actual source — never reimplement

Tests must import functions directly from source modules. Do not reimplement logic in test files ("shadow copies").

Anti-pattern:

```js
// BAD: copies logic into the test
function isValidEmail(s) { return /@/.test(s); }
test('isValidEmail', () => expect(isValidEmail('a@b')).toBe(true))
```

Correct:

```js
import { isValidEmail } from '../src/email'
test('isValidEmail', () => expect(isValidEmail('a@b')).toBe(true))
```

If functions resist direct testing (methods with side effects, deeply nested closures), extract core logic into exported pure functions and import them in both source and tests.

## 3. No hardcoded waits in E2E or integration

Await explicit conditions rather than arbitrary time intervals, using framework auto-retry primitives:

- Playwright: web-first assertions, `waitForURL`, `waitForResponse`, `expect.poll` (avoid `waitForLoadState('networkidle')`).
- Cypress: `cy.contains(...)`, `cy.get(...).should(...)`
- Selenium: explicit conditional waits, never `Thread.sleep`.
- pytest+httpx: poll using a short timeout and condition predicate.

A fixed wait (`waitForTimeout`, sleep promise) is allowed up to 120000 ms and only with a comment on the same line or the line immediately above stating why no condition can be awaited; in the e2e suite the `block-protected-paths` hook enforces this.

## 4. Negative and boundary cases are required, not optional

For every test that exercises the happy path, add at least one test that exercises:

- Invalid input (empty strings, null, oversized payloads, incorrect types).
- Boundary conditions (zero, one, maximum, off-by-one).
- Missing fields.
- Permission and authentication failures (when auth logic exists).

For security-sensitive paths, test bypass attempts: protocol-relative URLs (`//evil.com`), `NaN` values, oversized payloads, and double-encoded inputs.

## 5. Test data is fixtures, not code

Test inputs exceeding 5 lines belong in `fixtures/` or equivalent directories, keeping test bodies focused on assertions.

## 6. One assertion focus per test

Prefer multiple narrow tests over one wide one. Share setup via fixtures or `beforeEach`.

Exception: a single semantic assertion may require multiple `expect` calls (e.g. validating three required fields on a single object), representing one logical assertion across multiple statements.

## 7. Baseline before comparison

The Healthy Baseline Rule (see root `AGENTS.md`) requires evidence before a failure can be labeled "pre-existing". Two strategies for capturing baseline:

Both recipes are Bash (run them through the Bash tool). In PowerShell, create the folder with
`$OUT = "$(git rev-parse --show-toplevel)/.me/<slug>/logs"; New-Item -ItemType Directory -Force $OUT | Out-Null`
and compare with `Compare-Object (Get-Content "$OUT/baseline.log") (Get-Content "$OUT/after.log")`.

**Lazy with stash** (preferred for slow suites):

```
OUT="$(git rev-parse --show-toplevel)/.me/<slug>/logs"; mkdir -p "$OUT"
# Run verification on your work first
<verification command>            # suppose it fails

# Stash and capture baseline
git stash -u
<verification command> > "$OUT/baseline.log" 2>&1
git stash pop

# Diff: anything in the baseline file that ALSO appears in your post-change
# output is pre-existing. Anything new is yours.
```

**Upfront** (preferred for fast suites under ~30s):

```
OUT="$(git rev-parse --show-toplevel)/.me/<slug>/logs"; mkdir -p "$OUT"
# Before first edit
<verification command> > "$OUT/baseline.log" 2>&1

# After your changes
<verification command> > "$OUT/after.log" 2>&1

# Diff
diff "$OUT/baseline.log" "$OUT/after.log"
```

When evidence proves a failure is pre-existing, follow the surface-and-ask protocol from the Healthy Baseline Rule; never fix silently.

## 8. Run targeted tests, not the full suite

During development, run only the test files relevant to your change.

Where CI runs the full suites, they do not require local agent gating. Where no CI runs them, root `AGENTS.md` "Verification before push" names what runs locally before pushing.

When making cross-cutting infrastructure modifications (test helpers, shared mocks, test runner configurations), execute the full suite locally before pushing.

## 9. Test fixtures and test apps live in the test tree, not the example tree

`tests/fixtures/` is for test inputs. `examples/` (if present) is for user-facing demos. Do not pollute examples with test pages.

## 10. When a test passes accidentally, it is broken

Before verifying a test as passed:

- Comment out the production code under test. Does the test still pass? If yes, the test is not testing the thing.
- Inverse the assertion (`expect(true)` -> `expect(false)`). Does the test fail? If no, the test runner is not running this assertion.

## 11. Mocking the unit under test is forbidden

Mocks belong at the boundary (databases, external HTTP, time, randomness), never on the function under test.

## 12. Web changes ship a happy-path e2e

If the repo has an end-to-end suite, a change to a web frontend (a user-visible UI subproject) ships a happy-path e2e — new for a new flow, updated for a changed one. The happy-path test asserts what the user can see and do through the changed flow, end to end.

If a web change is delivered WITHOUT a happy-path e2e, the developer records the justification in the PR description (and commit) — for example, the flow is not yet reachable in the test environment, or the suite cannot exercise it.

Keep happy-path e2e tests concise and behavior-driven at the top of the test pyramid; negative and boundary tests belong in unit and integration layers (rule 4).

## What goes into the per-subproject AGENTS.md

Each subproject `AGENTS.md` `## Verification` section links this file and documents: exact test commands, change-to-test mappings, fixture locations, and coverage tooling.
