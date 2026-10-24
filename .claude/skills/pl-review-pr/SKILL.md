---
name: pl-review-pr
description: "Review ONE GitHub PR with this repo's concern-split reviewers under profile `default` (secrets, logic, test) or `full` (all six), publishing one verdict. Strict unless the destination is lenient or `--lenient` is passed. Dry-run; asks before publishing. For local uncommitted work use pl-review-working-tree."
argument-hint: "<PR-number|PR-url> [--profile default|full] [--with patterns,security] [--design <path>] [--no-secrets] [--lenient] [--concerns <list>] [--model <name>] [--force]"
---

# pl-review-pr

Review the GitHub pull request in `$ARGUMENTS` by fanning out to this repo's **concern-split reviewer agents**, then merge, verify, and (with authorization) publish their findings as one GitHub review.

The dispatcher (this skill, in the main context) fetches the PR, validates it, and injects a **ground-truth manifest** into each agent's prompt. After the agents finish, it **merges** the per-concern reports and runs a deterministic **post-verifier** that drops comments whose path or line is outside the diff, whose `evidence` is missing, too short or not a verbatim diff substring, or whose `parentId` is not a review comment on the PR. Every step is a plain Node script under `.claude/skills/pl-review-pr/scripts/`.

**The review is not adversarial.** Every finding stops approval until it is resolved, for every profile: severity measures impact, never whether a finding must be addressed. The PR is approved only with zero findings.

**The review is a conversation.** `digest.mjs` groups the PR's comments into threads and hands each reviewer the open ones of its concern (the finding, the developer's answer, and the `parentId` to reply into). The reviewer answers in the thread instead of re-posting.

**No reviewer covers for another.** A finding outside a reviewer's concern is dropped, whether or not that concern runs in this run.

GitHub access is direct REST and GraphQL through `scripts/lib/gh.mjs` (Node `fetch`, no npm package). Authentication comes from `gh auth login`: the scripts take the token from `GH_TOKEN` or `GITHUB_TOKEN` when set, else from `gh auth token`; nothing else needs configuring. The repository is `GH_REPO` (`owner/repo`) when set, else the `origin` remote. `GITHUB_API_URL` points them at another API base (GitHub Enterprise: `https://<host>/api/v3`). The optional `REVIEW_PR_AUTHOR` footer handle and `REVIEW_PR_MODEL` go in `.claude/settings.local.json`'s `env` block (per-developer, gitignored). A comment and the review are attributed to the account that owns the token.

Artifacts live in `.me/review-pr/` (see `.claude/references/reviewer-contract.md`). Re-review detection matches that directory name and the `**[review-pr` marker prefix literally; never rename either. The profile suffix (`**[review-pr:default · ...`) keeps the prefix.

Talk to the developer in the `Docs language` root `AGENTS.md` declares. The Spanish messages below are the default wording: say them in that language. Markers, script output and the published comment's fixed labels are wire format and stay as the scripts write them.

## Profiles and the concern set

The same selection rules apply in `pl-review-working-tree`. A concern is **selected** by the profile and the flags:

| Concern | Agent | Selected |
|---|---|---|
| secrets | `secrets-reviewer` | always, documentation-only diffs included; `--no-secrets` turns it off |
| logic | `logic-reviewer` | when non-test code changes |
| test | `test-reviewer` | only when a test file changes |
| patterns | `patterns-reviewer` | off; `--with patterns` or `--profile full` |
| security | `security-reviewer` | off; `--with security` or `--profile full` |
| architecture | `architecture-reviewer` | off; a design reference (`--design ...`) or `--profile full` |

There are exactly two profiles: **`default`** (the "always" and "when" rows, plus whatever `--with` and `--design` add) and **`full`** (all six). One run publishes one review with one verdict.

A selected concern still has to qualify:

