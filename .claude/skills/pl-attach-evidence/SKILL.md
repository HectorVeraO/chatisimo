---
name: pl-attach-evidence
description: "Post the resolution comment with links to the agent's evidence on a GitHub issue, and set its closing status (board card to Done, or close the issue), after one combined preview and one yes, or on the /pl-address-issue closing answer when delegated. Never edits, labels or deletes issues."
argument-hint: '<#N> [--summary "<text>"] [--no-comment] [--comment-only] [--attach <path|glob> ...] [--run <run-id>] [--close]'
---

# pl-attach-evidence

Collect the agent's evidence, link it, and post it with a resolution **comment** on `$ARGUMENTS` (a GitHub issue), then set the issue's **closing status**. These are the only issue writes this skill performs. Every write runs only after the developer's explicit yes: the Step 6 preview when standalone, the `/pl-address-issue` closing question when delegated. Deleting an issue is denied in settings.

`$ARGUMENTS` is parsed as: `<#N> [--summary "<text>"] [--no-comment] [--comment-only] [--attach <path|glob> ...] [--run <run-id>] [--close]`.

Two invocation paths:
- **Standalone** by a developer: `/pl-attach-evidence #<N> [--attach ...] [--close]`, or the same request in words.
- **Delegated** by `/pl-address-issue` (Step 11b), which stages artifacts into `.me/<slug>/evidence/` and passes the writes the developer chose after seeing their content at its Step 10. Post that content without a second preview.

GitHub has no API to upload a file to an issue. Evidence is therefore **linked**, never attached: a CI run's Actions artifact, or a local file uploaded to the team's storage when one is configured, or named as a staged local path.

## Communication conventions

- **Talk to the developer in the project's conversation language** (see root `AGENTS.md`). Code, identifiers, and this file's structure stay in English.
- **One preview, one question.** Prepare everything first (Steps 3-5), show it together (Step 6), and write only what the developer accepts. Silence is not consent. Delegated runs already had their question.
- **Ask, do not guess** on genuine decision points (for example, nothing to link, or `gh` not authenticated).
- **The issue is public to everyone who can read the repository.** No secret, token, real personal data or customer identifier goes into the comment or into a linked file.

## Step 1: Parse and validate arguments

- **Issue**: `#N`, `N` or an issue URL of this repository. Read it with `gh issue view <N> --json number,title,body,state,url,projectItems`; when `url` contains `/pull/`, the number is a PR: stop and ask. Without an issue (a `--no-issue` run), skip every issue write: collect and link the evidence (Steps 3 and 7) and hand the links to the PR description.
- **`--summary "<text>"`**: the developer's wording for the comment's resolution section in business language. It replaces only that section; the rest of the Step 4 shape is still built from the issue and the verified results. If omitted, draft the section from the issue's title and body plus the actual run results; in standalone mode, ask the developer what the change means for the user.
- **`--no-comment`**: set the closing status only; post no comment.
- **`--comment-only`**: post the comment only; set no status, even when `--close` is passed. `--no-comment` and `--comment-only` are mutually exclusive; if both are passed, stop and ask.
- **`--close`**: set the closing status (Step 5). Without the flag, set it only when the developer asks.
- **`--attach <path|glob> ...`**: explicit local files or globs to link. If omitted, scan `.me/<slug>/evidence/` (Step 3).
- **`--run <run-id>`**: a GitHub Actions run whose artifacts are evidence. If omitted, Step 3 looks up the latest run of the current branch.
- **`<slug>`** (the scratch key): the current branch name without its `feature/` or `bugfix/` prefix; delegated runs pass it.

## Step 2: Access

- `gh auth status` must report a logged-in account for `github.com`. If it does not, stop and tell the developer to run `gh auth login`; never ask for a token.
- The board status needs the `project` scope. When `gh auth status` does not list it, the status change is skipped and the report says `gh auth refresh -s project`.

## Step 3: Collect evidence (skip if `--no-comment`)

