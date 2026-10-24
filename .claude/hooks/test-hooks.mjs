/**
 * Self-test for the hook scripts in this directory.
 *
 * Run: `node .claude/hooks/test-hooks.mjs` in a scaffolded repo, or
 * `node references/hooks/test-hooks.mjs` from the skill directory. No install is needed.
 * Prints a one-line summary plus any failed or skipped case; `--verbose` prints every case.
 * Temporary files live under `<repo>/.me/test-runs/` and are removed after the run.
 *
 * Payloads are built with JSON.stringify, never shell echo, so Windows paths keep their
 * backslashes.
 *
 * Exits non-zero if any case does not produce the expected exit code.
 */

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HOOKS_DIR = path.dirname(fileURLToPath(import.meta.url));
const VERBOSE = process.argv.slice(2).includes('--verbose');

/**
 * Returns the repository root: the nearest ancestor of this file holding `.git`, else the
 * current directory.
 */
function findRepoRoot() {
  let dir = HOOKS_DIR;
  for (;;) {
    if (fs.existsSync(path.join(dir, '.git'))) {
      return dir;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      return process.cwd();
    }
    dir = parent;
  }
}

const TEST_RUNS_DIR = path.join(findRepoRoot(), '.me', 'test-runs');
fs.mkdirSync(TEST_RUNS_DIR, { recursive: true });
const RUN_DIR = fs.mkdtempSync(path.join(TEST_RUNS_DIR, 'test-hooks-'));

/**
 * Removes this run's directory, and `.me/test-runs` when no other run is using it.
 */
function removeRunDir() {
  fs.rmSync(RUN_DIR, { recursive: true, force: true });
  try {
    fs.rmdirSync(TEST_RUNS_DIR);
  } catch {
    // Another run still holds files there.
  }
}
process.on('exit', removeRunDir);

const PROTECTED_PATHS = 'block-protected-paths.mjs';
const DANGEROUS_COMMANDS = 'block-dangerous-commands.mjs';
const LINT_CHANGED_FILE = 'lint-changed-file.mjs';

const BLOCK = 2;
const ALLOW = 0;

const win = (...segments) => segments.join('\\');