| Concern | Qualifies when |
|---|---|
| secrets | the diff has a file (`digest.surface.secrets`: a credential can leak into any file) |
| logic | a changed file is neither inert nor a test file (`digest.surface.logic`) |
| test | a non-inert test file changed (`digest.surface.test`; the files are in `digest.surface.testFiles`) |
| patterns | the diff has a non-inert file (`digest.surface.patterns`) and a touched subproject has a non-empty cookbook |
| security | the diff has a non-inert file (`digest.surface.security`) |
| architecture | the diff has a non-inert file (`digest.surface.architecture`) and design input resolves |

A documentation-only change (every file documentation, a lockfile or plain data) therefore runs `secrets` alone, whatever the profile. `digest.surface.defaultConcerns` is the `default` set for this diff before any flag.

Never dispatch `architecture` or `patterns` without their input: a reviewer with no baseline invents one. **Report every exclusion**; a silently omitted concern reads as approval.

## Argument parsing

`$ARGUMENTS` may contain, in any order:

- **A PR identifier**: URL (e.g. `https://github.com/<owner>/<repo>/pull/2`) or number (`2`). If omitted, infer it from the current branch: `gh pr view --json number --jq .number`.
- **`--profile default|full`**: default `default`. It selects the concern set, the artifact paths and the marker. Never infer `full` from who is asking, the branch or the diff size. The retired names `dev` and `arch` are rejected: say so and name the two profiles.
- **`--with <list>`**: comma-separated `patterns` and/or `security`, added to the `default` profile. Redundant under `full`. Any other name is a usage error: say so and stop.
- **`--design <path>`**: the design artifact the implementation was agreed to follow. Selects the architecture concern. Accepts a file path, or `issue` for the design section of the issue the PR closes (`Closes #N` in its body; read it with `gh issue view <N>`).
- **`--no-secrets`**: do not run `secrets`. Report it as excluded by the flag.
- **`--lenient`**: force lenient mode regardless of destination. What it changes is defined in `.claude/references/reviewer-contract.md` "Severity and verdict".
- **`--concerns <list>`**: comma-separated subset of the selected concerns. It only narrows; a concern that is not selected is reported as such, not run.
- **`--model <name>`**: one model for every reviewer this run. Use it as given, without a confirmation question. Without it each reviewer runs on the model its definition pins (`secrets`, `logic`, `test`, `patterns`: `claude-haiku-5-5`; `security`, `architecture`: `sonnet`).
- **`--force`**: rerun a `default` review that a `full` review already covers on this head, over the whole diff (see "Coexistence").

**Never raise the model on your own**, not for a large diff, many files or a subtle-looking change. A different model is the developer's choice, passed with `--model`.

## Pre-flight (Step 1) - auth probe

```
node .claude/skills/pl-review-pr/scripts/auth-probe.mjs
```

If exit != 0, ABORT with:

> Cannot reach GitHub. The auth probe failed. Run `gh auth login` (or `gh auth status` to see the active account), and confirm the repository resolves (`GH_REPO=owner/repo`, or an `origin` remote on GitHub). (Last error: `<one-line stderr from the probe>`)

On success it prints `{ "ok": true, "user": "<login>", "scopes": [...] | null, "repo": "<owner/repo>", "token": { "present": true, "source": ... }, ... }`. Proceed only on `ok: true` with a non-empty `repo`. `scopes` is `null` for a fine-grained or app token, which send no `x-oauth-scopes` header; that is not a failure.

On failure it prints `ok: false` with each failed step's HTTP status and meaning: **401** the credentials were rejected (re-run `gh auth login`); **403** the token is valid but lacks permission, or a rate limit was hit; **404** the path or base URL is wrong, or the repository is private and this account cannot see it. A variable reported as **`empty`** is shadowed: a `""` key in `.claude/settings.local.json`'s `env` block overrides a working user-level value. Tell the developer to remove that key. Token values are never printed, only presence and source.

## Pre-flight (Step 2) - fetch PR data (or reuse the packet)

The **dispatcher** runs the fetch, not the agents. Start with `gh pr view <pr-number> --json headRefName,headRefOid` to identify the source branch and current head commit.

