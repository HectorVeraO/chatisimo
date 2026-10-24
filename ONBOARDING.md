# Onboarding — Chatisimo agent toolkit

<!-- AGENTIC-SETUP:AUTOGEN:onboarding START -->
## Day one

1. Install Claude Code, Node, the GitHub CLI `gh` and [mise](https://mise.jdx.dev), then run `gh auth login` (github.com, HTTPS, browser login).
   The issues live on the Projects board 1 of `HectorVeraO`: also run `gh auth refresh -s project`, so the agent can move cards.
2. In the clone: `mise install` (installs the pinned tools, gitleaks and lefthook included), then `lefthook install`, so gitleaks checks every commit before it is created.
3. Create `.claude/settings.local.json` (gitignored, like the `.claude/*.proposed` exemplars, so a fresh clone has neither) with this content: it pre-approves the MCP servers. Add an optional key only when your OS user or shell environment does not already define it ("Optional keys" below).

   ```json
   {
     "enabledMcpjsonServers": ["context7"]
   }
   ```
4. Restart Claude Code (or `/restart`) and approve the project hooks from `.claude/settings.json`; the MCP servers are pre-approved by step 3.
5. Check the setup: `gh auth status` lists your account; `node .claude/hooks/test-hooks.mjs` prints a one-line summary (`--verbose` for every case); `node .claude/skills/pl-review-pr/scripts/auth-probe.mjs` checks that the review engine reaches GitHub with your `gh` login.
6. Work: `/pl-address-issue <#N or a sentence>`; before committing on your own, `/pl-review-working-tree --with patterns`. The guardrails are in root `AGENTS.md` -> "Blocked actions".

Then read root `AGENTS.md` (the rules) and the subproject `AGENTS.md` closest to the code you touch; the closest one wins.

## Credentials

GitHub needs no key in any file: `gh auth login` keeps the token in your OS credential store, and every skill and the review engine (`gh auth token`) use that login. Never put a `GITHUB_TOKEN` or `GH_TOKEN` in `.claude/settings.local.json`: it would override your login for every `gh` call.

### Optional keys

Add one to the `env` block of `.claude/settings.local.json` only when your shell does not already define it, and never as an empty string (an empty key hides your user-level variable). Names are exact: no aliases.

| Key | Needed by | Where to get it |
|---|---|---|
| `CONTEXT7_API_KEY` | library docs lookups, optional | `context7.com`; raises rate limits |
| `REVIEW_PR_AUTHOR`, `REVIEW_PR_MODEL` | `/pl-review-pr` comment footer, optional | Your handle; the model name for a manual run |


## How the agent works

- **Source of truth:** root `AGENTS.md` + the closest subproject `AGENTS.md`. `CLAUDE.md` is a one-line alias to `AGENTS.md`.
- **Two modes:** *triage* (exploratory — investigate and propose, no edits) vs *implementation* (action verbs — follow the cookbook and verify; commit, push and merge on green on their own, per root `AGENTS.md` "Workflow"). When ambiguous the agent defaults to triage and asks.
- **Library docs:** the agent queries the `context7` MCP for version-specific API docs, falling back to official-doc domains via WebSearch — never blogs / Stack Overflow as authority.
- **Healthy Baseline:** the agent assumes the branch is green; failures after its edits are its own unless proven pre-existing, and pre-existing failures are surfaced for your decision, never silently fixed.

## Agents (`.claude/agents/`)

| Agent | Use it for |
|---|---|
| `issue-implementer` | Executes the Step 7 plan handed over by `/pl-address-issue`: applies the subproject cookbook and runs lint/build/type-check. Never branches, opens a PR or writes to an issue; commits or pushes only on your word. |
| `logic-reviewer` | Correctness only: wrong output, crashes, data loss, races, leaks, regressions. Runs by default whenever non-test code changes. |
| `security-reviewer` | Runtime security only: injection, authn/authz, multi-tenant leakage, PII in logs, unsafe deserialization. **On request** - `--with security` or `--profile full`. |
| `architecture-reviewer` | Does the implementation follow the agreed design. **Optional** — runs only when you supply the design (`--design <path>`), also under `--profile full`. Without it, it aborts rather than guessing. |
| `patterns-reviewer` | Cookbook compliance: file map, conventions, Code Path Parity, known pitfalls. **On request** - `--with patterns` or `--profile full`, and only when a touched subproject has a cookbook. |
| `secrets-reviewer` | Secrets and personal data written into the repo (code, tests, fixtures, docs, config) and the gitleaks configuration. **Always** - every review, every profile, documentation included; `--no-secrets` turns it off. A documentation-only change runs this reviewer alone. |
| `test-reviewer` | The changed tests: behavior coverage, tests that lock in internals, anti-patterns, synthetic test data. Runs whenever a test file changes, every profile. |

You never dispatch these yourself; the two review skills below fan out to them, merge their
reports and verify every finding against the real diff. `secrets`, `logic`, `test` and `patterns` run on
Haiku, `security` and `architecture` on Sonnet, all at medium effort; `--model` overrides it for one run.

**They are not adversarial and they do not nitpick.** Each carries a closed emission filter: a
finding must be inside that agent's concern, quotable from the diff, introduced by your change,
and attached to a concrete bad outcome. Style, naming, formatting and "you could also write it
this way" are never findings. Every finding stops approval until it is resolved; severity (`blocker` / `warning`) only measures its impact.

## Skills (`.claude/skills/`)

Every workflow below is a **skill**; this repo has no slash commands. Invoke a skill by its `/name`.

| Skill | What it does | Common flags |
|---|---|---|
| `/pl-address-issue <#N or a sentence>` | Branch (`feature/` or `bugfix/` from the issue title), plan, implement, run the e2e and review, then one closing question: commit, push, open the PR, and the issue writes. An issue number or plain words both work. | `--bugfix`, `--worktree`, `--no-issue` |
| `/pl-review-working-tree` | Review your uncommitted change before you commit; every finding stops it until resolved. Local only. `--destination` defaults to the branch upstream, then `master`. | `--profile default\|full`, `--with patterns,security`, `--design <path>`, `--no-secrets`, `--concerns <list>` |
| `/pl-review-pr <PR-id\|url>` | Review one PR and post the verified inline comments with one verdict after your yes. A `default` review a `full` review already covered on the same head is reported and skipped. | `--profile default\|full`, `--with patterns,security`, `--design <path>`, `--no-secrets`, `--force` |
| `/pl-address-review [PR-number\|path]` | Apply PR review feedback to its branch and resolve the addressed threads after the push; no argument means the current branch's PR. Never replies, never merges. | - |
| `/pl-attach-evidence <#N>` | Post the resolution comment with links to the evidence (CI artifacts, optional uploads), and close the issue or move its card to Done. Standalone: one combined preview, one yes. From `/pl-address-issue`, your closing answer is the yes. | `--attach <path>`, `--run <id>`, `--close`, `--comment-only` |
| `/pl-draft-lesson [PR-number]` | Propose a durable lesson into `.claude/references/lesson-proposals.md`; ask to promote it into `AGENTS.md`. | - |
| `/pl-validate-agent-docs [subproject-path]` | Check every `AGENTS.md` against the code. Monthly sweep; reports only. | - |

## Integrations (through `gh`)

- **GitHub Issues, board: https://github.com/users/HectorVeraO/projects/1** - the agent reads issues with `gh issue view`. Its writes, each shown to you before it runs except the first: moving the issue's board card to In Progress when the work starts, a resolution comment with links to the evidence, and the closing status (card to Done, or the issue closed). `/pl-address-issue` offers the last two at its closing question. Issue create/edit happens only when you ask; deletes are denied.
- **GitHub** - reads are free (PRs, diffs, checks, runs). Writes: **opening a PR** (`gh pr create`, offered after a push, with `Closes #N`), **recording a review verdict** (`gh pr review`, only when you explicitly ask; on your own PR GitHub allows only a comment), **`/pl-review-pr` posting inline comments** (deterministic verifier + human y/n), and **resolving review threads** you agreed to (`/pl-address-review`). Deleting a repository, issue, release, run or project is denied outright.
- **Evidence** - CI runs keep their reports as Actions artifacts for 7 days; the resolution comment links the run.

## Branch model

- Branches: `feature/<slug>` and `bugfix/<slug>`, the slug from the issue title, both cut from `master` in place and merged back through a PR whose body says `Closes #N`. There is no other prefix. See root `AGENTS.md` -> "Branch types".
- **The agent merges on green**: once every required check passes, except a PR that touches `.github/`, `AGENTS.md`, `cookbooks/`, `.claude/`, `lefthook.yml` or `.gitleaks.toml`, which waits for your explicit OK.
- **Defaults, not gates:** an issue, a branch plus a PR, and green verification are what the agent does by default. When you ask for work without an issue, a direct commit to `master` or a skipped check, the agent does it and names it in its report. Only the `.claude/hooks/` guardrails, the gitleaks hook and the settings `deny` list block anything; root `AGENTS.md` -> "Blocked actions" lists them.


## Conventions worth knowing on day one

- **Lessons Learned / Code Path Parity:** living sections in each `AGENTS.md`. When a fix teaches something durable, `/pl-draft-lesson` writes a proposal to `.claude/references/lesson-proposals.md`; promoting it into `AGENTS.md` is your call.
- **Scratchpad:** keep notes/plans out of the source tree; use the project's gitignored scratchpad folder.
- **Secrets and personal data:** never commit a credential or real personal data, tests and fixtures included; test data is synthetic (`user@example.com`). gitleaks blocks the commit and CI scans the full history; root `AGENTS.md` -> "Secrets and personal data".
- **E2E for web changes:** if this repo has an end-to-end suite, a change to a web frontend ships its happy-path e2e (new or updated). If a web change is delivered without one, record the justification in the PR/commit. See the e2e subproject's `AGENTS.md`.

<!-- AGENTIC-SETUP:AUTOGEN:onboarding END -->

<!-- Team additions (custom agents, commands, project-specific onboarding) go below. The skill never edits below this line. -->

## Chatisimo specifics

- Agents commit, push and merge on green on their own; a PR that touches `.github/`, `AGENTS.md`,
  `cookbooks/`, `.claude/`, `lefthook.yml`, `.gitleaks.toml` or the GitHub ruleset waits for the
  owner's explicit OK. Root `AGENTS.md` "Workflow" is the rule; where the text above says otherwise,
  it wins.
- Review before committing with `/pl-review-working-tree --with patterns`: the patterns reviewer checks
  Go code against `cookbooks/coding.md`. The project rules each reviewer applies are in root
  `AGENTS.md` "Review rubrics by concern".
- There is no e2e suite and no web frontend: the e2e steps above do not apply.
