# Writing tests

Tests describe what the code does for its callers, never how it does it. The check for every
test: if a module's internals were rewritten completely and its behavior stayed the same, would
the test still pass? If not, rewrite it against the public API or delete it.

## What to test

- Test a module through its public API, from an external test package
  (`package giftexchange_test`). Go then refuses access to anything unexported, which keeps the
  tests honest.
- Assert outcomes: returned values, returned errors, and what ends up in the database.
- Every happy-path test has companions for invalid input, missing fields and boundaries: zero,
  one, the maximum, and one past it. Permission failures are tested wherever permissions exist.
- A bug fix comes with a test that fails without the fix and passes with it.

## Doubles

- Replace only the boundaries: the clock, the source of randomness, Discord, and other
  external services. Never replace the code under test or its collaborators inside the module.
- Doubles are small hand-written fakes that satisfy the module's own interfaces.
- Code that talks to Postgres is tested against the real database from `compose.yaml`, not a
  fake.

## Shape

- Use the standard `testing` package. A test helper library is a new dependency and needs
  Héctor's approval.
- Use table-driven tests with `t.Run` for variations of one behavior. Each case has a name that
  states the behavior, such as `"rejects an exchange with no participants"`.
- One behavior per test, so a red test points at one cause.
- Arrange, act, assert. No custom assertion frameworks or test-only inheritance.
- Input data longer than a few lines lives in `testdata/` next to the test.
- Never reimplement production logic in a test; import it.
- Never wait with `time.Sleep`. Inject the clock, or wait on an explicit condition.

## Running

- While working, run the package you changed: `go test ./internal/giftexchange/...`.
- Before opening a pull request, run `go test ./...` and `golangci-lint run ./...`. CI also
  runs the suite with `-race`.
- Before trusting a new test, break the code it covers and watch it fail.