const CASES = [
  // block-protected-paths: posix separators
  [PROTECTED_PATHS, { file_path: 'service_a/.env' }, BLOCK, 'posix .env'],
  [PROTECTED_PATHS, { file_path: 'service_a/.env.example' }, ALLOW, 'legacy .env.example template'],
  [PROTECTED_PATHS, { file_path: 'service_a/.example.env' }, ALLOW, 'middlefix .example.env template'],
  [PROTECTED_PATHS, { file_path: '.prod.example.env' }, ALLOW, 'middlefix .prod.example.env template'],
  [PROTECTED_PATHS, { file_path: '.deploy/qa/service.example.env' }, ALLOW, 'deploy env template'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repo', '.prod.example.env') }, ALLOW, 'middlefix template, windows path'],
  [PROTECTED_PATHS, { file_path: 'service_a/.env.local' }, BLOCK, 'real .env.local'],
  [PROTECTED_PATHS, { file_path: 'service_a/.env.production' }, BLOCK, 'real .env.production'],
  [PROTECTED_PATHS, { file_path: 'secrets/.example.env' }, BLOCK, 'template under secrets stays blocked'],
  [PROTECTED_PATHS, { file_path: 'service_a/.env.examples' }, BLOCK, 'examples is not an example segment'],
  [PROTECTED_PATHS, { file_path: '.deploy/qa/service.env' }, BLOCK, 'deploy env file'],
  [PROTECTED_PATHS, { file_path: 'package-lock.json' }, BLOCK, 'lockfile'],
  [PROTECTED_PATHS, { file_path: 'service_a/app.js' }, ALLOW, 'ordinary source file'],
  [PROTECTED_PATHS, { file_path: '.deploy/local/.redis.conf' }, ALLOW, 'dotted conf under .deploy is not an env file'],
  [PROTECTED_PATHS, { file_path: 'AGENTS.md' }, ALLOW, 'documentation'],
  [PROTECTED_PATHS, { file_path: 'lefthook.yml' }, BLOCK, 'leak gate: lefthook.yml'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repo', 'lefthook.yaml') }, BLOCK, 'leak gate: lefthook.yaml, windows path'],
  [PROTECTED_PATHS, { file_path: '.gitleaks.toml' }, BLOCK, 'leak gate: .gitleaks.toml'],
  [PROTECTED_PATHS, { file_path: 'docs/lefthook.yml.md' }, ALLOW, 'a doc named after lefthook is not the config'],
  [PROTECTED_PATHS, { file_path: '.github/workflows/ci.yml' }, ALLOW, 'ci workflow is not protected'],

  // block-protected-paths: windows separators, the case a shell-quoted test cannot reach
  [PROTECTED_PATHS, { file_path: win('C:', 'repos', 'project', 'service_a', '.env') }, BLOCK, 'windows .env'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repos', 'project', '.claude', 'secrets', 'token.txt') }, BLOCK, 'windows secrets dir'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repos', 'project', 'service_a', 'app.js') }, ALLOW, 'windows source file'],

  // block-protected-paths: multi-target payloads
  [PROTECTED_PATHS, { edits: [{ file_path: 'ok.js' }, { file_path: 'package-lock.json' }] }, BLOCK, 'MultiEdit with one bad target'],
  [PROTECTED_PATHS, { edits: [{ file_path: 'ok.js' }, { file_path: 'also-ok.js' }] }, ALLOW, 'MultiEdit all clean'],

  // block-protected-paths: payloads carrying incoming content still resolve every path
  [PROTECTED_PATHS, { file_path: '.env', new_string: 'KEY=x' }, BLOCK, 'Edit with new_string on .env'],
  [PROTECTED_PATHS, { file_path: 'secrets/key.txt', content: 'x' }, BLOCK, 'Write with content into secrets'],
  [PROTECTED_PATHS, { file_path: 'ok.js', content: 'const a = 1;' }, ALLOW, 'Write with content on a source file'],
  [PROTECTED_PATHS, { file_path: 'ok.js', old_string: 'a', new_string: 'b' }, ALLOW, 'Edit with new_string on a source file'],
  [PROTECTED_PATHS, { edits: [{ file_path: 'ok.js', new_string: 'a' }, { file_path: 'secrets/k.txt', new_string: 'b' }] }, BLOCK, 'MultiEdit new_string, second target bad'],
  [PROTECTED_PATHS, { edits: [{ file_path: 'ok.js', new_string: 'a' }, { file_path: 'b.js', new_string: 'b' }] }, ALLOW, 'MultiEdit new_string all clean'],
  [PROTECTED_PATHS, { notebook_path: win('C:', 'repo', '.env') }, BLOCK, 'notebook path on .env'],

  // block-protected-paths: focused tests are blocked in every test file, e2e suite or not.
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', content: "test.only('x', async () => {});" }, BLOCK, 'test.only in a spec'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.test.js', new_string: "describe.only('x', () => {});" }, BLOCK, 'describe.only in a test file'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', new_string: "test.describe.only('x', () => {});" }, BLOCK, 'test.describe.only'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repo', 'web', 'src', 'a.spec.ts'), content: "  it.only('x', () => {});" }, BLOCK, 'it.only, windows path'],
  [PROTECTED_PATHS, { file_path: 'web/src/a.spec.ts', content: "fdescribe('x', () => {});" }, BLOCK, 'jasmine fdescribe'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', content: "test('x', async () => {});" }, ALLOW, 'ordinary test'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', content: '// never commit test.only(...)' }, ALLOW, 'focused test named in a comment'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/query.ts', content: 'builder.only(fields);' }, ALLOW, '.only outside a test file'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', content: "/* focus */ test.only('x', () => {});" }, BLOCK, 'code after a closed block comment is not a comment line'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', content: 'expect(layout.fit(box)).toBe(true);' }, ALLOW, 'a fit() method call is not jasmine fit'],
  [PROTECTED_PATHS, { file_path: 'service_a/src/a.spec.ts', edits: [{ old_string: 'a', new_string: "test.only('x', () => {});" }] }, BLOCK, 'MultiEdit entry without its own file_path'],
  [PROTECTED_PATHS, { file_path: 'a.spec.ts', content: "test.only.each([1])('x %s', () => {});" }, BLOCK, 'test.only.each'],
  [PROTECTED_PATHS, { file_path: 'a.spec.ts', content: "test.concurrent.only('x', async () => {});" }, BLOCK, 'test.concurrent.only'],
  [PROTECTED_PATHS, { file_path: 'a.spec.ts', content: "context.only('x', () => {});" }, BLOCK, 'mocha context.only'],
  [PROTECTED_PATHS, { file_path: 'a.spec.ts', content: "it\n  .only('x', () => {});" }, BLOCK, 'it.only split over two lines'],
  [PROTECTED_PATHS, { file_path: 'a.spec.ts', content: "test.skip('x', () => {});\nquery.it.only(1);" }, ALLOW, 'skip is not focus, and a member named it is not a test'],

  // block-protected-paths: NTFS and APFS are case-insensitive, so the base set is too.
  [PROTECTED_PATHS, { file_path: 'service_a/.ENV' }, BLOCK, 'uppercase .ENV'],
  [PROTECTED_PATHS, { file_path: 'PACKAGE-LOCK.JSON' }, BLOCK, 'uppercase lockfile'],
  [PROTECTED_PATHS, { file_path: win('C:', 'repo', 'Secrets', 'k.txt') }, BLOCK, 'Secrets dir, windows path'],
  [PROTECTED_PATHS, { file_path: '.envrc' }, ALLOW, '.envrc is not an env file'],

  // block-dangerous-commands
  [DANGEROUS_COMMANDS, { command: 'git commit --no-verify -m x' }, BLOCK, 'skip verification'],
  [DANGEROUS_COMMANDS, { command: 'git reset --hard origin/main' }, BLOCK, 'reset hard'],
  [DANGEROUS_COMMANDS, { command: 'git push --force' }, BLOCK, 'force push'],
  [DANGEROUS_COMMANDS, { command: 'git push -f origin x' }, BLOCK, 'force push short flag'],
  // `--follow-tags` starts with `--f` but is not a force flag.
  [DANGEROUS_COMMANDS, { command: 'git push --follow-tags origin x' }, ALLOW, 'follow-tags is not a force push'],
  // The recursive-delete rule covers `.git` ONLY. Rebuildable directories are allowed: a
  // lockfile or a compiler reproduces them. `.git` holds unpushed commits and stashes, so it
  // is the one target where the delete is unrecoverable.
  [DANGEROUS_COMMANDS, { command: 'rm -rf .git' }, BLOCK, 'remove .git destroys unpushed work'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ../other-repo/.git' }, BLOCK, 'remove .git via a path'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf node_modules' }, ALLOW, 'node_modules is rebuildable'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf dist build .venv' }, ALLOW, 'build output is rebuildable'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf .gitignore' }, ALLOW, 'the .git boundary does not catch .gitignore'],
  // Ephemeral local state is not work. Volumes, containers and dev databases are disposable,
  // so `down -v` is permitted.
  [DANGEROUS_COMMANDS, { command: 'docker compose down -v' }, ALLOW, 'local volumes are disposable'],
  [DANGEROUS_COMMANDS, { command: 'docker compose -f .deploy/local/docker-compose.yml down -v' }, ALLOW, 'compose down -v with flags between'],
  [DANGEROUS_COMMANDS, { command: 'docker compose -f x.yml down --volumes' }, ALLOW, 'compose down long volumes flag'],
  [DANGEROUS_COMMANDS, { command: 'docker compose down' }, ALLOW, 'compose down'],
  [DANGEROUS_COMMANDS, { command: 'docker compose -f x.yml up -d' }, ALLOW, 'compose up'],
  [DANGEROUS_COMMANDS, { command: 'git -C . status' }, ALLOW, 'git status'],
  [DANGEROUS_COMMANDS, { command: 'git push origin feature/x' }, ALLOW, 'ordinary push'],
  [DANGEROUS_COMMANDS, { command: 'npm run lint' }, ALLOW, 'lint'],

  // Staging secrets. Same `.env` boundary as block-protected-paths.
  [DANGEROUS_COMMANDS, { command: 'git add .env' }, BLOCK, 'stage .env'],
  [DANGEROUS_COMMANDS, { command: 'git add service_a/.env.example' }, ALLOW, 'stage legacy .env.example template'],
  [DANGEROUS_COMMANDS, { command: 'git add .example.env .prod.example.env' }, ALLOW, 'stage middlefix templates'],
  [DANGEROUS_COMMANDS, { command: 'git add .env.local' }, BLOCK, 'stage .env.local'],
  [DANGEROUS_COMMANDS, { command: 'git add src/app.js .env.production' }, BLOCK, 'stage a real env file among others'],
  [DANGEROUS_COMMANDS, { command: 'git add secrets/.example.env' }, BLOCK, 'stage a template under secrets'],
  [DANGEROUS_COMMANDS, { command: 'git add secrets/token.txt' }, BLOCK, 'stage secrets dir'],
  [DANGEROUS_COMMANDS, { command: 'git stage secrets/token.txt' }, BLOCK, 'git stage alias'],
  [DANGEROUS_COMMANDS, { command: 'git add src/app.js' }, ALLOW, 'stage a source file'],

  // Repo-wide formatter / linter sweeps: a whole-tree --write or --fix rewrites files the
  // session never touched. Only the invoked command's own path targets count.
  [DANGEROUS_COMMANDS, { command: 'prettier --write .' }, BLOCK, 'prettier write dot'],
  [DANGEROUS_COMMANDS, { command: 'npx prettier -w .' }, BLOCK, 'npx prettier -w dot'],
  [DANGEROUS_COMMANDS, { command: 'prettier --write "*"' }, BLOCK, 'prettier write quoted star'],
  [DANGEROUS_COMMANDS, { command: "npx prettier --write './'" }, BLOCK, 'prettier write quoted dot-slash'],
  [DANGEROUS_COMMANDS, { command: 'pnpm exec prettier --write .' }, BLOCK, 'pnpm exec prettier'],
  [DANGEROUS_COMMANDS, { command: 'eslint .' }, BLOCK, 'eslint dot'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint . --fix' }, BLOCK, 'npx eslint dot then fix'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --fix .' }, BLOCK, 'npx eslint fix then dot'],
  [DANGEROUS_COMMANDS, { command: 'npm exec -- eslint --fix "**"' }, BLOCK, 'npm exec eslint globstar'],
  [DANGEROUS_COMMANDS, { command: 'cd pkg && npx eslint .' }, BLOCK, 'sweep after cd'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint src/a.ts > .me/x/logs/lint.log 2>&1' }, ALLOW, 'redirect target is not a lint target'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint src/a.ts *> .me\\x\\logs\\lint.log' }, ALLOW, 'PS all-streams redirect'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint src/a.ts 2> .me/x/err.log | tail -40' }, ALLOW, 'stderr redirect and pipe'],
  [DANGEROUS_COMMANDS, { command: 'cd pkg && npx prettier --write src/a.ts' }, ALLOW, 'prettier on a file after cd'],
  [DANGEROUS_COMMANDS, { command: "git log -S 'eslint .'" }, ALLOW, 'eslint inside a quoted argument'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint src/a.ts src/b.ts' }, ALLOW, 'eslint on explicit files'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --fix src/a.ts' }, ALLOW, 'eslint fix on an explicit file'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint ./src/a.ts' }, ALLOW, 'explicit file under dot-slash'],
  [DANGEROUS_COMMANDS, { command: 'prettier --check src/a.ts' }, ALLOW, 'prettier check on a file'],
  [DANGEROUS_COMMANDS, { command: 'prettier --check .' }, ALLOW, 'prettier check writes nothing'],
  // ESLint 9 lints the working directory when given no path, so no target is a sweep.
  [DANGEROUS_COMMANDS, { command: 'npx eslint' }, BLOCK, 'eslint with no targets'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --fix' }, BLOCK, 'eslint fix with no targets'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint ./.' }, BLOCK, 'eslint dot-slash-dot'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint "**/*.ts"' }, BLOCK, 'eslint globstar pattern'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --version' }, ALLOW, 'eslint version prints information'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --print-config src/a.ts' }, ALLOW, 'eslint print-config lints nothing'],
  [DANGEROUS_COMMANDS, { command: 'npx prettier --check src/a.ts && npx eslint src/a.ts' }, ALLOW, 'targeted prettier and eslint together'],
  [DANGEROUS_COMMANDS, { command: 'bash -c "npx eslint --fix ."' }, BLOCK, 'sweep inside bash -c'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint (git diff --name-only)' }, ALLOW, 'PS subexpression supplies the targets'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint $(git diff --name-only)' }, ALLOW, 'bash substitution supplies the targets'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint --resolve-plugins-relative-to . src/a.ts' }, ALLOW, 'dot as a value-taking option value'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint -c . src/a.ts' }, ALLOW, 'dot as the -c value'],
  [DANGEROUS_COMMANDS, { command: 'npx prettier --config ./p.json --write .' }, BLOCK, 'prettier config value then write dot'],
  [DANGEROUS_COMMANDS, { command: 'npx eslint -f json .' }, BLOCK, 'eslint format value then dot'],

  // PowerShell forms. The hook is wired to both shells because PowerShell is typically the
  // primary shell on a Windows host; a Bash-only hook leaves every rule above reachable
  // through the other tool.
  // Same `.git`-only scope as the rm rule, with flags in any order, abbreviated, through the
  // Remove-Item aliases, and through a pipeline that feeds the delete.
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force .git' }, BLOCK, 'PS remove .git'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Force -Recurse .\\.git' }, BLOCK, 'PS remove .git, flags reordered'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item .git -Recurse -Force' }, BLOCK, 'PS target before flags'],
  [DANGEROUS_COMMANDS, { command: 'remove-item -recurse -force .git' }, BLOCK, 'PS lowercase cmdlet'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Rec -Force .git' }, BLOCK, 'PS abbreviated -Rec'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force node_modules' }, ALLOW, 'PS node_modules is rebuildable'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item scratch.txt' }, ALLOW, 'PS remove a single file'],
  [DANGEROUS_COMMANDS, { command: 'Get-ChildItem .git' }, ALLOW, 'PS list is not a delete'],
  [DANGEROUS_COMMANDS, { command: 'git commit --no-verify -m x' }, BLOCK, 'PS shares the git patterns'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item .git -r -fo' }, BLOCK, 'PS abbreviated -r and -fo'],
  [DANGEROUS_COMMANDS, { command: 'gci .git | Remove-Item -Recurse -Force' }, BLOCK, 'PS pipeline into Remove-Item'],
  [DANGEROUS_COMMANDS, { command: "Get-ChildItem -Force | Where-Object Name -eq '.git' | ri -Recurse" }, BLOCK, 'PS filtered pipeline into the ri alias'],
  [DANGEROUS_COMMANDS, { command: 'rd /s /q .git' }, BLOCK, 'cmd rd /s'],
  [DANGEROUS_COMMANDS, { command: 'gci -r -filter .git | % { Remove-Item $_ -Recurse -Force }' }, BLOCK, 'PS ForEach-Object block deleting .git'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf $(find . -name .git)' }, BLOCK, 'rm of a substitution that finds .git'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf $(find . -name dist)' }, ALLOW, 'rm of a substitution without .git'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Force .gitignore' }, ALLOW, 'PS -Force is not a recursive flag and .gitignore is not .git'],
  [DANGEROUS_COMMANDS, { command: 'Get-ChildItem .git | Measure-Object' }, ALLOW, 'PS pipeline without a delete'],
  [DANGEROUS_COMMANDS, { command: 'Get-ChildItem .git; Remove-Item -Recurse dist' }, ALLOW, 'PS .git listed in another group'],

  // Measured bypasses of the raw-string patterns: flags in any position, git global
  // options, the git.exe basename, short and clustered flags, +refspec, rm flag orders.
  [DANGEROUS_COMMANDS, { command: 'git push origin main --force' }, BLOCK, 'force flag after the refspec'],
  [DANGEROUS_COMMANDS, { command: 'git push origin +main' }, BLOCK, 'force via +refspec'],
  [DANGEROUS_COMMANDS, { command: 'git push -fu origin main' }, BLOCK, 'force inside a short cluster'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin x' }, BLOCK, 'force-with-lease to an unprefixed branch'],

  // A lease-protected force push is allowed only toward a developer's own work branch.
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin feature/add-login' }, ALLOW, 'lease to feature/'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin bugfix/fix-timeout' }, ALLOW, 'lease to bugfix/'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin hotfix/fix-timeout' }, BLOCK, 'lease to hotfix/, not a work prefix'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin chore/cleanup' }, BLOCK, 'lease to chore/, not a work prefix'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease=feature/x:abc123 origin feature/x' }, ALLOW, 'lease with an expected value'],
  [DANGEROUS_COMMANDS, { command: 'git push origin feature/x --force-with-lease' }, ALLOW, 'lease flag after the refspec'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin HEAD:feature/x' }, ALLOW, 'lease with src:dst to a work branch'],
  [DANGEROUS_COMMANDS, { command: 'git push -u --force-with-lease origin refs/heads/bugfix/x' }, ALLOW, 'lease to a full ref of a work branch'],
  [DANGEROUS_COMMANDS, { command: 'git.exe push --force-with-lease origin feature/x' }, ALLOW, 'lease through git.exe'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin main' }, BLOCK, 'lease to main'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin release/staging' }, BLOCK, 'lease to a release branch'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin develop' }, BLOCK, 'lease to develop'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin feature/x:main' }, BLOCK, 'lease with src:dst to main'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin HEAD' }, BLOCK, 'lease to HEAD is not a named branch'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease' }, BLOCK, 'lease without a named branch'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin' }, BLOCK, 'lease with a remote but no branch'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin feature/x main' }, BLOCK, 'lease with one shared branch among two'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease --all origin' }, BLOCK, 'lease with --all'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin feature/' }, BLOCK, 'bare prefix is not a branch'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease --force origin feature/x' }, BLOCK, 'lease plus --force'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease -f origin feature/x' }, BLOCK, 'lease plus -f'],
  [DANGEROUS_COMMANDS, { command: 'git push --force-with-lease origin +feature/x' }, BLOCK, 'lease plus +refspec'],
  [DANGEROUS_COMMANDS, { command: 'git push --force origin feature/x' }, BLOCK, 'plain --force to a work branch'],
  [DANGEROUS_COMMANDS, { command: 'git push -f origin feature/x' }, BLOCK, 'plain -f to a work branch'],
  [DANGEROUS_COMMANDS, { command: 'pwsh -Command "git push --force-with-lease origin main"' }, BLOCK, 'nested lease to main'],

  // Recursive deletes of a root, the home directory or the repo root, in both shells.
  [DANGEROUS_COMMANDS, { command: 'rm -rf /' }, BLOCK, 'rm -rf /'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf /*' }, BLOCK, 'rm -rf /*'],
  [DANGEROUS_COMMANDS, { command: 'rm -fr /' }, BLOCK, 'rm -fr /'],
  [DANGEROUS_COMMANDS, { command: 'rm -r -f /' }, BLOCK, 'rm split flags on /'],
  [DANGEROUS_COMMANDS, { command: 'rm --recursive --force -- /' }, BLOCK, 'rm long flags on / after --'],
  [DANGEROUS_COMMANDS, { command: 'sudo rm -rf /' }, BLOCK, 'rm / behind sudo'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ~' }, BLOCK, 'rm -rf ~'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ~/' }, BLOCK, 'rm -rf ~/'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf "$HOME"' }, BLOCK, 'rm -rf quoted $HOME'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ${HOME}/*' }, BLOCK, 'rm -rf ${HOME}/*'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf .' }, BLOCK, 'rm -rf .'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ./' }, BLOCK, 'rm -rf ./'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ./*' }, BLOCK, 'rm -rf ./*'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf /c' }, BLOCK, 'rm -rf Git Bash drive root'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf /c/' }, BLOCK, 'rm -rf Git Bash drive root with slash'],
  [DANGEROUS_COMMANDS, { command: 'bash -c "rm -rf ~"' }, BLOCK, 'nested rm -rf ~'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force C:\\' }, BLOCK, 'PS remove C:\\'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Path D: -Recurse' }, BLOCK, 'PS remove drive D:'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force ~' }, BLOCK, 'PS remove ~'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item $env:USERPROFILE -Recurse -Force' }, BLOCK, 'PS remove $env:USERPROFILE'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force ${env:USERPROFILE}' }, BLOCK, 'PS remove ${env:USERPROFILE}'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force .\\' }, BLOCK, 'PS remove .\\'],
  [DANGEROUS_COMMANDS, { command: 'ri . -r -fo' }, BLOCK, 'PS ri alias on .'],
  [DANGEROUS_COMMANDS, { command: 'rm -Recurse -Force .' }, BLOCK, 'PS rm alias on .'],
  [DANGEROUS_COMMANDS, { command: 'rd /s /q C:\\' }, BLOCK, 'cmd rd /s on a drive root'],
  [DANGEROUS_COMMANDS, { command: 'cmd /c rmdir /s /q .' }, BLOCK, 'nested cmd rmdir /s on .'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ./dist' }, ALLOW, 'rm -rf ./dist'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ~/projects/scratch' }, ALLOW, 'rm -rf a directory under ~'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf $HOME/.cache/x' }, ALLOW, 'rm -rf a directory under $HOME'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf /tmp/build-1' }, ALLOW, 'rm -rf an absolute build dir'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf .me/test-runs' }, ALLOW, 'rm -rf a dot directory'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf ..' }, ALLOW, 'parent directory is outside the protected set'],
  [DANGEROUS_COMMANDS, { command: 'rm -f .' }, ALLOW, 'rm without recursion cannot remove a directory'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force C:\\temp\\build' }, ALLOW, 'PS remove a directory under a drive'],
  [DANGEROUS_COMMANDS, { command: 'Remove-Item -Recurse -Force $env:TEMP\\x' }, ALLOW, 'PS remove under $env:TEMP'],
  [DANGEROUS_COMMANDS, { command: 'rd /s /q dist' }, ALLOW, 'cmd rd /s on a build dir'],
  [DANGEROUS_COMMANDS, { command: 'git clean -fd' }, ALLOW, 'git clean -fd'],
  [DANGEROUS_COMMANDS, { command: 'git restore .' }, ALLOW, 'git restore .'],
  [DANGEROUS_COMMANDS, { command: 'git checkout -- src/app.js' }, ALLOW, 'git checkout -- file'],
  [DANGEROUS_COMMANDS, { command: 'git -C . reset --hard' }, BLOCK, 'reset hard behind -C'],
  [DANGEROUS_COMMANDS, { command: 'git -c user.name=x reset --hard HEAD~1' }, BLOCK, 'reset hard behind -c'],
  [DANGEROUS_COMMANDS, { command: 'git.exe reset --hard' }, BLOCK, 'git.exe basename'],
  [DANGEROUS_COMMANDS, { command: '"C:\\Program Files\\Git\\bin\\Git.EXE" reset --hard' }, BLOCK, 'quoted absolute Git.EXE'],
  [DANGEROUS_COMMANDS, { command: '& git reset --hard' }, BLOCK, 'PS call operator'],
  [DANGEROUS_COMMANDS, { command: 'git commit -n' }, BLOCK, 'bare commit -n skips hooks'],
  [DANGEROUS_COMMANDS, { command: 'git commit -n -m x' }, BLOCK, 'commit -n skips hooks'],
  [DANGEROUS_COMMANDS, { command: 'find . -name .git | xargs rm -rf' }, BLOCK, 'xargs rm fed a .git path'],
  [DANGEROUS_COMMANDS, { command: 'find . -name dist | xargs rm -rf' }, ALLOW, 'xargs rm without .git'],
  [DANGEROUS_COMMANDS, { command: 'git commit -anm x' }, BLOCK, 'commit -n inside a cluster'],
  [DANGEROUS_COMMANDS, { command: 'git -c core.hooksPath=/dev/null commit -m x' }, BLOCK, 'hooksPath override'],
  [DANGEROUS_COMMANDS, { command: 'rm -fr .git' }, BLOCK, 'rm -fr'],
  [DANGEROUS_COMMANDS, { command: 'rm -r -f .git' }, BLOCK, 'rm split flags'],
  [DANGEROUS_COMMANDS, { command: 'rm --recursive --force .GIT' }, BLOCK, 'rm long flags, uppercase .GIT'],
  [DANGEROUS_COMMANDS, { command: 'rm -rf .git/objects' }, BLOCK, 'rm inside .git'],
  [DANGEROUS_COMMANDS, { command: 'sudo rm -rf ./.git/' }, BLOCK, 'rm behind sudo'],
  [DANGEROUS_COMMANDS, { command: 'bash -c "git reset --hard"' }, BLOCK, 'nested bash -c'],
  [DANGEROUS_COMMANDS, { command: 'pwsh -Command "git push --force"' }, BLOCK, 'nested pwsh -Command'],
  [DANGEROUS_COMMANDS, { command: 'cmd /c git reset --hard' }, BLOCK, 'nested cmd /c'],
  [DANGEROUS_COMMANDS, { command: `pwsh -EncodedCommand ${Buffer.from('git reset --hard', 'utf16le').toString('base64')}` }, BLOCK, 'nested pwsh -EncodedCommand'],
  [DANGEROUS_COMMANDS, { command: 'echo $(git reset --hard)' }, BLOCK, 'command substitution'],
  [DANGEROUS_COMMANDS, { command: 'git push -n origin x' }, ALLOW, 'push -n is a dry run, not a hook skip'],
  [DANGEROUS_COMMANDS, { command: 'git push --follow-tags' }, ALLOW, 'follow-tags without a remote'],
  [DANGEROUS_COMMANDS, { command: 'git commit -m "feat: add parser"' }, ALLOW, 'ordinary commit'],
  [DANGEROUS_COMMANDS, { command: 'git commit -am "wip"' }, ALLOW, 'commit -am'],
  [DANGEROUS_COMMANDS, { command: 'git commit -m -n' }, ALLOW, 'message value -n is not the flag'],
  [DANGEROUS_COMMANDS, { command: 'git reset --soft HEAD~1' }, ALLOW, 'reset soft'],
  [DANGEROUS_COMMANDS, { command: 'git branch -d merged-branch' }, ALLOW, 'safe branch delete'],
  [DANGEROUS_COMMANDS, { command: 'git fetch --force origin' }, ALLOW, 'force on fetch is not a push'],

  // Quoted text is an argument, never a flag.
  [DANGEROUS_COMMANDS, { command: 'git commit -m "docs: never use --no-verify here"' }, ALLOW, '--no-verify inside a commit message'],
  [DANGEROUS_COMMANDS, { command: "git commit -m 'explain why git reset --hard is blocked'" }, ALLOW, 'reset hard inside a commit message'],
  [DANGEROUS_COMMANDS, { command: 'rg "git reset --hard" docs/' }, ALLOW, 'search pattern naming a blocked command'],
  [DANGEROUS_COMMANDS, { command: "grep -rn 'rm -rf .git' ." }, ALLOW, 'grep pattern naming rm -rf .git'],
  [DANGEROUS_COMMANDS, { command: "cat > notes.md <<'EOF'\nNever run git reset --hard or git push --force.\nEOF" }, ALLOW, 'heredoc body is text'],
  [DANGEROUS_COMMANDS, { command: "cat <<EOF > notes.md\nrm -rf .git\nEOF\ngit push --force" }, BLOCK, 'command after a heredoc is still read'],

  // D-10: other ways to lose work.
  [DANGEROUS_COMMANDS, { command: 'git clean -f' }, ALLOW, 'clean -f'],
  [DANGEROUS_COMMANDS, { command: 'git clean -fdx' }, ALLOW, 'clean -fdx'],
  [DANGEROUS_COMMANDS, { command: 'git clean -xdf' }, ALLOW, 'clean -xdf'],
  [DANGEROUS_COMMANDS, { command: 'git clean --force -d' }, ALLOW, 'clean --force'],
  [DANGEROUS_COMMANDS, { command: 'git clean -n' }, ALLOW, 'clean dry run'],
  [DANGEROUS_COMMANDS, { command: 'git branch --delete --force x' }, BLOCK, 'branch --delete --force'],
  [DANGEROUS_COMMANDS, { command: 'git branch -d -f x' }, BLOCK, 'branch -d -f'],
  [DANGEROUS_COMMANDS, { command: 'git branch -df x' }, BLOCK, 'branch -df'],
  [DANGEROUS_COMMANDS, { command: 'git branch -D x' }, BLOCK, 'branch -D'],

  // D-11: shell writes into environment files and secrets.
  [DANGEROUS_COMMANDS, { command: 'echo KEY=x > .env' }, ALLOW, 'redirect into .env'],
  [DANGEROUS_COMMANDS, { command: 'echo KEY=x >> service_a/.env.local' }, ALLOW, 'append into .env.local'],
  [DANGEROUS_COMMANDS, { command: 'echo KEY=x>.env' }, ALLOW, 'redirect without spaces'],
  [DANGEROUS_COMMANDS, { command: 'cat .env.example > .ENV' }, ALLOW, 'redirect into uppercase .ENV'],
  [DANGEROUS_COMMANDS, { command: 'node gen.js 2> secrets/err.log' }, ALLOW, 'stderr redirect into secrets'],
  [DANGEROUS_COMMANDS, { command: "'KEY=x' *> .env" }, ALLOW, 'PS all-streams redirect into .env'],
  [DANGEROUS_COMMANDS, { command: 'echo KEY=x | tee -a .env' }, ALLOW, 'tee into .env'],
  [DANGEROUS_COMMANDS, { command: 'Set-Content -Path .env -Value "KEY=x"' }, ALLOW, 'PS Set-Content .env'],
  [DANGEROUS_COMMANDS, { command: '"KEY=x" | Out-File .env.local' }, ALLOW, 'PS Out-File .env.local'],
  [DANGEROUS_COMMANDS, { command: 'Add-Content .env "KEY=x"' }, ALLOW, 'PS Add-Content .env'],
  [DANGEROUS_COMMANDS, { command: 'cat .env' }, ALLOW, 'reading .env is not a write'],
  [DANGEROUS_COMMANDS, { command: 'node app.js < .env' }, ALLOW, 'input redirect from .env'],
  [DANGEROUS_COMMANDS, { command: 'echo x > .envrc' }, ALLOW, '.envrc is not an env file'],
  [DANGEROUS_COMMANDS, { command: 'npm test > .me/x/logs/test.log 2>&1' }, ALLOW, 'log redirect'],
  [DANGEROUS_COMMANDS, { command: 'echo x | tee .me/x/out.log' }, ALLOW, 'tee into a log'],

  // lint-changed-file: only the environment-independent paths are asserted here, so the
  // suite stays runnable with no eslint, no ruff and no project install. Whether a real
  // lint failure surfaces depends on the linters present, which is not this suite's job.
  [LINT_CHANGED_FILE, { file_path: 'does-not-exist-on-purpose.js' }, ALLOW, 'missing file is skipped'],
  [LINT_CHANGED_FILE, { file_path: 'notes.txt' }, ALLOW, 'unlintable extension is skipped']
];

/**
 * A payload the harness never sends must not wedge the hook: an empty stdin has to
 * fail open, otherwise every tool call breaks the moment the payload shape changes.
 */
const EMPTY_STDIN_CASES = [PROTECTED_PATHS, DANGEROUS_COMMANDS, LINT_CHANGED_FILE];

/**
 * Unparseable stdin splits by hook purpose, and the split is deliberate. A guardrail that
 * cannot read the payload has not cleared the call, so it fails CLOSED. The linter is
 * advisory and runs after the write already happened, so it fails OPEN rather than
 * reporting a lint error nobody can act on.
 */
const MALFORMED_STDIN_CASES = [
  [PROTECTED_PATHS, '{ not json at all', BLOCK, 'unparseable payload fails closed'],
  [DANGEROUS_COMMANDS, '{ not json at all', BLOCK, 'unparseable payload fails closed'],
  [LINT_CHANGED_FILE, '{ not json at all', ALLOW, 'unparseable payload fails open'],
  [PROTECTED_PATHS, 'null', BLOCK, 'null payload fails closed'],
  [DANGEROUS_COMMANDS, 'null', BLOCK, 'null payload fails closed'],
  [LINT_CHANGED_FILE, 'null', ALLOW, 'null payload fails open'],
  [DANGEROUS_COMMANDS, '[1, 2]', BLOCK, 'array payload fails closed'],
  [PROTECTED_PATHS, '"just a string"', BLOCK, 'string payload fails closed'],
  [DANGEROUS_COMMANDS, '{"tool_input": null}', ALLOW, 'object payload without a command is not a shell call'],
  [DANGEROUS_COMMANDS, '{"tool_input": {"command": 42}}', ALLOW, 'non-string command is not a shell call'],
  [PROTECTED_PATHS, '{"tool_input": {"file_path": 7, "edits": [null, 3]}}', ALLOW, 'object payload without a path string']
];

/**
 * Runs a hook and returns its exit status. A hook that exits through its internal-error
 * handler returns the string 'crash' instead, so a crash that happens to exit 2 is never
 * counted as a deliberate BLOCK.
 */
function runHookFile(scriptPath, stdin) {
  const result = spawnSync(process.execPath, [scriptPath], {
    input: stdin,
    encoding: 'utf8'
  });
  if (/internal error/.test(result.stderr || '')) {
    return 'crash';
  }
  return result.status;
}

function runHook(script, stdin) {
  return runHookFile(path.join(HOOKS_DIR, script), stdin);
}

let failures = 0;
let skips = 0;

function report(script, label, expected, actual) {
  const passed = actual === expected;
  if (!passed) {
    failures += 1;
  }
  if (VERBOSE || !passed) {
    console.log(`${passed ? 'ok  ' : 'FAIL'} ${script} :: ${label} (expected ${expected}, got ${actual})`);
  }
}

function reportSkip(message) {
  skips += 1;
  console.log(`skip ${message}`);
}

for (const [script, toolInput, expected, label] of CASES) {
  const payload = JSON.stringify({ tool_name: 'Test', tool_input: toolInput });
  report(script, label, expected, runHook(script, payload));
}

for (const script of EMPTY_STDIN_CASES) {
  report(script, 'empty stdin fails open', ALLOW, runHook(script, ''));
}

for (const [script, stdin, expected, label] of MALFORMED_STDIN_CASES) {
  report(script, label, expected, runHook(script, stdin));
}

/**
 * A missing linter must not read as a lint failure.
 *
 * The hook spawns ruff without a shell, so an absent binary surfaces as `result.error`
 * (ENOENT) and is treated as "not configured here". Left unhandled it would block every
 * Python write on any host without ruff.
 *
 * The case is conditional because it can only prove anything where ruff is genuinely
 * absent. Where ruff IS installed the same file would be a real lint failure, and
 * asserting ALLOW would be wrong -- so it is skipped and said out loud rather than
 * quietly counted as a pass.
 */
let conditionalCases = 0;
const ruffProbe = spawnSync('ruff', ['--version'], { encoding: 'utf8' });
const ruffPresent = !ruffProbe.error && ruffProbe.status === 0;

if (ruffPresent) {
  reportSkip('lint-changed-file.mjs :: missing ruff fails open (ruff IS installed here)');
} else {
  const scratch = path.join(RUN_DIR, 'missing-linter.py');
  // Deliberately ruff-violating: an unused import plus an undefined name.
  fs.writeFileSync(scratch, 'import os\n\n\ndef f():\n    return undefined_name\n');
  try {
    const payload = JSON.stringify({ tool_name: 'Write', tool_input: { file_path: scratch } });
    report(LINT_CHANGED_FILE, 'missing ruff fails open', ALLOW, runHook(LINT_CHANGED_FILE, payload));
    conditionalCases += 1;
  } finally {
    fs.rmSync(scratch, { force: true });
  }
}

/**
 * Generator tokens in block-protected-paths.mjs.
 *
 * The e2e-scoped wait rules and the extra protected pattern depend on what the generator
 * substituted, so their logic is proven against temporary copies rendered here with neutral
 * values. That keeps the suite identical and green in the unrendered template, in a repo with
 * an e2e suite and in a repo without one. The live hook is then checked with its own
 * `E2E_DIR`, read from its source so this file carries no second token that could drift, and
 * those cases are skipped out loud when the repo has no e2e suite.
 */
const E2E_DIR_DECLARATION = /^\/\/ @E2E_DIR:(.*)$/m;
const EXTRA_DECLARATION = /^\/\/ @EXTRA_PROTECTED_PATTERNS:(.*)$/m;
const PLACEHOLDER = /^\{\{[A-Z0-9_]+\}\}$/;
const SAMPLE_E2E_DIR = 'sample-e2e';

let renderCount = 0;

/**
 * Writes a copy of the protected-paths hook with both generator values replaced, the way
 * the generator does it (plain text in place of the token), and returns its path. Throws
 * when the marker lines changed shape, so a refactor of the hook cannot silently turn
 * these cases into no-ops.
 */
function renderHookCopy(source, dir, e2eDir, extraSource) {
  if (!E2E_DIR_DECLARATION.test(source) || !EXTRA_DECLARATION.test(source)) {
    throw new Error('block-protected-paths.mjs no longer carries the // @E2E_DIR: / // @EXTRA_PROTECTED_PATTERNS: lines');
  }
  const rendered = source
    .replace(E2E_DIR_DECLARATION, () => `// @E2E_DIR: ${e2eDir}`)
    .replace(EXTRA_DECLARATION, () => `// @EXTRA_PROTECTED_PATTERNS: ${extraSource}`);
  renderCount += 1;
  const target = path.join(dir, `render-${renderCount}.mjs`);
  fs.writeFileSync(target, rendered);
  return target;
}

const e2e = (...segments) => [SAMPLE_E2E_DIR, ...segments].join('/');
const NO_COMMENT_WAIT = 'await page.waitForTimeout(3000);';
const COMMENTED_WAIT = '// The drawer animation has no DOM signal to wait on.\nawait page.waitForTimeout(3000);';

const E2E_RULE_CASES = [
  [{ file_path: e2e('tests', 'a.spec.ts'), content: NO_COMMENT_WAIT }, BLOCK, 'waitForTimeout without a comment'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: COMMENTED_WAIT }, ALLOW, 'waitForTimeout with a comment above'],
  [{ file_path: e2e('tests', 'a.spec.ts'), new_string: 'await page.waitForTimeout(500); // lets the toast settle' }, ALLOW, 'waitForTimeout with a comment on the same line'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Why.\n\nawait page.waitForTimeout(500);' }, BLOCK, 'blank line between comment and wait'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '/* Backend debounce.\n */\nawait page.waitForTimeout(500);' }, ALLOW, 'wait below a closed multi-line block comment'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Queue drain.\nawait page.waitForTimeout(150000);' }, BLOCK, 'commented wait over the 120 s cap'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Queue drain.\nawait page.waitForTimeout(120_000);' }, ALLOW, 'commented wait at the 120 s cap'],
  [{ file_path: e2e('src', 'utils', 'evidence.ts'), content: '// Cosmetic pause.\nawait page.waitForTimeout(ms);' }, ALLOW, 'commented non-literal wait'],
  [{ file_path: e2e('src', 'utils', 'evidence.ts'), content: 'await page.waitForTimeout(ms);' }, BLOCK, 'uncommented non-literal wait'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: 'await new Promise((r) => setTimeout(r, 500));' }, BLOCK, 'sleep promise without a comment'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Backend debounce.\nawait new Promise((r) => setTimeout(r, 500));' }, ALLOW, 'sleep promise with a comment'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Long.\nawait new Promise(resolve => {\n  setTimeout(resolve, 200000);\n});' }, BLOCK, 'multi-line sleep promise over the cap'],
  [{ file_path: win('C:', 'repo', SAMPLE_E2E_DIR, 'tests', 'a.spec.ts'), content: NO_COMMENT_WAIT }, BLOCK, 'uncommented wait, windows path'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: '// Avoid waitForTimeout(5000) here.' }, ALLOW, 'wait named inside a comment'],
  [{ file_path: e2e('AGENTS.md'), content: NO_COMMENT_WAIT }, ALLOW, 'documentation in the e2e dir'],
  [{ file_path: `other-${SAMPLE_E2E_DIR}/tests/a.spec.ts`, content: NO_COMMENT_WAIT }, ALLOW, 'look-alike directory name'],
  [{ file_path: 'service_a/src/poll.ts', content: NO_COMMENT_WAIT }, ALLOW, 'wait outside the e2e dir'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: `${COMMENTED_WAIT}\ntest.only('x', () => {});` }, BLOCK, 'focused test inside the e2e dir']
];