Evidence means **what the agent or CI produced**; never fabricate or link manual QA captures the agent did not generate.

1. **CI evidence**: with `--run`, that run; otherwise the latest run of the branch, `gh run list --branch <branch> --limit 5 --json databaseId,workflowName,conclusion,url`. Record each run's workflow, conclusion and `url`, and its artifacts with `gh api repos/{owner}/{repo}/actions/runs/<run-id>/artifacts --jq '.artifacts[] | {name, expired, expires_at}'`. Link the run page (it lists the artifacts); an expired artifact is reported as expired, never linked as evidence. Generated workflows keep artifacts 7 days.
2. **Local evidence**: the `--attach` paths, otherwise `.me/<slug>/evidence/`. Typical artifacts:
   - **Playwright e2e** (when executed): screenshots and the HTML report. **Skip heavy traces**; mention them instead.
   - **Verification logs**: captured stdout of lint, build, or test runs, one file per command (`verification-<cmd>.log`).
3. Record each local file with its size for the Step 6 preview, and check that none holds a secret or real personal data (the gitleaks rules in root `AGENTS.md` apply to evidence too); a file that does is left out and reported. If nothing is available and `--no-comment` was not passed, say so in the preview and offer the comment alone.

## Step 4: Draft the resolution comment (skip if `--no-comment`)

Delegated: reuse the body shown at `/pl-address-issue` Step 10; do not redraft it.

### Audience

The comment is for **stakeholders** (product, QA, support, a client-facing lead) who decide whether the issue can be accepted. Write about **behavior and acceptance criteria**, never code.

**Never put any of the following in a resolution comment:**

- File paths, class or function names, branch names, commit hashes, line numbers.
- Diff fragments, stack traces, log lines, SQL, or code of any kind.
- Framework or library names, unless the stakeholder asked for them.
- Internal jargon without external meaning.
- Personal data, credentials, tokens, customer identifiers, or production payload data.
- Claims that the work is accepted. Closing the issue records that the change is delivered, not that a stakeholder accepted it.

If a fact cannot be stated without naming a file, it belongs in the PR, not here. The PR itself is linked from the issue by its `Closes #N`; do not repeat its number.

### Shape

The comment body is GitHub Markdown, written to `.me/<slug>/resolution.md`. Headings and labels follow the project's docs language; this template is English.

```
### Resolution

<2-4 lines in business language: what the user could not do before, and
what they can do now. No technical terms.>

### Acceptance criteria

| Criterion | Status | How it was verified |
|---|---|---|
| <criterion 1, copied from the issue> | Met | <verification in plain words> |
| <criterion 2> | Not verified | <why it could not be verified> |

### Additional checks

- <check beyond the criteria, with its measured result>

### Evidence

- [<run or file name>](<link>) - <what it shows, in one line> (<expiry, for a CI artifact>)

### Pending

- <what is left out and why>

---
_Comment drafted by /pl-attach-evidence (<model>) and reviewed by <account> before posting. It summarizes the work done; it does not replace the team's functional validation._
```

Keep the closing italic line on a single line: an italic span does not cross a line break.

### Section rules

- **Resolution** - two to four lines in business language: what the user could not do before, and what they can do now. No technical mechanisms.
- **Acceptance criteria** - one row per criterion, **copied from the issue verbatim** (its `Acceptance criteria` section or checkbox lines). `Status` is exactly one of `Met`, `Partial`, `Not met`, `Not verified`; anything other than `Met` carries its reason in the third column. If the issue lists no acceptance criteria, write `The issue defines no explicit acceptance criteria.` and list what was verified instead. Never invent criteria.
- **How it was verified** - plain language: `The payment flow was tested on a slow network and no duplicate charge was created`, not `checkout.test.ts passed`.
- **Additional checks** - verification beyond the criteria, with measured results. Omit the section when none were run.
- **Evidence** - one line per linked run or file saying what it demonstrates; the links match what Step 7 actually produced. A file that could not be uploaded is named as `staged locally: <file name>`.
- **Pending** - out-of-scope, deferred, or unverified items. Omit only when there are none.

