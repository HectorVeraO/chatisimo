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
  one, the maximum, and one past it.
- A bug fix comes with a test that fails without the fix and passes with it.

## Security tests

v1 has no inbound HTTP, so these tests are its security tests: the attack surface is Discord
interactions and the data they reach. Every module and adapter change includes each category
that applies to it.

- Authorization: every operation that has permissions has a test where a caller without them is
  denied and nothing changes.
- Input abuse: tests with very long input, unusual Unicode (combining marks, right-to-left text,
  zero-width characters, emoji), mentions, IDs that belong to another guild or person, and
  repeated or out-of-order interactions. Every parser of user input also has a native Go fuzz
  test (`func FuzzParseX(f *testing.F)`) seeded with these cases, which asserts that the parser
  never panics and returns either a valid value or an error.
- Assignment confidentiality: no giver-recipient pair appears in logs, errors, or any reply
  except the one to the giver. Assert on the captured logs, the returned errors and every reply
  the Discord fake receives.

## Test data

- Test data is synthetic. Email addresses use the RFC 2606 domains (`example.com`,
  `example.org`, `example.net`). Discord IDs are fixed fake snowflakes, defined once as
  constants in the test package and reused.
- Never use a real guild, user or person, and that includes `testdata/` and recorded payloads.

## Doubles

- Replace only the boundaries: the clock, the source of randomness, Discord, and other
  external services. Never replace the code under test or its collaborators inside the module.
- Doubles are small hand-written fakes that satisfy the module's own interfaces.
- Code that talks to Postgres is tested against the real database of the local stack
  (`.deploy/local/docker-compose.yaml`), or the Postgres service container in CI, not a fake.

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