const NO_E2E_CASES = [
  [{ file_path: e2e('tests', 'a.spec.ts'), content: NO_COMMENT_WAIT }, ALLOW, 'wait rules are off'],
  [{ file_path: e2e('tests', 'a.spec.ts'), content: "test.only('x', () => {});" }, BLOCK, 'focused tests are still blocked'],
  [{ file_path: 'docs/test-plan.xlsx' }, ALLOW, 'empty extra pattern adds nothing']
];

const EXTRA_SAMPLE = String.raw`\.xlsx$|(^|/)plans/`;
const EXTRA_CASES = [
  [{ file_path: 'docs/test-plan.XLSX' }, BLOCK, 'matches case-insensitively'],
  [{ file_path: win('C:', 'repo', 'plans', 'q3.md') }, BLOCK, 'second alternative, windows path'],
  [{ file_path: 'docs/plan.md' }, ALLOW, 'does not over-match'],
  [{ file_path: '.env' }, BLOCK, 'keeps the base set']
];

// Rendered values carrying every character that breaks a JavaScript string literal: a
// quote, a backtick, `${` and parentheses. The hook must neither crash nor fail open.
const HOSTILE_E2E_DIR = "it's `e2e` ${x} (a)";
const HOSTILE_EXTRA = String.raw`(^|/)o'k` + '`' + String.raw`\$\{x\}\(1\)/`;
const HOSTILE_CASES = [
  [{ file_path: `${HOSTILE_E2E_DIR}/tests/a.spec.ts`, content: NO_COMMENT_WAIT }, BLOCK, 'hostile e2e dir still scopes the wait rule'],
  [{ file_path: `${HOSTILE_E2E_DIR}/tests/a.spec.ts`, content: COMMENTED_WAIT }, ALLOW, 'hostile e2e dir allows a commented wait'],
  [{ file_path: 'src/poll.ts', content: NO_COMMENT_WAIT }, ALLOW, 'hostile e2e dir does not over-match'],
  [{ file_path: "docs/o'k`${x}(1)/plan.md" }, BLOCK, 'hostile extra pattern matches its literal path'],
  [{ file_path: 'docs/plan.md' }, ALLOW, 'hostile extra pattern does not over-match'],
  [{ file_path: '.env' }, BLOCK, 'hostile render keeps the base set']
];
const INVALID_EXTRA_CASES = [
  [{ file_path: 'src/app.ts', content: 'const a = 1;' }, BLOCK, 'uncompilable extra pattern fails closed with a message']
];