### Only what was actually verified

Every row and bullet reports something that was actually executed, with its actual outcome. A criterion nobody exercised is `Not verified` with the reason, never `Met` because the code "should" satisfy it. A test nobody ran does not appear.

### Disclosure

The closing italic line is mandatory and names the **model** that actually ran. No emoji anywhere in the comment. Never `@`-mention unless the developer asked for it.

## Step 5: Resolve the closing status (only with `--close` or an explicit request)

The issue lives on the board `1` of `HectorVeraO`. The closing status is the card in `Done`:

```
gh project view 1 --owner HectorVeraO --format json --jq '.id'
gh project field-list 1 --owner HectorVeraO --format json \
  --jq '.fields[] | select(.name == "Status") | {id, options}'
gh project item-list 1 --owner HectorVeraO --format json --limit 500 \
  --jq '.items[] | select(.content.number == <N>) | {id, status}'
```

Record the current status for the preview. When the field, the option `Done` or the item is missing, say so; the status change is then not offered.

The issue itself is closed by the PR's `Closes #N` when it merges. Close it here (`gh issue close <N> --reason completed`) only when the developer asks for it explicitly, or when the change reached `master` without a PR that closes it.

## Step 6: Preview once and confirm

Show everything prepared, together:

- the full comment body (Step 4), or "no comment";
- the evidence: each CI run with its link and artifact expiry, each local file with its size and where it will go (uploaded or staged), or "no evidence";
- the closing status as `<current> -> <target>`, or "no status change".

Then ask once with `AskUserQuestion`: **All** / **Pick** (the developer selects which writes) / **None**. Write only what was accepted.

**Delegated** from `/pl-address-issue`: skip the preview and the question and write what the developer chose there, with the comment body shown there.

## Step 7: Write

### The local evidence

Local files stay in `.me/<slug>/evidence/`, and the comment names them as staged locally.

### The comment

```
gh issue comment <N> --body-file .me/<slug>/resolution.md
```

`gh` prints the comment URL on success; report it. Never claim "posted" on a non-zero exit. This skill always posts a new comment; it never edits one (`--edit-last` is not used).

### The closing status

```
gh project item-edit --id <item id> --project-id <project id> --field-id <field id> --single-select-option-id <id of "Done">
```
When the developer chose to close the issue: `gh issue close <N> --reason completed`. Report any non-zero exit with its message; never claim "closed" or "moved" on a failed call.

## Step 8: Fallback

- `gh` not authenticated: write nothing; print the comment body path and the exact commands for the developer to run after `gh auth login`.
- A local file that was not uploaded: name it under `Evidence` as `staged locally: <file name>` and print its path.
- A status change that could not run (missing scope, field or option): leave it to the developer, with the reason and the fix.
- No issue: print the evidence links as a `## Visuals & Context` block for the PR description instead of posting.

## Step 9: Report

Print: the issue number; whether the comment was posted (its URL) or skipped; **every acceptance-criteria row other than `Met`, with its reason**; the evidence linked (runs with expiry, uploaded files with their links, staged files with their paths); the closing status (`<from> -> <to>`, skipped, or left to the developer); anything intentionally skipped (heavy traces, expired artifacts, files left out for holding sensitive data).

## What this skill does NOT do

- Edit, label, assign, create, reopen or delete issues, or edit an existing comment.
- Write anything the developer did not accept, in the Step 6 preview or the `/pl-address-issue` closing question.
- Upload or link secrets, personal data, or fabricated/manual captures - only the agent's and CI's own artifacts.
- Mark an acceptance criterion `Met` on the strength of reading the code. Only an executed verification earns it.
- Put a storage token in CI or in any file: uploads run only from the developer's machine.
- Treat a missing permission prompt as consent. The comment and status calls are allowlisted; the developer's yes (Step 6, or the `/pl-address-issue` closing question) is the only check before a write.
