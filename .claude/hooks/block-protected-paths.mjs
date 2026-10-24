/**
 * PreToolUse hook for Edit / Write / MultiEdit.
 *
 * Blocks edits to files that must never be changed by an agent (environment files,
 * secrets directories, dependency lockfiles, production config, the leak-gate configs) and blocks content that
 * must never be saved into a test file (focused tests, unexplained or oversized fixed
 * waits in the e2e suite).
 *
 * Claude Code delivers the tool payload as JSON on stdin. It does NOT set
 * CLAUDE_TOOL_INPUT_FILE_PATH or any similar per-tool environment variable.
 *
 * Two values are filled by the generator through plain text replacement. Each lives on
 * its own `// @NAME: value` comment line, which the hook reads from its own source at
 * runtime, so no rendered character (quote, backtick, `${`, parenthesis) can break the
 * JavaScript. Every state (unrendered, filled, empty) is valid, and a value still holding
 * its placeholder reads as empty:
 * - E2E_DIR: repo-relative, forward-slashed directory of the e2e suite. Empty when the
 *   repo has no e2e suite, which turns the e2e-scoped rules off.
 * - EXTRA_PROTECTED_PATTERNS: one extra regex source for repo-specific protected paths,
 *   matched case-insensitively against the forward-slashed path. Alternate several
 *   patterns with `|`. Empty by default. A source that does not compile blocks every
 *   edit with a message naming it, so a bad render surfaces at the first write.
 *
 * Content rules see only the text the edit carries (`content`, `new_string`), never the
 * file on disk: a fixed wait's explaining comment has to travel in the same edit.
 *
 * Exit codes: 0 permits the call, 2 blocks it and surfaces stderr to the model. Any
 * internal error also exits 2: a guardrail that crashed has not cleared the call.
 */

import fs from 'node:fs';
import { fileURLToPath } from 'node:url';

// @E2E_DIR: 
// @EXTRA_PROTECTED_PATTERNS: 

// Case-insensitive: NTFS and APFS resolve `.ENV` and `PACKAGE-LOCK.JSON` to the same file.
// Env-file patterns do not apply to an env template: a basename carrying an `.example`
// segment (`.example.env`, `.prod.example.env`, legacy `.env.example`).
const ENV_FILE_PATTERNS = [/(^|[\\/])\.env($|\.)/i, /\.deploy[\\/].+\.env$/i];
const ENV_TEMPLATE_PATTERN = /(^|\/)[^/]*\.example(\.[^/]*)?$/i;

const BASE_PROTECTED_PATTERNS = [
  ...ENV_FILE_PATTERNS,
  /(^|[\\/])secrets[\\/]/i,
  /package-lock\.json$/i,
  /pnpm-lock\.yaml$/i,
  /yarn\.lock$/i,
  /configs\.production\.json$/i,
  // The leak-gate configs: an agent that could edit them could switch its own gate off.
  /(^|[\\/])lefthook\.ya?ml$/i,
  /(^|[\\/])\.gitleaks\.toml$/i
];

const MAX_FIXED_WAIT_MS = 120000;

const TEST_FILE_PATTERN = /\.(spec|test)\.[cm]?[jt]sx?$/;
const CODE_FILE_PATTERN = /\.[cm]?[jt]sx?$/;