let renderedCases = 0;
const renderDir = fs.mkdtempSync(path.join(RUN_DIR, 'render-'));
try {
  const hookSource = fs.readFileSync(path.join(HOOKS_DIR, PROTECTED_PATHS), 'utf8');
  const suites = [
    [renderHookCopy(hookSource, renderDir, SAMPLE_E2E_DIR, ''), `e2e dir '${SAMPLE_E2E_DIR}'`, E2E_RULE_CASES],
    [renderHookCopy(hookSource, renderDir, '', ''), 'no e2e dir, no extra pattern', NO_E2E_CASES],
    [renderHookCopy(hookSource, renderDir, '', EXTRA_SAMPLE), 'extra pattern', EXTRA_CASES],
    [renderHookCopy(hookSource, renderDir, HOSTILE_E2E_DIR, HOSTILE_EXTRA), 'hostile values', HOSTILE_CASES],
    [renderHookCopy(hookSource, renderDir, '', 'a(b'), 'invalid extra pattern', INVALID_EXTRA_CASES]
  ];
  for (const [scriptPath, suiteLabel, cases] of suites) {
    for (const [toolInput, expected, label] of cases) {
      const payload = JSON.stringify({ tool_name: 'Test', tool_input: toolInput });
      report(PROTECTED_PATHS, `rendered, ${suiteLabel} :: ${label}`, expected, runHookFile(scriptPath, payload));
      renderedCases += 1;
    }
  }

  const liveValue = E2E_DIR_DECLARATION.exec(hookSource)[1].trim();
  const liveE2eDir = PLACEHOLDER.test(liveValue) ? '' : liveValue;
  if (liveE2eDir === '') {
    reportSkip(`${PROTECTED_PATHS} :: live e2e dir cases (this repo has no e2e suite)`);
  } else {
    const livePath = `${liveE2eDir}/tests/live-check.spec.ts`;
    const liveCases = [
      [{ file_path: livePath, content: NO_COMMENT_WAIT }, BLOCK, `live e2e dir '${liveE2eDir}' blocks an uncommented wait`],
      [{ file_path: livePath, content: COMMENTED_WAIT }, ALLOW, `live e2e dir '${liveE2eDir}' allows a commented wait`]
    ];
    for (const [toolInput, expected, label] of liveCases) {
      const payload = JSON.stringify({ tool_name: 'Test', tool_input: toolInput });
      report(PROTECTED_PATHS, label, expected, runHook(PROTECTED_PATHS, payload));
      renderedCases += 1;
    }
  }
} finally {
  fs.rmSync(renderDir, { recursive: true, force: true });
}

