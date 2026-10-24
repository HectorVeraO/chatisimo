#!/usr/bin/env node
// Builds .me/review-pr/working.input.json from the LOCAL UNCOMMITTED diff, in the
// same shape fetch-pr.mjs produces for a pull request. It first removes the previous
// working-tree packet and reports. It needs no credentials; lib/gh.mjs rejects the
// `working` id, so this packet can never reach post-comments.mjs.
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { clearProfileReports } from './lib/artifacts.mjs';
import { BASE_PROFILES } from './lib/extra-profile.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

const INPUT_DIR = '.me/review-pr';
const REVIEW_ID = 'working';

// Lines of surrounding context per hunk; override with --context <n>.
const DEFAULT_CONTEXT_LINES = 25;

// `git diff HEAD` covers staged and unstaged changes; bare `git diff` misses staged ones.
// `core.quotepath=false` keeps non-ASCII paths unescaped.
const QUOTEPATH_OFF = ['-c', 'core.quotepath=false'];

function diffArgs(contextLines) {
  return [...QUOTEPATH_OFF, 'diff', `-U${contextLines}`, 'HEAD'];
}

// Untracked, non-ignored files, which `git diff HEAD` never shows.
function untrackedFiles() {
  const raw = git([...QUOTEPATH_OFF, 'ls-files', '--others', '--exclude-standard', '-z']);
  const files = raw.split('\0').filter((f) => f !== '');
  return files;
}

// One file as a new-file diff against /dev/null. `--no-index` exits 1 when the files
// differ, which for a new file is always.
function untrackedDiff(path, contextLines) {
  const args = [...QUOTEPATH_OFF, 'diff', '--no-index', `-U${contextLines}`, '--', '/dev/null', path];
  const diff = git(args, [0, 1]);
  return diff;
}

function gitBin() {
  if (process.platform === 'win32') {
    return 'git.exe';
  }
  return 'git';
}

function git(args, okStatuses = [0]) {
  const result = spawnSync(gitBin(), args, { encoding: 'utf-8', maxBuffer: 64 * 1024 * 1024 });
  if (result.error) {
    throw new Error(`git ${args.join(' ')} failed to start: ${result.error.message}`);
  }
  if (!okStatuses.includes(result.status)) {
    const stderr = (result.stderr ?? '').trim();
    throw new Error(`git ${args.join(' ')} exited ${result.status}: ${stderr}`);
  }
  return result.stdout ?? '';
}

function currentBranch() {
  const name = git(['rev-parse', '--abbrev-ref', 'HEAD']).trim();
  if (name === '' || name === 'HEAD') {
    return '(detached HEAD)';
  }
  return name;
}

function readFlag(name, fallback) {
  const index = process.argv.indexOf(name);
  if (index === -1) {
    return fallback;
  }
  const value = process.argv[index + 1];
  if (value === undefined || value.startsWith('--')) {
    throw new Error(`${name} requires a value`);
  }
  return value;
}

function clearPreviousRun() {
  rmSync(join(INPUT_DIR, `${REVIEW_ID}.input.json`), { force: true });
  // A fresh packet makes every earlier working-tree report stale, whichever profile
  // produced it.
  for (const profile of BASE_PROFILES) {
    clearProfileReports(INPUT_DIR, REVIEW_ID, profile);
  }
}

function main() {
  rejectUnknownFlags(
    'build-working-input.mjs',
    'Usage: build-working-input.mjs [--destination <branch>] [--context <n>]',
    { valueFlags: ['--destination', '--context'] },
  );
  // The branch the work will merge into, never the branch it sits on: digest.mjs tests
  // it against the lenient pattern.
  const destination = readFlag('--destination', 'HEAD');
  const source = currentBranch();

  const contextRaw = readFlag('--context', String(DEFAULT_CONTEXT_LINES));
  const contextLines = Number.parseInt(contextRaw, 10);
  if (!Number.isInteger(contextLines) || contextLines < 0) {
    throw new Error(`--context must be a non-negative integer, got ${JSON.stringify(contextRaw)}`);
  }

  clearPreviousRun();

  const args = diffArgs(contextLines);
  const trackedDiff = git(args);
  const untracked = untrackedFiles();
  const untrackedDiffs = untracked.map((path) => untrackedDiff(path, contextLines));
  const diff = [trackedDiff, ...untrackedDiffs]
    .filter((part) => part !== '')
    .map((part) => (part.endsWith('\n') ? part : `${part}\n`))
    .join('');
  const command = untracked.length === 0
    ? `git ${args.join(' ')}`
    : `git ${args.join(' ')} + ${untracked.length} untracked file(s)`;

  if (diff.trim() === '') {
    process.stderr.write(
      `build-working-input.mjs: \`${command}\` returned an empty diff. `
      + 'There is nothing to review. This is a STOP, not a reason to fall back to a commit range.\n',
    );
    process.stdout.write(JSON.stringify({
      reviewId: REVIEW_ID,
      command,
      empty: true,
      filesCount: 0,
    }, null, 2) + '\n');
    process.exitCode = 3;
    return;
  }

  const nameOnly = git([...QUOTEPATH_OFF, 'diff', 'HEAD', '--name-only']);
  const tracked = nameOnly.split('\n').map((l) => l.trim()).filter((l) => l !== '');
  const files = [...tracked, ...untracked];

  // digest.mjs requires these three meta fields; the title names the diff command.
  const output = {
    reviewId: REVIEW_ID,
    target: 'working-tree',
    meta: {
      title: `Working tree - ${command} (staged + unstaged + untracked)`,
      source: { branch: source, sha: null },
      destination: { branch: destination, sha: null },
    },
    // The uncommitted changes sit on top of HEAD, so reviewers read context from disk.
    head: {
      sha: git(['rev-parse', 'HEAD']).trim(),
      branch: source,
      readFrom: 'working-tree',
      headAvailableLocally: true,
    },
    contextLines,
    diff,
  };

  mkdirSync(INPUT_DIR, { recursive: true });
  const outPath = join(INPUT_DIR, `${REVIEW_ID}.input.json`);
  writeFileSync(outPath, JSON.stringify(output, null, 2));

  process.stdout.write(JSON.stringify({
    reviewId: REVIEW_ID,
    command,
    empty: false,
    filesCount: files.length,
    untrackedCount: untracked.length,
    diffChars: diff.length,
    contextLines,
    source,
    destination,
    inputPath: outPath,
  }, null, 2) + '\n');
}

try {
  main();
} catch (err) {
  process.stderr.write(`build-working-input.mjs failed: ${err.message}\n`);
  process.exitCode = 1;
}