**Make the head readable locally first**, so reviewers read context at the reviewed commit (`git show <sha>:<path>`), never from your checkout:

```
git fetch origin <source-branch>
```

**Reuse the packet across profiles.** If `.me/review-pr/<pr-id>.input.json` exists, compare its `head.sha` with the PR's current source commit:

- **Same SHA**: reuse it, skip the fetch, and report `packet reutilizado (head <sha7>)`. Re-run `git cat-file -e <sha>^{commit}` and prefer that result over the packet's `headAvailableLocally`.
- **Different SHA, or no `head.sha`**: fetch anew; the redirect below overwrites the packet.

Otherwise fetch it:

```
node .claude/skills/pl-review-pr/scripts/fetch-pr.mjs <pr-id> > .me/review-pr/<pr-id>.input.json
```

Create `.me/review-pr/` first if it does not exist. If the script exits non-zero, ABORT and surface its stderr; dispatch nothing. The script validates required fields (`meta.title`, `meta.source.branch`, `meta.destination.branch`, non-empty diff), reports every missing one at once, and never emits partial output. The packet carries the PR metadata (`meta`: title, body, author login, source and destination branch and sha), the diff, the changed `files` (with `filesMissingFromDiff` when GitHub listed a file its diff omits; name them in the final report), and `existingComments`: every review comment, conversation comment and submitted review body, in one normalized shape (`id`, `kind` `inline|general|review`, `body`, `author`, `path`, `line`, `parentId`).