/**
 * lint-changed-file against a fixture repository with a stand-in eslint.
 *
 * The stand-in is the boundary: a `node_modules/eslint` package whose bin prints an eslint
 * JSON report driven by markers in the linted file, so no real eslint or network is
 * needed. The fixture puts the only eslint config at the repository root (a `.git` dir)
 * and the file in a nested package whose directory and name carry a space and an `&`, so
 * a BLOCK proves the config was found up the tree and the path reached eslint intact:
 * the stand-in reports nothing when it cannot read the path it was given.
 */
const FAKE_ESLINT_BIN = `
const fs = require('node:fs');
const file = process.argv[process.argv.length - 1];
let text = '';
try {
  text = fs.readFileSync(file, 'utf8');
} catch {
  text = '';
}
const messages = [];
text.split(/\\r?\\n/).forEach((lineText, index) => {
  const line = index + 1;
  if (lineText.includes('LINT_ERROR_MARKER')) {
    messages.push({ severity: 2, message: 'marked error', ruleId: 'fake/error', line, column: 1 });
  }
  if (lineText.includes('LINT_WARNING_MARKER')) {
    messages.push({ severity: 1, message: 'marked warning', ruleId: 'fake/warning', line, column: 1 });
  }
  if (lineText.includes('LINT_FATAL_MARKER')) {
    messages.push({ severity: 2, fatal: true, message: 'Parsing error', line, column: 1 });
  }
});
if (text.includes('LINT_IGNORED_MARKER')) {
  messages.push({ severity: 1, message: 'File ignored because of a matching ignore pattern.' });
}
process.stdout.write(JSON.stringify([{ filePath: file, messages }]));
process.exit(messages.some((m) => m.severity === 2) ? 1 : 0);
`;

