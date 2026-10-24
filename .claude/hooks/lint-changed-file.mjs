/**
 * PostToolUse hook for Edit / Write / MultiEdit.
 *
 * Lints only the file that was just written: never a repo-wide sweep, never a `--fix` over
 * files the session did not touch.
 *
 * JavaScript and TypeScript go through the project's own eslint with `--no-fix <file>`,
 * never the package's `lint` script, which often carries `--fix` over the whole tree.
 * Python goes through `ruff check`, which does not write. Warnings count as well as errors;
 * the "file ignored" notice for a path the config ignores does not.
 *
 * Only problems on the lines the edit touched are reported: where each `new_string` now
 * sits, or, for a Write, the lines that differ from HEAD. An untracked file counts every
 * line. Problems elsewhere are summarized as pre-existing and never fail the hook.
 *
 * No shell is involved: eslint runs as `node <eslint bin> ...` and ruff by name, so a path
 * with spaces or `&` is one argument and never a second command. The eslint config is
 * searched from the file's directory up to the repository root (the directory holding
 * `.git`), so a monorepo whose only config sits at the root is still linted. An absent
 * config, an absent eslint install or an absent ruff binary skips the lint.
 *
 * Claude Code delivers the tool payload as JSON on stdin. It does NOT set
 * CLAUDE_TOOL_INPUT_FILE_PATH.
 *
 * Exit codes: 0 permits the result, 2 reports the lint failure back to the model. The
 * hook is advisory and runs after the write happened, so its own failures (unparseable
 * payload, internal error) exit 0 with a message on stderr.
 */

import path from 'node:path';
import fs from 'node:fs';
import { spawnSync } from 'node:child_process';

const JS_EXTENSIONS = ['.js', '.ts', '.jsx', '.tsx', '.mjs', '.cjs', '.mts', '.cts'];

const ESLINT_CONFIG_FILES = [
  'eslint.config.mjs',
  'eslint.config.js',
  'eslint.config.cjs',
  'eslint.config.ts',
  'eslint.config.mts',
  'eslint.config.cts',
  '.eslintrc.js',
  '.eslintrc.cjs',
  '.eslintrc.json',
  '.eslintrc.yml',
  '.eslintrc.yaml'
];

/**
 * Reads the entire stdin stream. Returns an empty string when stdin is closed,
 * which keeps the hook fail-open on a harness that supplies no payload.
 */
async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  const joined = Buffer.concat(chunks).toString('utf8');
  return joined;
}

/**
 * Returns the directories from `startDir` up to the repository root (the first ancestor
 * holding `.git`), nearest first. Without a repository root the walk stops at the nearest
 * directory holding `package.json`, so a file outside any project is never linted against
 * an unrelated config higher up the disk.
 *
 * @param {string} startDir an absolute directory.
 * @returns {string[]} the directories to search, nearest first; empty when unbounded.
 */
function searchDirs(startDir) {
  const dirs = [];
  let dir = startDir;
  let packageBoundary = -1;
  for (;;) {
    dirs.push(dir);
    if (fs.existsSync(path.join(dir, '.git'))) {
      return dirs;
    }
    if (packageBoundary === -1 && fs.existsSync(path.join(dir, 'package.json'))) {
      packageBoundary = dirs.length;
    }
    const parent = path.dirname(dir);
    if (parent === dir) {
      return packageBoundary === -1 ? [] : dirs.slice(0, packageBoundary);
    }
    dir = parent;
  }
}

/**
 * True when `dir` carries an eslint config file or a package.json with `eslintConfig`.
 */
function hasEslintConfig(dir) {
  if (ESLINT_CONFIG_FILES.some((name) => fs.existsSync(path.join(dir, name)))) {
    return true;
  }
  const manifestPath = path.join(dir, 'package.json');
  if (!fs.existsSync(manifestPath)) {
    return false;
  }
  try {
    const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
    return Boolean(manifest && manifest.eslintConfig);
  } catch {
    return false;
  }
}

/**
 * Returns the absolute path of the eslint CLI script installed for `dirs` (nearest
 * `node_modules/eslint` first), or null when eslint is not installed there.
 *
 * @param {string[]} dirs the directories to search, nearest first.
 * @returns {string|null} the eslint bin script.
 */
function findEslintBin(dirs) {
  for (const dir of dirs) {
    const packageDir = path.join(dir, 'node_modules', 'eslint');
    const manifestPath = path.join(packageDir, 'package.json');
    if (!fs.existsSync(manifestPath)) {
      continue;
    }
    try {
      const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
      const bin = typeof manifest.bin === 'string' ? manifest.bin : manifest.bin && manifest.bin.eslint;
      if (typeof bin === 'string') {
        const binPath = path.join(packageDir, bin);
        return fs.existsSync(binPath) ? binPath : null;
      }
    } catch {
      continue;
    }
  }
  return null;
}