// The lookbehinds keep method calls such as `layout.fit(box)` and `query.it.only` out.
// Chained modifiers are covered: `test.only.each`, `test.concurrent.only`, `context.only`.
const FOCUSED_TEST_PATTERN = /(?<![\w$.])(?:test|describe|it|context|suite)(?:\.\w+)*\.only\b|(?<![\w$.])(?:fdescribe|fit)\s*\(/;
// A member chain split over lines (`it\n  .only(...)`) is joined across this many lines.
const MAX_CHAIN_LINES = 5;
const WAIT_FOR_TIMEOUT_PATTERN = /\bwaitForTimeout\s*\(([^)]*)/;
const PROMISE_START_PATTERN = /\bnew\s+Promise\b/;
const SLEEP_PROMISE_PATTERN = /^new\s+Promise\s*\(\s*(?:\([^)]*\)|[\w$]+)\s*=>\s*\{?\s*setTimeout\b/;
const SLEEP_PROMISE_DELAY_PATTERN = /^[^;]*?setTimeout\s*\([^;]*?,\s*([\d_]+)\s*\)/;
const SLEEP_PROMISE_WINDOW = 300;
const BLOCK_COMMENT_START_PATTERN = /^(\/\*|\*)/;
const INLINE_COMMENT_PATTERN = /(\/\/|\/\*)\s*\w/;
const PLACEHOLDER_PATTERN = /^\{\{[A-Z0-9_]+\}\}$/;

const FIXED_WAIT_ADVICE =
  'Prefer a web-first assertion (expect(locator).toBeVisible(), waitForURL, waitForResponse, expect.poll). ' +
  `A fixed wait of at most ${MAX_FIXED_WAIT_MS} ms is allowed only with a comment stating why, ` +
  'on the same line or the line immediately above, inside the same edit.';

/**
 * Reads the generator values from the `// @NAME: value` lines of this file's own source.
 * A missing line, or a value still holding its double-brace placeholder, reads as empty.
 *
 * @returns {{e2eDir: string, extraSource: string}} the rendered values.
 */
function readGeneratorValues() {
  const lines = fs.readFileSync(fileURLToPath(import.meta.url), 'utf8').split(/\r?\n/);
  const read = (name) => {
    const prefix = `// @${name}:`;
    const line = lines.find((candidate) => candidate.startsWith(prefix));
    if (line === undefined) {
      return '';
    }
    const value = line.slice(prefix.length).trim();
    return PLACEHOLDER_PATTERN.test(value) ? '' : value;
  };
  const values = { e2eDir: read('E2E_DIR'), extraSource: read('EXTRA_PROTECTED_PATTERNS') };
  return values;
}

/**
 * Builds the list of protected-path regexes: the base set plus the optional
 * repo-specific pattern. An empty value adds nothing; a source that does not compile
 * throws with a message naming it.
 *
 * @param {string} extraSource the rendered EXTRA_PROTECTED_PATTERNS value.
 * @returns {RegExp[]} the protected-path patterns.
 */
function buildProtectedPatterns(extraSource) {
  const patterns = [...BASE_PROTECTED_PATTERNS];
  if (extraSource === '') {
    return patterns;
  }
  try {
    patterns.push(new RegExp(extraSource, 'i'));
  } catch (error) {
    throw new Error(
      `EXTRA_PROTECTED_PATTERNS is not a valid regex (${extraSource}): ${error.message}. ` +
        'Fix the `// @EXTRA_PROTECTED_PATTERNS:` line of this hook.'
    );
  }
  return patterns;
}

/**
 * Escapes every regex metacharacter so a directory name matches literally.
 */
function escapeRegex(text) {
  const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return escaped;
}

/**
 * Returns the regex that matches code files inside the e2e suite, or null when the
 * repo has no e2e suite (E2E_DIR rendered empty).
 *
 * @param {string} e2eDir the rendered E2E_DIR value.
 * @returns {RegExp|null} the e2e scope.
 */
function buildE2eScope(e2eDir) {
  const dir = e2eDir.trim().replace(/\\/g, '/').replace(/^\.\//, '').replace(/\/+$/, '');
  if (dir === '') {
    return null;
  }
  const scope = new RegExp(`(^|/)${escapeRegex(dir)}/`);
  return scope;
}

/**
 * Parses a numeric literal such as `3000` or `120_000`. Returns null for anything
 * else (a variable, an expression), whose value the hook cannot know.
 */
function parseDelayLiteral(argument) {
  if (typeof argument !== 'string') {
    return null;
  }
  const trimmed = argument.trim();
  if (!/^\d[\d_]*$/.test(trimmed)) {
    return null;
  }
  const value = Number(trimmed.replace(/_/g, ''));
  return value;
}

/**
 * True when the line itself is a comment line, so a match on it is documentation.
 */
function isCommentLine(line) {
  const trimmed = line.trim();
  if (trimmed.startsWith('//')) {
    return true;
  }
  if (!BLOCK_COMMENT_START_PATTERN.test(trimmed)) {
    return false;
  }
  // A block comment that closes mid-line has code after it: `/* x */ test.only(...)`.
  const close = trimmed.indexOf('*/', 1);
  return close === -1 || close === trimmed.length - 2;
}

/**
 * True when line `index` carries a comment with text, or the line immediately above is
 * a comment line with text.
 */
function hasExplainingComment(lines, index) {
  if (INLINE_COMMENT_PATTERN.test(lines[index])) {
    return true;
  }
  if (index === 0) {
    return false;
  }
  const previous = lines[index - 1].trim();
  // A bare `*/` closes a block comment, which explains by construction.
  return isCommentLine(previous) && (/\w/.test(previous) || previous === '*/');
}

/**
 * Returns line `index` joined with the following lines that continue a member chain
 * (their trimmed text starts with `.`), so `it\n  .only(` reads as `it.only(`.
 */
function joinChain(lines, index) {
  let joined = lines[index].trimEnd();
  for (let next = index + 1; next < lines.length && next <= index + MAX_CHAIN_LINES; next += 1) {
    const trimmed = lines[next].trim();
    if (!trimmed.startsWith('.')) {
      break;
    }
    joined += trimmed;
  }
  return joined;
}

/**
 * Returns the problem with a fixed wait on line `index`, or null when it is allowed.
 * `delayText` is the raw delay argument, used only when it is a numeric literal.
 */
function fixedWaitProblem(kind, delayText, lines, index) {
  const delay = parseDelayLiteral(delayText);
  if (delay !== null && delay > MAX_FIXED_WAIT_MS) {
    return `${kind} of ${delay} ms exceeds the ${MAX_FIXED_WAIT_MS} ms cap. ${FIXED_WAIT_ADVICE}`;
  }
  if (!hasExplainingComment(lines, index)) {
    return `${kind} without an explaining comment. ${FIXED_WAIT_ADVICE}`;
  }
  return null;
}

/**
 * Checks one line of incoming content against the fixed-wait rules. `text` is the whole
 * normalized content and `lineStart` the offset of line `index` in it, so a sleep
 * promise that spans lines can still expose its delay literal.
 */
function fixedWaitProblems(lines, index, text, lineStart) {
  const problems = [];
  const line = lines[index];
  const waitMatch = WAIT_FOR_TIMEOUT_PATTERN.exec(line);
  if (waitMatch) {
    const problem = fixedWaitProblem('waitForTimeout', waitMatch[1], lines, index);
    if (problem) {
      problems.push(problem);
    }
  }
  const promiseColumn = line.search(PROMISE_START_PATTERN);
  if (promiseColumn >= 0) {
    const windowStart = lineStart + promiseColumn;
    const window = text.slice(windowStart, windowStart + SLEEP_PROMISE_WINDOW);
    if (SLEEP_PROMISE_PATTERN.test(window)) {
      const delayMatch = SLEEP_PROMISE_DELAY_PATTERN.exec(window);
      const problem = fixedWaitProblem('sleep promise', delayMatch ? delayMatch[1] : null, lines, index);
      if (problem) {
        problems.push(problem);
      }
    }
  }
  return problems;
}

/**
 * Returns every content violation the incoming text would introduce into `path`
 * (forward-slashed), each as a human-readable message with its line number.
 */
function contentViolations(path, content, e2eScope) {
  if (typeof content !== 'string' || content === '') {
    return [];
  }
  const checkFocused = TEST_FILE_PATTERN.test(path);
  const checkWaits = e2eScope !== null && e2eScope.test(path) && CODE_FILE_PATTERN.test(path);
  if (!checkFocused && !checkWaits) {
    return [];
  }
  const text = content.replace(/\r\n/g, '\n');
  const lines = text.split('\n');
  const violations = [];
  let lineStart = 0;
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const currentStart = lineStart;
    lineStart += line.length + 1;
    if (isCommentLine(line)) {
      continue;
    }
    if (checkFocused && FOCUSED_TEST_PATTERN.test(joinChain(lines, index))) {
      violations.push(`line ${index + 1}: focused test (.only / fdescribe / fit) disables the rest of the suite and must never be saved.`);
    }
    if (checkWaits) {
      for (const problem of fixedWaitProblems(lines, index, text, currentStart)) {
        violations.push(`line ${index + 1}: ${problem}`);
      }
    }
  }
  return violations;
}

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
 * Returns the content an edit would write: Write carries `content`, Edit and each
 * MultiEdit entry carry `new_string`. Empty string when neither is present.
 */
function incomingContent(input) {
  if (typeof input.content === 'string') {
    return input.content;
  }
  if (typeof input.new_string === 'string') {
    return input.new_string;
  }
  return '';
}

/**
 * Collects every target the payload references as `{ path, content }`, pairing each
 * path with the content headed for it. MultiEdit and some future tools may carry more
 * than one target, so a single `file_path` lookup is not enough.
 */
function collectTargets(toolInput) {
  if (!toolInput || typeof toolInput !== 'object') {
    return [];
  }
  const targets = [];
  if (typeof toolInput.file_path === 'string') {
    targets.push({ path: toolInput.file_path, content: incomingContent(toolInput) });
  }
  if (typeof toolInput.notebook_path === 'string') {
    targets.push({ path: toolInput.notebook_path, content: '' });
  }
  if (Array.isArray(toolInput.edits)) {
    for (const edit of toolInput.edits) {
      if (edit && typeof edit.file_path === 'string') {
        targets.push({ path: edit.file_path, content: incomingContent(edit) });
      }
      // A MultiEdit entry without its own file_path edits the top-level file.
      if (edit && typeof edit.file_path !== 'string' && typeof toolInput.file_path === 'string') {
        targets.push({ path: toolInput.file_path, content: incomingContent(edit) });
      }
    }
  }
  return targets;
}

/**
 * Returns every violation across all targets, so one blocked call reports all of its
 * problems at once instead of one per retry.
 */
function collectViolations(targets) {
  const { e2eDir, extraSource } = readGeneratorValues();
  let protectedPatterns;
  try {
    protectedPatterns = buildProtectedPatterns(extraSource);
  } catch (error) {
    // A broken protected-path set cannot clear any edit, so every edit is refused.
    return [`hook configuration: ${error.message}`];
  }
  const e2eScope = buildE2eScope(e2eDir);
  const violations = [];
  for (const target of targets) {
    const normalized = target.path.replace(/\\/g, '/');
    const isTemplate = ENV_TEMPLATE_PATTERN.test(normalized);
    const offender = protectedPatterns.find(
      (pattern) => pattern.test(normalized) && !(isTemplate && ENV_FILE_PATTERNS.includes(pattern))
    );
    if (offender) {
      violations.push(
        `edits to ${target.path} are not allowed (matched ${offender}). ` +
          'Environment files, secrets, lockfiles and production config are edited by the developer. ' +
          'Env templates (`.example.env`) are editable. ' +
          'If the edit is really needed, hand it to the developer as root AGENTS.md -> "Blocked actions" describes.'
      );
      continue;
    }
    for (const message of contentViolations(normalized, target.content, e2eScope)) {
      violations.push(`${target.path} ${message}`);
    }
  }
  return violations;
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
    console.error('block-protected-paths: could not parse the hook payload as JSON; refusing to guess.');
    process.exit(2);
  }
  if (payload === null || typeof payload !== 'object' || Array.isArray(payload)) {
    console.error('block-protected-paths: the hook payload is not a JSON object; refusing to guess.');
    process.exit(2);
  }

  const violations = collectViolations(collectTargets(payload.tool_input));
  if (violations.length > 0) {
    console.error(`Blocked:\n- ${violations.join('\n- ')}`);
    process.exit(2);
  }

  process.exit(0);
}

main().catch((error) => {
  console.error(`block-protected-paths: internal error, blocking the call: ${error && error.message ? error.message : error}`);
  process.exit(2);
});
