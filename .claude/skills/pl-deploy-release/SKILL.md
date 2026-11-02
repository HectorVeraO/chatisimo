---
name: pl-deploy-release
description: "Cut and deploy a production SemVer release of Chatisimo: verify origin/master CI, draft structured release notes, create and push the annotated vX.Y.Z tag on origin/master, publish the GitHub Release, and watch the deploy.yml workflow to completion."
argument-hint: "[vX.Y.Z | patch | minor | major] [--rerun <run-id>]"
---

# pl-deploy-release

Cut a production release of Chatisimo on `origin/master`, publish its curated GitHub Release notes, and verify the `.github/workflows/deploy.yml` rollout on the production VM.

`$ARGUMENTS` (optional): explicit SemVer tag (`vX.Y.Z`), bump level (`patch`, `minor`, `major`), or `--rerun <run-id>` to retry a failed deploy run after an external config/intent fix.

Talk to the developer in the conversation language (`AGENTS.md`); keep release notes and tag messages in English.

## Step 1: Preflight `origin/master` and SemVer target

1. Fetch remote state and tags without touching local `master` (which may hold unpushed local commits):
   ```
   git fetch --tags origin master
   git tag -l --sort=-v:refname
   ```
2. Inspect commits merged to `origin/master` since the latest `vX.Y.Z` tag:
   ```
   git log <prev-tag>..origin/master --oneline
   ```
3. Pick the next SemVer tag (`vMAJOR.MINOR.PATCH`):
   - `feature:` commits -> `minor` bump (`v1.4.1` -> `v1.5.0`)
   - `fix:` / `documentation:` / `test:` / `chore:` only -> `patch` bump
   - Explicit `vX.Y.Z` in `$ARGUMENTS` overrides.
4. Verify the commit at `origin/master` (`git rev-parse origin/master`) has a completed, successful `ci.yml` run (`deploy.yml` refuses to deploy any SHA without one):
   ```
   gh api "repos/HectorVeraO/chatisimo/actions/workflows/ci.yml/runs?head_sha=<sha>" --jq '[.workflow_runs[] | select(.status == "completed" and .conclusion == "success")] | length'
   ```
   If the count is `0`, wait for the active `ci.yml` run on `origin/master` to pass before tagging.

## Step 2: Draft curated release notes

Write `.me/release-<tag>/release-notes.md` following the repository's release notes structure:

```markdown
## Highlights

- **<Feature or Fix Title> (#<issue>, #<pr>)**: <Concise technical summary of what changed, schemas/migrations added, commands, and runtime settings.>

## Upgrade Notes

- <Operator action required on deploy, such as enabling a Discord Developer Portal Privileged Gateway Intent, running a `/<module>-config set` slash command per server, or placing a secret/override file in `/config/chatisimo/prod/<key>`. Omit section if none.>

## Pull Requests Included (since `<prev-tag>`)

- #<pr> — <PR summary> (#<issue>)

**Full Changelog**: https://github.com/HectorVeraO/chatisimo/compare/<prev-tag>...<tag>
```

Scan the file for secrets, personal emails, or private paths before publishing.

## Step 3: Tag `origin/master`, push, and publish the GitHub Release

`deploy.yml` requires an **annotated** tag (`git cat-file -t refs/tags/<tag>` must be `tag`, not `commit`) whose commit is an ancestor of `origin/master`. Publishing the GitHub Release immediately after pushing the tag ensures `deploy.yml`'s `Publish the GitHub Release` step (`if ! gh release view "${TAG}"`) preserves the curated notes instead of falling back to `--generate-notes`.

- **When running in a harness with `.claude/settings.json` deny rules** (or without explicit developer authorization to run release commands directly): write `.me/release-<tag>/run-blocked.sh` per root `AGENTS.md` ("Blocked actions") and ask the developer to run it:
  ```bash
  #!/usr/bin/env bash
  set -e

  git fetch origin master
  git tag -a <tag> origin/master -m "Release <tag>"
  git push origin <tag>
  gh release create <tag> --verify-tag --title "Chatisimo <tag>" --notes-file .me/release-<tag>/release-notes.md
  ```
- **When authorized by the developer to execute directly**: run the commands directly in the session shell (on Windows PowerShell, execute `git` and `gh` directly rather than spawning an interactive `bash` subshell, which can hang without output):
  ```powershell
  git fetch origin master; git tag -a <tag> origin/master -m "Release <tag>"; git push origin <tag>; gh release create <tag> --verify-tag --title "Chatisimo <tag>" --notes-file .me/release-<tag>/release-notes.md
  ```

## Step 4: Watch `deploy.yml` and diagnose VM health checks

1. Locate the triggered `deploy` workflow run:
   ```
   gh run list --workflow=deploy.yml --limit 3
   ```
2. Watch the run to completion with output redirected to `.me/release-<tag>/deploy-watch.log`:
   ```
   gh run watch <run-id> --exit-status *> .me/release-<tag>/deploy-watch.log; gh run view <run-id>
   ```
3. **If `Start the release` fails**:
   - Inspect the failed log (`gh run view <run-id> --log-failed *> .me/release-<tag>/deploy-fail.log`).
   - Check where `.deploy/prod/release.sh` stopped:
     - **Before `Container chatisimo-bot Starting`**: a migration or `chatisimo config set <key>` call failed against `/config/chatisimo/prod/<key>`.
     - **At `chatisimo-bot is not healthy (running, restarts: true N)` waiting for `discord gateway open`**: migrations and config loads succeeded, and `chatisimo-bot` exited while opening the Discord Gateway. Common cause: a newly added Privileged Gateway Intent (such as `IntentMessageContent` `1 << 15`) is not yet toggled ON in the Discord Developer Portal (`Bot` -> `Privileged Gateway Intents`).
   - Once the external prerequisite is fixed, re-run the failed job:
     ```
     gh run rerun <run-id> --failed
     ```
     Because `Check for an existing image` in `deploy.yml` detects the image already pushed to Artifact Registry, the re-run skips `docker/build-push-action` and completes in ~30 seconds.

## Step 5: Cleanup

Remove `.me/release-<tag>/` once the deployment succeeds and report the release URL, deployed image tag, and workflow run URL.