/**
 * Runs eslint against a single file, from the directory that owns the nearest eslint
 * config so that local plugins and presets resolve. `--no-fix` is explicit: the hook reports
 * and never rewrites.
 *
 * @param {string} filePath an absolute file path.
 * @returns {object|null} the spawn result, or null when the lint is skipped.
 */
function lintJavaScript(filePath) {
  const dirs = searchDirs(path.dirname(filePath));
  const configIndex = dirs.findIndex(hasEslintConfig);
  if (configIndex === -1) {
    return null;
  }
  const eslintBin = findEslintBin(dirs);
  if (eslintBin === null) {
    return null;
  }
  const result = spawnSync(process.execPath, [eslintBin, '--no-fix', '--format', 'json', filePath], {
    cwd: dirs[configIndex],
    encoding: 'utf8'
  });
  if (result.error) {
    return null;
  }
  return eslintOutcome(result);
}

/**
 * Turns an eslint `--format json` run into `{ problems }`, one `{ line, fatal, text }` per
 * error or warning, except the "file ignored" notice eslint emits for a path its config
 * ignores. Output that is not the JSON report (a crash, a config error) is returned as
 * `{ raw }` with eslint's own status.
 */
function eslintOutcome(result) {
  let files;
  try {
    files = JSON.parse(result.stdout);
  } catch {
    return { raw: result };
  }
  if (!Array.isArray(files)) {
    return { raw: result };
  }
  const problems = [];
  for (const file of files) {
    const messages = Array.isArray(file.messages) ? file.messages : [];
    for (const message of messages) {
      if (typeof message.message === 'string' && message.message.startsWith('File ignored')) {
        continue;
      }
      const severity = message.severity === 2 ? 'error' : 'warning';
      problems.push({
        line: message.line || 0,
        fatal: message.fatal === true,
        text: `  ${message.line || 0}:${message.column || 0}  ${severity}  ${message.message}  ${message.ruleId || ''}`
      });
    }
  }
  return { problems };
}

/**
 * Runs ruff against a single Python file and returns `{ problems }` like eslintOutcome,
 * or `{ raw }` when its output is not the JSON report. A missing ruff binary (ENOENT,
 * since no shell is involved) is "not configured here", never a lint failure.
 *
 * @param {string} filePath an absolute file path.
 * @returns {object|null} the outcome, or null when the lint is skipped.
 */
function lintPython(filePath) {
  const result = spawnSync('ruff', ['check', '--output-format', 'json', filePath], { encoding: 'utf8' });
  if (result.error) {
    return null;
  }
  let findings;
  try {
    findings = JSON.parse(result.stdout);
  } catch {
    return { raw: result };
  }
  if (!Array.isArray(findings)) {
    return { raw: result };
  }
  const problems = findings.map((finding) => {
    const row = finding.location && finding.location.row ? finding.location.row : 0;
    const column = finding.location && finding.location.column ? finding.location.column : 0;
    return { line: row, fatal: !finding.code, text: `  ${row}:${column}  ${finding.code || 'error'}  ${finding.message}` };
  });
  return { problems };
}

/**
 * Returns the new-side line numbers of a `git diff -U0` output. A pure deletion marks the
 * lines on both sides of the cut.
 *
 * @param {string} diff the diff text.
 * @returns {Set<number>} the touched lines.
 */
function diffAddedLines(diff) {
  const lines = new Set();
  for (const match of diff.matchAll(/^@@ -\S+ \+(\d+)(?:,(\d+))? @@/gm)) {
    const start = Number(match[1]);
    const count = match[2] === undefined ? 1 : Number(match[2]);
    if (count === 0) {
      lines.add(start);
      lines.add(start + 1);
    }
    for (let line = start; line < start + count; line += 1) {
      lines.add(line);
    }
  }
  return lines;
}

/**
 * Returns the lines of `filePath` that differ from HEAD, or null when every line counts as
 * new: the file is untracked, the repository has no HEAD, or git is unavailable.
 *
 * @param {string} filePath an absolute file path.
 * @returns {Set<number>|null} the touched lines.
 */
function gitChangedLines(filePath) {
  const options = { cwd: path.dirname(filePath), encoding: 'utf8' };
  const name = path.basename(filePath);
  const tracked = spawnSync('git', ['ls-files', '--error-unmatch', '--', name], options);
  if (tracked.error || tracked.status !== 0) {
    return null;
  }
  const diff = spawnSync('git', ['diff', '-U0', '--no-color', '--no-ext-diff', 'HEAD', '--', name], options);
  if (diff.error || diff.status !== 0) {
    return null;
  }
  const lines = diffAddedLines(diff.stdout);
  return lines;
}

/**
 * Returns the lines where the edit's `new_string` values now sit in the file, or an empty
 * set when none is found (a deletion, or a formatter moved the text).
 *
 * @param {string} text the file content after the edit.
 * @param {string[]} newStrings the non-empty `new_string` values aimed at this file.
 * @returns {Set<number>} the touched lines.
 */
