# Test anti-patterns - named vocabulary

Companion to `test-rules.md`: it names specific violations so reviewers cite defects by name.

**How to use this.** Cite anti-patterns by name in review comments, commit messages and
agent reports - "this is Overspecification, see test-anti-patterns.md" is a complete comment.
Do not paste the explanations; point at the name.

Principle: the more a test resembles the way the software is used, the more confidence it gives (Martin Fowler's [practical test pyramid](https://martinfowler.com/articles/practical-test-pyramid.html), Kent C. Dodds).

## Index

| Name | The tell |
|---|---|
| **Change-Detector Test** | Breaks on refactor, passes when a real bug is introduced |
| **Interaction Testing over State Testing** | Asserts *how* it was done, not *what* was produced |
| **Overspecification** | Coupled to collaborator signatures and exact call order |
| **Tautological Test** | Asserts a mock returned what it was told to return |
| **Weak Assertion** | Passes regardless of the behaviour under test |
| **Assertion Roulette** | Many undifferentiated assertions; a failure names no cause |
| **Mocking Internal Boundaries** | Mocks the collaborators an integration test exists to exercise |
| **Solipsistic Test** | Everything mocked; only routing syntax is left under test |
| **Mock Drift** | Mocks no longer match real behaviour; green test, broken production |
| **Tight Coupling to Presentation** | Asserts CSS classes, DOM structure, component internals |
| **Fragile Test** | Breaks on cosmetic or structural change with behaviour unchanged |

---

## The overarching one

### Change-Detector Test

A test that fails on refactoring but passes when a real behavioral bug (a mutant) is introduced.

Apply the refactor litmus from `test-rules.md`: rewrite the internals completely while keeping
observable behaviour identical. Every test must still pass. One that goes red is a
change detector.

Each anti-pattern below is one way to produce it.

---

## Unit level

### Interaction Testing over State Testing
Asserts which methods were called rather than what was produced or persisted, encoding the algorithm instead of the contract.

*Instead:* assert return values or resulting state.

### Overspecification
Binds tests to internal signatures and exact call sequences. Any internal reordering or refactoring causes failures even when behavior is unchanged.

*Instead:* assert through the public entry point only.

### Tautological Test
Asserts that a mock returned what the test configured it to return, or merely that a call
happened. It cannot fail for any reason connected to the production code.

*Example:* verifying that `updateOne` was called without validating that the payload included `{ status: 'inactive' }` (the actual defect).

*Instead:* assert the payload, persisted records, or observable output.

### Weak Assertion
Passes regardless of execution behavior (e.g. `expect(result.success).toBe(true)` when `success` is set unconditionally).

*Instead:* assert the specific values or side effects produced by the behavior.

### Assertion Roulette
Combines multiple undifferentiated assertions in a single test, obscuring which specific behavior failed (see `test-rules.md` rule 6).

*Instead:* maintain one assertion focus per test, or supply distinct assertion messages.

---

## Integration level

### Mocking Internal Boundaries
Stubbing internal services inside an integration test - the very seams the test exists to
exercise.

*Instead:* mock exclusively at system boundaries (external HTTP, clocks, randomness, third-party APIs) and execute internal services directly.

### Solipsistic Test
Every dependency mocked, so the test only proves the routing and wiring syntax is well-formed.

*Instead:* execute real collaborators against real or containerized datastores.

### Mock Drift
Mocks encode behaviour the real service no longer has. Tests stay green while production
breaks.

*Instead:* use contract testing or record/replay against real providers, updating recordings when providers change.

---

## End-to-end level

### Tight Coupling to Presentation
Asserts CSS classes, DOM structures, or component internals (`.should('have.class', 'btn-disabled-v2')`) instead of user-visible effects.

*Instead:* assert accessible roles, states (`:disabled`, `aria-*`), or visible text ("Processing..."). Non-visible attributes are not e2e concerns.

### Fragile Test
Fails on cosmetic or markup changes (styling refactors, class renames) while feature behavior remains identical.

*Instead:* select by role, label or text. Reserve `data-testid` for cases with no accessible
handle, and treat needing one as a mild accessibility signal.

---

## Two names used loosely in the wild

**"Mocking What You Don't Own"** (Freeman & Pryce) is the wrapping rule: do *not* mock third-party types
directly - wrap them in an abstraction you own and mock that. Use **Mocking Internal Boundaries** for the
integration-test defect above, and reserve the classic phrase for the wrapping rule.

**"Assertion Roulette"** (Meszaros, *xUnit Test Patterns*) denotes multiple indistinguishable assertions in one test where failures obscure the underlying cause. It is distinct from **Weak Assertion**, which refers to assertions incapable of meaningful failure. Tests can exhibit either, both, or neither.