`head.headAvailableLocally` is `false` when the head commit is not readable (a fork PR's head is not on `origin`; `git fetch origin pull/<n>/head` fetches it). Do not abort: review the diff alone, and state in the final report that surrounding context was unavailable. Never substitute the working tree.

Hunks carry GitHub's fixed context (`contextLines: 3`): the PR diff endpoint takes no context parameter, and its hunks are exactly the lines a review comment can anchor to. Reviewers read more of a file with `git show <head.sha>:<path>`.

## Pre-flight (Step 3) - build the manifest

```
node .claude/skills/pl-review-pr/scripts/digest.mjs <pr-id>
```

It parses `.me/review-pr/<pr-id>.input.json` and emits a JSON object containing:

- `prId`, `title`, `source`, `destination`
- `lenientMode` (true when the destination matches the configured lenient pattern; always false when none is configured) and `lenientPattern` (the active pattern source, or `null`)
- `files`: array of `{ path, hunks: [{ startLine, endLine }] }` (destination-side line ranges)
- `stats`: file count and diff size
- `head`: the reviewed commit (`sha`, `branch`, `readFrom`, `headAvailableLocally`), copied from the packet
- `contextLines`: lines of surrounding context the hunks carry
- `surface`: per-concern `{ hasSurface, reason }`, plus `substantiveFiles` and `inertFiles`
- `surface.secrets` (`true` for any non-empty diff), `surface.test` (true when `surface.testFiles` is non-empty), `surface.testFiles` (`isTestFile` matches, `conftest.py` included) and `surface.defaultConcerns` (the `default` set for this diff before any flag)
- `threads`: every comment thread on the PR, one root plus its replies (reply-to-a-reply flattened into the root's thread). Only review comments on a line thread (`replyable: true`); a conversation comment or a review body is a thread of its own. Each carries `parentId`, its anchor (`path`, `line`, the original line when a push moved it), `rootIsBot`, `developerReplied` and `awaitingBotReply`.
- `activeThreads`: the threads reviewers may answer: a replyable **bot** finding a **developer** replied to, where the developer has the last word.
- `validParentIds`: every review comment id on the PR (`verify-report.mjs` re-derives it from `input.json`). A developer's answer in the PR conversation is not threaded by GitHub and cannot be replied to.
- `commentsTruncated`: true when the PR's comment list came back paginated; the thread list is then incomplete and `digestMarkdown` says so.
- `digestMarkdown`: the block to paste into each agent prompt: the ground truth plus an open-threads section, omitted when nothing is open or truncated.

Bot comments are identified by the `**[review-pr` marker `post-comments.mjs` stamps on every published comment, never by author (GitHub attributes the comment to the account that owns the token, usually the developer's own).

If `digest.mjs` exits non-zero, ABORT and surface stderr. Keep the parsed manifest in memory.

**Effective lenient mode** = `lenientMode` from the manifest, OR `--lenient`. Otherwise the review is strict.

## Pre-flight (Step 3b) - the re-review delta

Run it on every PR review, after the profile is known and before dispatching:

```
node .claude/skills/pl-review-pr/scripts/rereview-delta.mjs <pr-id> --profile <profile>
```

Pass `--force` too when the developer gave it. The script first removes this profile's previous reports (`<pr-id>.<profile>.report*.json`; never another profile's, never `input.json`), then reads the last-reviewed commit from the `review-pr:head=<sha>` stamp in the body of the last published review and writes `.me/review-pr/<pr-id>.<profile>.delta.json`.

**It always fails open.** No prior review, a review predating the stamp, a force-push, a commit missing from this clone, or a failed `git diff` all produce `mode: "full"` with a reason.

Report `mode` and `reason` to the developer, and supply the delta in each reviewer packet. Report these outcomes explicitly:

- `coveredByFullReview: true`: a `full` review already covers this `default` run on this head. Say `ya cubierto por la revisión full en este head (<sha7>)` and stop. With `--force` the script returns `mode: "full"` instead and the run reviews the whole diff.
- `legacyReview: true`: the prior review carries no stamp, so this run reviews the whole diff and leaves the stamp. Say so.
- `mode: "full"` for any other reason on a PR that HAS been reviewed before: name the reason.
- `noChangesSinceLastReview: true`: the head has not moved, so this run can only answer threads. Say so; it is not a clean review.

Each profile has its own stamp and delta: a `full` re-review is unaffected by a `default` review on the same PR, while a `full` stamp stands in for `default`.

## Pre-flight (Step 4) - resolve the concern set

Start from **the selected set**: `default` is `secrets`, `logic`, `test`, plus `patterns` / `security` named in `--with` and `architecture` when `--design` was given; `full` is all six. Remove `secrets` when `--no-secrets` was passed. Then remove what does not qualify:

1. **secrets / logic / test / security**: drop when `digest.surface.<concern>.hasSurface` is `false` (every changed file is documentation, a lockfile or plain data; logic also when only test files changed; test when no test file changed) and report the digest's reason. Never second-guess the signal: a `true` you disagree with still runs. A documentation-only change runs `secrets` alone.
2. **architecture**: include only if `digest.surface.architecture.hasSurface` is `true` and design input resolves. In order: the `--design <path>` file (must exist and be non-empty); else an SDD spec-delta for the in-flight change (`specs/changes/*/spec-delta.md`) when the repo uses SDD and the change is identifiable from the branch or the issue; else the issue's design section when `--design issue` was passed and the issue the PR closes has one. If none resolves, **do not dispatch it**.
3. **patterns**: include only if `digest.surface.patterns.hasSurface` is `true` and a touched subproject has a non-empty cookbook: at least one non-empty `<subproject>/cookbooks/*.md` fragment, or a `<subproject>/AGENTS.md` carrying real pattern content. Hand the agent each touched subproject's index and its fragment paths; it reads the fragments the index's "Read before" table routes the touched files to.
4. **`--concerns`**: when given, intersect with the result. A concern named there that is not selected, or that does not qualify, is reported as unavailable with its reason.

**If the resolved set is empty, stop.** Say which profile ran, that every concern was excluded, and why. Never fall back to another profile's concerns.

State the resolved set to the developer before dispatching, with the reason for every exclusion.

## Dispatch

Dispatch the resolved concerns **in parallel**, as multiple `Agent` tool calls in one message.

For each concern, invoke the `Agent` tool with:

- `subagent_type`: `logic-reviewer` | `security-reviewer` | `architecture-reviewer` | `patterns-reviewer` | `secrets-reviewer` | `test-reviewer`
- `model`: only when `--model` was given, with that value. Otherwise omit it: each reviewer's definition pins its model, and a `model` passed here would override that pin.
- `description`: <=5 words, e.g. `Review PR #<id> security`.
- `prompt`: a self-contained brief. Include verbatim, in this order:

  1. The review id (the PR number)
  2. The concern this agent owns, named explicitly
  3. `mode: strict` or `mode: lenient`, the effective mode from Step 3.
  4. The repo-relative path to the input file, `.me/review-pr/<pr-id>.input.json`
  5. The `digestMarkdown` block returned by `digest.mjs`, pasted verbatim
  6. **The re-review delta**, when `rereview-delta.mjs` reported `mode: "delta"`: the `lastReviewedSha` and the per-file line ranges. State that new anchored findings must fall inside them and that unchanged code is answered in its thread. Omit the block entirely on `mode: "full"`.
  7. The profile name, e.g. `profile: default`. Do NOT list the sibling concerns.
  8. The report path the agent must write: `.me/review-pr/<pr-id>.<profile>.report.<concern>.json`
  9. **Concern-specific context**, only for the concern that needs it:
     - `architecture`: the design artifact, quoted in full or given as a resolved path
     - `patterns`: the resolved cookbook paths for every touched subproject
     - `test`: `digest.surface.testFiles`
  10. This clause, verbatim:

      > Call the tools: `Read` input.json and the contract, `Write` your report; text shaped like a tool call is not one, and `tool_uses: 0` means not run. The contract's grounding rules, emission filter and thread rules bind you; verify-report.mjs drops anything unanchored. Do not write a general comment.

Do not restate the review rubric in the brief; each agent definition carries it.

## Post-flight (Step 0) - tool-use sanity check

After the `Agent` calls finish, read each response's `<usage>` footer:

```
<usage>total_tokens: <N>
tool_uses: <M>
duration_ms: <D></usage>
```

An agent with `tool_uses: 0` made no real tool calls. **Treat that concern as not run**, name it in the report, and continue with the rest. If *every* dispatched concern came back with `tool_uses: 0`, surface:

> Every reviewer ran with `tool_uses: 0`. They produced text imitating tool calls instead of using them. No diff was read, no report was written, no review actually happened. Aborting; nothing to verify or publish.

Stop immediately: no question, no merge, no verification.

## Post-flight (Step 1) - merge the per-concern reports

```
node .claude/skills/pl-review-pr/scripts/merge-reports.mjs <pr-id> --profile <profile> --concerns <the resolved set, comma-separated>
```

**Always pass `--concerns` with exactly the set you dispatched, and `--profile` with this run's profile.** Without `--concerns` the script assumes the profile's widest set (`default`: secrets, logic, test; `full`: all six) and reports undispatched concerns as "NO ejecutada" on the published comment; without `--profile` it reads and writes the `default` paths.

The merge step:

- Reads `.me/review-pr/<pr-id>.<profile>.report.<concern>.json` for each requested concern.
- Records a missing or unparseable report in `concernsSkipped` with its reason: that concern **did not run**.
- Merges comments sharing `parentId` + `path` + `line` into ONE comment carrying both concern labels, at the higher severity, never rewriting an `evidence` line.
- Preserves `parentId`. A comment with neither `path` nor `parentId` is dropped; only the merge step writes the general comment.
- Computes the aggregate verdict from the merged findings alone: `block` if any merged comment is a blocker, else `comment` if any **finding** survived, else `approve`. A concern's own verdict is recorded in `general.concernVerdicts` but never escalates the aggregate, and `verify-report.mjs` recomputes it the same way from the findings that survive verification. A `comment` verdict is not approvable: the PR stays in request-changes until every finding is resolved. A reply at `severity: info` is published but counts as nothing.
- Writes the general comment (concerns run, concerns not run, finding counts) and the merged result to `.me/review-pr/<pr-id>.<profile>.report.json`.

Exit 2 means no usable per-concern report existed. Surface it and stop; it is not a clean review.

## Post-flight (Step 2) - verify the merged report

```
node .claude/skills/pl-review-pr/scripts/verify-report.mjs <pr-id> --profile <profile>
```

The verifier:

- Builds the authoritative `{path, hunk-line-ranges}` set from the diff in `input.json`.
- Drops a comment for any of: `path-not-in-diff`, `line-outside-hunks`, `missing-evidence`, `evidence-too-short`, `evidence-not-in-this-file` (a real diff line from another file), `evidence-not-in-diff`, `invalid-parent-id` (not a positive integer), `parent-not-in-pr` (not a comment in `input.json.existingComments`), `parent-not-replyable` (a conversation comment or a review body, which GitHub does not thread).
- Keeps general comments (no path, no `parentId`) as-is.
- Keeps a **reply** with no `path` once its `parentId` resolves. A reply **with** a `path` goes through the same path/line/evidence gates as any inline comment.
- Drops any NEW anchored finding outside the re-review delta as `outside-rereview-delta`; thread replies are untouched.
- **Rebuilds the general comment from the survivors**, so its counts match what is published.
- Reports `bodyWords` per comment and `commentsOverBudget`; a long comment is never dropped.
- Writes the survivors to `.me/review-pr/<pr-id>.<profile>.report.verified.json`, with `verifierMeta.headSha` and `verifierMeta.anchorableRanges` (the diff's line ranges per path, the lines GitHub accepts an inline comment on).
- Exits **2** when there were anchored comments and every one failed an anchoring gate. Drops by `outside-rereview-delta` alone never count. Exits 0 otherwise.

**Outcomes:**

- **Report file missing**: the merge step did not produce one. Surface the agents' transcripts and end.
- **Exit 2 (fabrication suspected)**: print the verifier's `droppedDetails` AND its `filesInDiff` list, then:

  > Los reviewers no produjeron observaciones ancladas en el diff real. Todos los hallazgos inline referencian archivos que no están en el PR. No se publica.
  > Archivos realmente tocados por este PR: `<list>`.
  > Rutas afirmadas por los agentes: `<list of dropped paths>`.

  Stop here. Do not ask y/n.

- **Exit 0**: proceed to Step 3. `kept` counts inline, file-level and reply candidates; the general comment is always published on top of them, so `kept` can be 0.

## Post-flight (Step 3) - present the verified report

Print the verified report. State at the top:

- The profile, the resolved concern set, and every concern that did NOT run with its reason.
- The one consolidated verdict: `APPROVE` only with zero pending findings, `REQUEST_CHANGES` with any pending finding of any severity. Name the concerns that did not run beside it: a clean `default` run did not review security or architecture.
- Per-concern verdicts and finding counts.
- Whether surrounding context was available (`head.headAvailableLocally`); if not, that the review ran on the diff alone.
- How many open threads the digest found, how many the reviewers answered, and, when `commentsTruncated` is true, that the thread list was incomplete.

If the verifier dropped comments, add a **`## Dropped by dispatcher verifier`** section with each one's path, line, severity, reason and body preview. If any agent listed items under `## Dropped by the emission filter`, give the count per concern.

## Post-flight (Step 4) - ask publication authorization

Use `AskUserQuestion`:

> Publicar N observaciones en el PR, con veredicto <APPROVE|REQUEST_CHANGES>? (N = `kept` count from the verifier; the verdict is APPROVE only with zero findings, and goes out as a COMMENT review when the PR is yours)

Options: `Publicar las N`, `No publicar`, `Publicar un subset`. If subset, ask which numbers. There is no `--yes` flag. This answer is the publish confirmation: run Step 5 without asking again.

## Post-flight (Step 5) - publish

On `Publicar`:

```
node .claude/skills/pl-review-pr/scripts/post-comments.mjs <pr-id> .me/review-pr/<pr-id>.<profile>.report.verified.json --profile <profile> --model "<the models the reviewers actually ran on>"
```

**Pass `--model` with the models the reviewers actually ran on**: the `--model` value when one was given, otherwise the distinct models the dispatched reviewers' definitions pin, comma-separated (e.g. `claude-haiku-5-5, sonnet`). It goes into each comment's AI-generation disclosure footer.

`post-comments.mjs` accepts only the **verified** report: it refuses any report without the `verifierMeta` stamp `verify-report.mjs` writes, before any network call.

Before any write it checks that the PR's head is still `verifierMeta.headSha`; if the branch moved, it refuses and the review must be fetched and run again.

Publication, in this order:

1. **Thread replies.** Each comment with a `parentId` is posted with `POST .../pulls/<n>/comments/<root>/replies`, where `<root>` is the thread's top-level review comment (GitHub accepts replies only there). Matching thread and body prevents duplicate replies on later runs.
2. **Thread resolution.** A reply with `resolvesThread: true` (an `info` reply confirming a verified fix) resolves its thread once the reply is on the PR, through the GraphQL `resolveReviewThread` mutation; a thread already resolved is skipped.
3. **One review.** `POST .../pulls/<n>/reviews` with `commit_id` = the verified head, the general comment as `body`, and every finding whose line is inside `verifierMeta.anchorableRanges` as an inline comment (`path`, `line`, `side: RIGHT`; `start_line`/`start_side` when the finding carries a `startLine` in the same hunk). A file-level finding, or one whose line GitHub cannot anchor, goes into the body under "Observaciones sin linea anclable en el diff de GitHub", because GitHub rejects the whole review when one inline comment cannot be placed. The event is the verdict: `APPROVE` with zero pending findings, `REQUEST_CHANGES` otherwise. On a PR **you authored** GitHub forbids both, so the event is `COMMENT` and the body ends with `**Veredicto: APPROVE|REQUEST_CHANGES**`. When the body and every inline comment are already on the PR, no review is sent (`review.submitted: false`).

Each comment gets a `[review-pr:<profile> · agente Claude Code]` header and an **AI-generation disclosure footer** (skill, model, and the account it was posted from), then is deduped against existing PR comments. Surface the script's JSON summary (`posted`, `skipped`, `errors`, `repliedThreads`, `resolvedThreads`, `resolveErrors`, and `review`: `event`, `action`, `ownPr`, `inlineComments`, `inBody`, `submitted`). The script retries transient GitHub failures itself (network error, timeout, HTTP 429/502/503/504, a rate-limited 403; 3 attempts in total), and re-checks the PR before re-sending a reply or the review so one that landed despite the error is never duplicated. On any error in `errors`: stop and report; do not re-run. A failed reply fails closed: the review requests changes whatever the tally said (`review.reason` names the failed count) and the script exits non-zero. A `resolveErrors` entry is reported, never a stop: the reply landed and only its thread stayed open.

On `No publicar`: end with no writes. The verified report stays in `.me/review-pr/`.

## Coexistence - `default` and `full` on one PR

1. **A `full` review covers `default`.** When `rereview-delta.mjs` reports `coveredByFullReview: true`, a `default` run on that head is redundant: report it and stop, without a question. `--force` reruns it.
2. **Never touch another profile's artifacts**, and never post against another profile's verified report.

## What this skill does NOT do

- Modify source files, in the dispatcher or any agent.
- Publish comments without the `AskUserQuestion` authorization step.
- Merge, close, or reopen the PR. The replies, the thread resolutions and the one review are its only writes.
- Choose the verdict. It is computed by `post-comments.mjs` from the verified findings alone, never from a reviewer's own verdict or the dispatcher's judgement.
- Raise the model on its own. Only `--model` changes it.