/**
 * Creates a fixture repository and returns the path of a file inside it holding `content`.
 * `withConfig` and `withEslint` switch the root eslint config and the stand-in install.
 */
function makeLintFixture(root, name, content, withConfig, withEslint) {
  const repo = path.join(root, name);
  const packageDir = path.join(repo, 'packages', 'my pkg & co', 'src');
  fs.mkdirSync(path.join(repo, '.git'), { recursive: true });
  fs.mkdirSync(packageDir, { recursive: true });
  fs.writeFileSync(path.join(repo, 'packages', 'my pkg & co', 'package.json'), '{"name":"nested"}');
  if (withConfig) {
    fs.writeFileSync(path.join(repo, 'eslint.config.mjs'), 'export default [];\n');
  }
  if (withEslint) {
    const eslintDir = path.join(repo, 'node_modules', 'eslint');
    fs.mkdirSync(path.join(eslintDir, 'bin'), { recursive: true });
    fs.writeFileSync(path.join(eslintDir, 'package.json'), '{"name":"eslint","bin":{"eslint":"bin/eslint.js"}}');
    fs.writeFileSync(path.join(eslintDir, 'bin', 'eslint.js'), FAKE_ESLINT_BIN);
  }
  const filePath = path.join(packageDir, 'a & b.js');
  fs.writeFileSync(filePath, content);
  return filePath;
}

