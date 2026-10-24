# PR Description Format & Guidelines

Detailed guide, full template, section requirements, and a worked example for this repo's pull request descriptions.

Passive reference, reached from root `AGENTS.md` "PR description format" (and read during `/pl-address-issue` PR preparation, `/pl-address-review`, or manual PR drafting). The rules embedded in root `AGENTS.md` remain the active contract.

---

## 1. Purpose & Reviewer Expectations

A reviewer reads the PR description before opening the diff.

### Core Principles
- **Conventions, Not Mechanical Blocks**: Sections like `## Declared change` and `## Pre-existing fixes` are conventions; no reviewer fails a PR because one is absent. A developer is not blocked on writing the PR description.
- **Silence vs False Claims**: Reviewers flag sections that assert changes absent from the diff. Truthfulness and alignment with the actual diff are mandatory.
- **Evidence-Based Verification**: The `## How It Was Tested` section is inspected against strict standards: only real, executed commands and observable outputs may be claimed.

---

## 2. Standard PR Description Shape

```markdown
## Title & Issue Link
`<type>(<scope>): <Imperative subject>`
Closes #<issue>

## What Changed
* <change 1: which component / endpoint / module and what it does now>
* <change 2>

## Why It Changed
<The problem, the need or the trigger. Business or technical context.>

## How It Was Tested
1. `<executed command>` -> `<real observed output>`
No verificado: <what> - <why not>

## Declared change
- <class>: <what changes and which consumers it affects>

## Pre-existing fixes
- <path>: <one-line description> (authorized during work on #<issue>)

## Visuals & Context
* <link to a real, existing artifact, or N/A with the reason>

---
_Description drafted by `<skill>` (<model>) and reviewed by <account> before publishing._
```

### Language Rules
- **Section Headings**: Fixed English tokens, never translated. Reviewers and automated tooling match on them literally.
- **Section Content**: Follows the `Docs language` root `AGENTS.md` declares. `No verificado:` is a fixed token like the headings.

---

## 3. Section-by-Section Details

### Title & Issue Link
- The backticked line is the **actual PR title**, matching the commit subject line convention: `<type>(<scope>): <Subject>`, using full type words (`feature`, not `feat`), imperative mood, capitalised, no trailing period.
- `Closes #<issue>` names the issue the PR resolves; GitHub links it and closes the issue on merge. Use `Refs: #<issue>` for an issue the PR touches without resolving it. When the work genuinely has no issue, write `Issue: none - <reason>`. Never invent a number or link an unrelated issue.

### What Changed
- Two to six bullet points, one per logical unit of change, naming the component, endpoint, or module and what it now does.
- Not a file list. With more than six bullets, split the PR or acknowledge it as composite.

### Why It Changed
- Two to five lines of prose.
- Explains the underlying problem, user impact, business rationale, or technical trigger.
- Tells the reviewer what "correct" means for this change.

### How It Was Tested (Executed Steps Only)
- **Every step listed must be one that was actually performed, with its actual result.**
- A step someone intends to run, or believes would pass, does not belong here. Write the executed command and observed output:
  ```
  1. `npm run test -- checkout.test.ts` -> 14 passed, 0 failed
  2. Quick double click on "Pay" with Slow 3G -> exactly one request to /api/v1/payments
  ```
- **Unverified items**: Anything not verified MUST be declared under an explicit line:
  ```
  No verificado: <what> - <why not>
  ```

### Declared change (when a designated surface is touched)
- Include whenever the PR touches a surface other teams or consumers depend on (goose migrations under `internal/<name>/internal/store/`, a module's public API package (`internal/<name>/`), the import rules in `cookbooks/architecture.md`).
- Counterpart to the `Declared-change:` commit trailer.
- Omit this section entirely when no designated shared surface is touched.

### Pre-existing fixes (when the PR fixes something outside the issue's scope)
- Include when the PR fixes something outside the issue's scope to reach a healthy baseline under the Healthy Baseline Rule.
- Counterpart to `chore(unscoped):` commits.
- Omit this section entirely when the PR is fully in-scope.

### Visuals & Context
- Screenshots, recordings, design links, dashboards, or traces.
- **Link only real artifacts that exist**. Never construct placeholder or example URLs.
- `N/A` requires an explicit reason: `N/A - backend logic change, no visual surface`.

### AI-Generation Disclosure
- Mandatory one-line italic footer on any PR description drafted by an AI agent:
  ```markdown
  ---
  _Description drafted by `/pl-address-issue` (claude-sonnet-4-5) and reviewed by <account> before publishing._
  ```
- Must name the model, not just the harness or skill.

### Opening the PR
Write the body to `.me/<slug>/pr-body.md`, then open the PR against the base branch (same command in Bash and PowerShell):
```
gh pr create --base <base-branch> --title "<title>" --body-file .me/<slug>/pr-body.md
```

---

## 4. Worked Example

```markdown
## Title & Issue Link
`fix(checkout): Prevent duplicate payments on double click`
Closes #17

## What Changed
* `PaymentButton.tsx` disables the button on the first click and shows a loading state.
* `processPayment()` is wrapped in an 800 ms debounce.

## Why It Changed
On slow mobile networks users could tap "Pay" several times before the
redirect, creating duplicate Stripe charges and support requests.

## How It Was Tested
1. `npm run test -- checkout.test.ts` -> 14 passed, 0 failed
2. Checkout with Slow 3G throttling in DevTools, quick double click on "Pay"
   -> a single request to `/api/v1/payments` in the Network tab
3. `npm run lint src/checkout` -> no warnings
4. `npm run test:e2e -- checkout.spec.ts` -> 1 passed (double-click payment happy path)

No verificado: Safari iOS behaviour - no device available in this session.

## Visuals & Context
* Video of the `checkout.spec.ts` spec, linked from the resolution comment on #17: the button stays disabled with a loading state after the first click.

---
_Description drafted by `/pl-address-issue` (claude-sonnet-4-5) and reviewed by <account> before publishing._
```
