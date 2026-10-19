# Writing Go code

These rules apply to every Go file in the repository. Where they are silent, follow
[Effective Go](https://go.dev/doc/effective_go) and
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments). Before writing in a package,
read it and match its naming, structure and comment density.

## Design

- Prefer the straightforward solution. Add an abstraction when a second real use needs it, not
  in anticipation.
- Interfaces belong to the package that uses them and stay small, usually one or two methods.
  Functions accept interfaces and return concrete types.
- No mutable package-level state and no `init()` with side effects. Dependencies arrive through
  constructors.
- A new third-party module needs Héctor's approval before it is added to `go.mod`.

## Functions

- A function does one thing and fits on one screen, about 40 lines of logic.
- Exported functions validate their parameters first and return an error for invalid input.
- Every returned error is checked. Discarding one with `_` needs a comment above the line that
  says why it cannot fail or does not matter.
- The body of an `if`, `for` or `switch` case goes on its own line, never on the line of the
  condition.
- A composite or multi-step expression is assigned to a named variable before it is returned,
  so a debugger can stop on it. Return simple values directly.
- A condition whose meaning is not obvious is assigned to a named boolean first:
  `isPastDeadline := now.After(exchange.Deadline)`.
- Functions that do I/O or may block take `ctx context.Context` as their first parameter.
  Contexts are passed, never stored in structs.
- A goroutine is started only with a clear owner and a way to stop it, normally its context.

## Errors

- Errors carry context as they move up: `fmt.Errorf("draw exchange %s: %w", id, err)`.
- A module's public API exports the errors callers need to branch on, as sentinel values or
  types. Callers match them with `errors.Is` and `errors.As`, never by comparing strings.
- Validation collects every problem and returns them together with `errors.Join`, instead of
  stopping at the first.
- A state that can only be reached through a bug panics with a message that names the broken
  invariant. Everything a caller or user can cause returns an error.

## Naming

- Go conventions: `MixedCaps`, short receiver names, acronyms in one case (`ID`, `URL`).
- Names do not repeat their package: `giftexchange.Exchange`, not
  `giftexchange.GiftExchange`.
- Identifiers, comments and log messages use ASCII only, and no emoji.

## Comments

- Every exported identifier has a doc comment that starts with its name. Every package has a
  package comment.
- Comments go on the line above the code they describe, never at the end of a line. The
  exception is a `//nolint` directive, which Go tooling requires on the same line; it names the
  linter and gives the reason.
- Comment only what the code cannot say: a non-obvious invariant, a constraint, a surprising
  choice.
- Comments describe the code as it is. No issue numbers, dates, author names or history; git
  holds those.
- Pending work is marked `TODO(hvera): <what remains>`.

## Logging

- Use `log/slog` with structured fields.
- Levels: `Debug` for tracing, `Info` for lifecycle events, `Warn` for recoverable problems,
  `Error` for failures that need attention.
- Never log secrets, tokens or personal data. Never log who gives a gift to whom: assignments
  stay out of logs at every level.