const LINT_FIXTURE_CASES = [
  ['clean', 'const a = 1;\n', true, true, 'file', ALLOW, 'clean file, root config, path with space and &'],
  ['error', '// LINT_ERROR_MARKER\n', true, true, 'file', BLOCK, 'lint error found through the root config'],
  ['warning', '// LINT_WARNING_MARKER\n', true, true, 'file', BLOCK, 'a warning is reported (zero-warning rule)'],
  ['ignored', '// LINT_IGNORED_MARKER\n', true, true, 'file', ALLOW, 'the file-ignored notice is not a warning'],
  ['multi', '// LINT_ERROR_MARKER\n', true, true, 'edits', BLOCK, 'edits[].file_path is linted'],
  ['no-eslint', '// LINT_ERROR_MARKER\n', true, false, 'file', ALLOW, 'no eslint installed skips the lint'],
  ['no-config', '// LINT_ERROR_MARKER\n', false, true, 'file', ALLOW, 'no eslint config skips the lint']
];

let lintFixtureCases = 0;
const lintDir = fs.mkdtempSync(path.join(RUN_DIR, 'lint-'));
try {
  for (const [name, content, withConfig, withEslint, shape, expected, label] of LINT_FIXTURE_CASES) {
    const filePath = makeLintFixture(lintDir, name, content, withConfig, withEslint);
    const toolInput = shape === 'file' ? { file_path: filePath } : { edits: [{ file_path: filePath }] };
    const payload = JSON.stringify({ tool_name: 'Write', tool_input: toolInput });
    report(LINT_CHANGED_FILE, `fixture :: ${label}`, expected, runHook(LINT_CHANGED_FILE, payload));
    lintFixtureCases += 1;
  }
} finally {
  fs.rmSync(lintDir, { recursive: true, force: true });
}

