---
name: pl-review-branch
description: Review the current branch's changes against main with the five Chatisimo reviewer agents before opening a pull request, then apply the findings. Runs without asking Héctor; unapplied findings go in the pull request body.
allowed-tools: Bash(git *), Bash(mkdir *), Read, Edit, Write, Agent
---

Review everything the current branch changes relative to `origin/main`: its commits plus
uncommitted work. Héctor does not review the findings; you decide, apply and record them.

## 1. Collect the change

Run each command and stop at the first failure, reporting the command and its error:

```sh
git fetch -q origin main
base="$(git merge-base origin/main HEAD)"
mkdir -p .me/review
git diff "$base" > .me/review/branch.diff
git ls-files --others --exclude-standard
```

If `base` is empty, stop: the branch has no common ancestor with `origin/main`. Only after all
commands succeed: if the diff file is empty and there are no untracked files, report "Nothing
to review" and stop.

## 2. Dispatch the reviewers

Send all five in one message so they run in parallel: `architecture-reviewer`,
`patterns-reviewer`, `test-reviewer`, `security-reviewer`, `logic-reviewer`. Give each the same
prompt:

```text
Diff: .me/review/branch.diff
Untracked files (read in full): <list, or "none">

Procedure:
1. Read your rules file first, then the diff and every untracked file.
2. Review only added or modified lines. Read the surrounding file when needed to judge them.
3. Report a finding only when you can point at the line and name the rule it breaks. If
   unsure, leave it out.
4. Do not edit, create or delete files.

Return exactly:
## <your name>
### Mandatory
- `path:line` | <rule and its source>
  Problem: <what is wrong>
  Fix: <what to change>
### Polish
- same shape, for improvements that break no rule
Write "Nothing to report." under the heading when both lists are empty.
```

## 3. Decide

Merge the five answers into one numbered list, dropping duplicates. Add no findings of your
own. For each finding, verify the claim against the code before acting on it, then decide:

- Mandatory: apply it, unless the claim is false. A false claim is recorded with the evidence.
- Polish: apply it when it improves the change without breaking another rule in `AGENTS.md`
  or `references/`. Otherwise record why it was not applied.

## 4. Apply and re-check

Apply the chosen fixes, then run `go test ./...` and `golangci-lint run ./...`. Regenerate the
diff and dispatch all five reviewers again on it. Repeat steps 3 and 4 until no mandatory
finding remains, at most three rounds; if mandatory findings remain after the third, stop and
report them to Héctor.

## 5. Record

Write the findings that were not applied, each with its reason, under "Review notes" in the
pull request body. Report to Héctor only: rounds run, findings applied, findings recorded.