function newStringLines(text, newStrings) {
  const lines = new Set();
  for (const newString of newStrings) {
    const spanned = newString.split('\n').length;
    let found = text.indexOf(newString);
    while (found !== -1) {
      const firstLine = text.slice(0, found).split('\n').length;
      for (let line = firstLine; line < firstLine + spanned; line += 1) {
        lines.add(line);
      }
      found = text.indexOf(newString, found + newString.length);
    }
  }
  return lines;
}

/**
 * Returns the lines of `filePath` this edit touched, or null when every line counts.
 * Edit and MultiEdit locate their `new_string`; Write, and an Edit whose text cannot be
 * located, compare the file with HEAD.
 *
 * @param {string} filePath the path as the payload named it.
 * @param {object} toolInput the payload's tool_input.
 * @returns {Set<number>|null} the touched lines.
 */
function touchedLines(filePath, toolInput) {
  const newStrings = [];
  if (toolInput.file_path === filePath && typeof toolInput.new_string === 'string') {
    newStrings.push(toolInput.new_string);
  }
  for (const edit of Array.isArray(toolInput.edits) ? toolInput.edits : []) {
    const target = edit && typeof edit.file_path === 'string' ? edit.file_path : toolInput.file_path;
    if (edit && target === filePath && typeof edit.new_string === 'string') {
      newStrings.push(edit.new_string);
    }
  }
  const absolute = path.resolve(filePath);
  const nonEmpty = newStrings.filter((value) => value !== '');
  if (nonEmpty.length > 0) {
    const text = fs.readFileSync(absolute, 'utf8').replace(/\r\n/g, '\n');
    const located = newStringLines(text, nonEmpty.map((value) => value.replace(/\r\n/g, '\n')));
    if (located.size > 0) {
      return located;
    }
  }
  const changed = gitChangedLines(absolute);
  return changed;
}

/**
 * Collects the distinct file paths a payload wrote: `file_path` and every
 * `edits[].file_path`.
 */
function collectPaths(toolInput) {
  if (!toolInput || typeof toolInput !== 'object') {
    return [];
  }
  const paths = [];
  if (typeof toolInput.file_path === 'string') {
    paths.push(toolInput.file_path);
  }
  if (Array.isArray(toolInput.edits)) {
    for (const edit of toolInput.edits) {
      if (edit && typeof edit.file_path === 'string') {
        paths.push(edit.file_path);
      }
    }
  }
  const unique = [...new Set(paths)];
  return unique;
}

/**
 * Lints one file and returns its failure report, or null when it is clean or skipped.
 * Only problems on the lines the edit touched are reported (a fatal parse error always
 * is); the rest are counted as pre-existing.
 *
 * @param {string} filePath the path as the payload named it.
 * @param {object} toolInput the payload's tool_input.
 * @returns {string|null} the report.
 */
function lintFile(filePath, toolInput) {
  if (!fs.existsSync(filePath)) {
    return null;
  }
  const absolute = path.resolve(filePath);
  const extension = path.extname(absolute).toLowerCase();
  let outcome = null;
  if (JS_EXTENSIONS.includes(extension)) {
    outcome = lintJavaScript(absolute);
  } else if (extension === '.py') {
    outcome = lintPython(absolute);
  }
  if (!outcome) {
    return null;
  }
  if (outcome.raw) {
    if (outcome.raw.status === 0) {
      return null;
    }
    const rawReport = `lint failed for ${filePath}:\n${outcome.raw.stdout || ''}${outcome.raw.stderr || ''}`;
    return rawReport;
  }
  if (outcome.problems.length === 0) {
    return null;
  }
  const touched = touchedLines(filePath, toolInput);
  const relevant = outcome.problems.filter(
    (problem) => touched === null || problem.fatal || problem.line === 0 || touched.has(problem.line)
  );
  if (relevant.length === 0) {
    return null;
  }
  const preExisting = outcome.problems.length - relevant.length;
  const tail = preExisting > 0 ? `${preExisting} pre-existing problem(s) elsewhere in the file: do not fix them.\n` : '';
  const report = `lint failed for ${filePath} on the lines this edit touched:\n${relevant.map((problem) => problem.text).join('\n')}\n${tail}`;
  return report;
}

async function main() {
  const raw = await readStdin();
  if (!raw.trim()) {
    process.exit(0);
  }

  let payload;
  try {
    payload = JSON.parse(raw);
  } catch {
    console.error('lint-changed-file: could not parse the hook payload as JSON; skipping the lint.');
    process.exit(0);
  }

  const toolInput = payload && typeof payload === 'object' ? payload.tool_input : undefined;
  const reports = collectPaths(toolInput)
    .map((filePath) => lintFile(filePath, toolInput))
    .filter((report) => report !== null);
  if (reports.length > 0) {
    process.stderr.write(reports.join('\n'));
    process.exit(2);
  }

  process.exit(0);
}

main().catch((error) => {
  console.error(`lint-changed-file: internal error, skipping the lint: ${error && error.message ? error.message : error}`);
  process.exit(0);
});