/**
 * lint-changed-file reports only the lines an edit touched. These fixtures are real git
 * repositories, so a Write is compared with HEAD; an Edit locates its `new_string`. The
 * committed file carries a warning on line 1 that every clean edit must leave unreported.
 */
const COMMITTED_TEXT = '// LINT_WARNING_MARKER\nconst a = 1;\n';

/**
 * Runs git in `cwd` and throws on failure, so a broken fixture never reads as a hook result.
 */
function git(cwd, args) {
  const result = spawnSync('git', ['-c', 'user.email=test@example.com', '-c', 'user.name=test', '-c', 'commit.gpgsign=false', ...args], { cwd, encoding: 'utf8' });
  if (result.error || result.status !== 0) {
    throw new Error(`git ${args.join(' ')} failed: ${result.error || result.stderr}`);
  }
}

/**
 * Creates a git repository with the stand-in eslint and a committed `src/a.js`, then writes
 * `afterText` over it (the edit already happened, as in PostToolUse). Returns the file path.
 */
function makeGitLintFixture(root, name, afterText, commit) {
  const repo = path.join(root, name);
  fs.mkdirSync(repo, { recursive: true });
  git(repo, ['init', '-q']);
  const eslintDir = path.join(repo, 'node_modules', 'eslint');
  fs.mkdirSync(path.join(eslintDir, 'bin'), { recursive: true });
  fs.writeFileSync(path.join(eslintDir, 'package.json'), '{"name":"eslint","bin":{"eslint":"bin/eslint.js"}}');
  fs.writeFileSync(path.join(eslintDir, 'bin', 'eslint.js'), FAKE_ESLINT_BIN);
  fs.writeFileSync(path.join(repo, 'eslint.config.mjs'), 'export default [];\n');
  fs.mkdirSync(path.join(repo, 'src'), { recursive: true });
  const filePath = path.join(repo, 'src', 'a.js');
  if (commit) {
    fs.writeFileSync(filePath, COMMITTED_TEXT);
    git(repo, ['add', 'src/a.js', 'eslint.config.mjs']);
    git(repo, ['commit', '-q', '-m', 'init']);
  }
  fs.writeFileSync(filePath, afterText);
  return filePath;
}

const GIT_LINT_CASES = [
  ['write-clean', `${COMMITTED_TEXT}const b = 2;\n`, true, (file) => ({ file_path: file, content: `${COMMITTED_TEXT}const b = 2;\n` }), ALLOW, 'Write adding a clean line leaves the old warning unreported'],
  ['write-error', `${COMMITTED_TEXT}// LINT_ERROR_MARKER\n`, true, (file) => ({ file_path: file, content: `${COMMITTED_TEXT}// LINT_ERROR_MARKER\n` }), BLOCK, 'Write adding an error line is reported'],
  ['write-unchanged', COMMITTED_TEXT, true, (file) => ({ file_path: file, content: COMMITTED_TEXT }), ALLOW, 'Write changing nothing reports nothing'],
  ['edit-clean', '// LINT_WARNING_MARKER\nconst a = 2;\n', true, (file) => ({ file_path: file, old_string: 'const a = 1;', new_string: 'const a = 2;' }), ALLOW, 'Edit on a clean line leaves the old warning unreported'],
  ['edit-warning', '// LINT_WARNING_MARKER\nconst a = 1; // LINT_WARNING_MARKER\n', true, (file) => ({ file_path: file, old_string: 'const a = 1;', new_string: 'const a = 1; // LINT_WARNING_MARKER' }), BLOCK, 'Edit adding a warning is reported'],
  ['multiedit-error', '// LINT_WARNING_MARKER\nconst a = 1;\n// LINT_ERROR_MARKER\n', true, (file) => ({ file_path: file, edits: [{ old_string: 'x', new_string: '// LINT_ERROR_MARKER' }] }), BLOCK, 'MultiEdit entry adding an error is reported'],
  ['edit-fatal', '// LINT_WARNING_MARKER\nconst a = 1;\n// LINT_FATAL_MARKER\n', true, (file) => ({ file_path: file, old_string: 'const a = 1;', new_string: 'const a = 1;' }), BLOCK, 'a parse error is reported wherever it sits'],
  ['untracked', COMMITTED_TEXT, false, (file) => ({ file_path: file, content: COMMITTED_TEXT }), BLOCK, 'an untracked file counts every line']
];

let gitLintCases = 0;
const gitProbe = spawnSync('git', ['--version'], { encoding: 'utf8' });
if (gitProbe.error || gitProbe.status !== 0) {
  reportSkip(`${LINT_CHANGED_FILE} :: touched-line cases (git is not installed here)`);
} else {
  const gitLintDir = fs.mkdtempSync(path.join(RUN_DIR, 'lint-git-'));
  try {
    for (const [name, afterText, commit, buildInput, expected, label] of GIT_LINT_CASES) {
      const filePath = makeGitLintFixture(gitLintDir, name, afterText, commit);
      const payload = JSON.stringify({ tool_name: 'Edit', tool_input: buildInput(filePath) });
      report(LINT_CHANGED_FILE, `touched lines :: ${label}`, expected, runHook(LINT_CHANGED_FILE, payload));
      gitLintCases += 1;
    }
  } finally {
    fs.rmSync(gitLintDir, { recursive: true, force: true });
  }
}

const total = CASES.length + EMPTY_STDIN_CASES.length + MALFORMED_STDIN_CASES.length
  + conditionalCases + renderedCases + lintFixtureCases + gitLintCases;
const passedCount = total - failures;
console.log(`test-hooks: ${passedCount} passed, ${failures} failed, ${skips} skipped`);
process.exit(failures === 0 ? 0 : 1);
