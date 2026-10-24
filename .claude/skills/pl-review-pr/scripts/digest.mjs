#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { resolveExtraProfile } from './lib/extra-profile.mjs';
import { parseDiff as parseDiffFiles, parseReviewId } from './lib/diff.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

const INPUT_DIR = '.me/review-pr';

// Rendered by the agent-infra generator (a JS regex source, no delimiters). Empty
// means NO lenient mode. The pattern is anchored at the start inside a
// non-capturing group: 'feature/poc' matches 'feature/poc-staging' but not
// 'bugfix/feature/poc-old'; a trailing '$' forces an exact match. A raw template
// keeps backslashes and quotes; the generator refuses a backtick or `${`.
const LENIENT_DEST_PATTERN = String.raw``;

// REVIEW_ALL_PRS_LENIENT_PATTERN applies only to the unrendered template (the generic
// engine run from ~/.claude). In a generated repo the rendered constant always wins,
// an empty one included.
function lenientDestMatcher() {
  const substituted = (typeof LENIENT_DEST_PATTERN === 'string') ? LENIENT_DEST_PATTERN.trim() : '';
  const unrendered = substituted.startsWith('{{');
  const raw = unrendered
    ? (process.env.REVIEW_ALL_PRS_LENIENT_PATTERN || '').trim()
    : substituted;
  if (raw === '') {
    return null;
  }
  try {
    const anchored = new RegExp(`^(?:${raw})`);
    return anchored;
  } catch (err) {
    process.stderr.write(`digest.mjs: invalid LENIENT_DEST_PATTERN '${raw}': ${err.message}; treating as no-lenient-mode.\n`);
    return null;
  }
}

// Accepts the literal 'working' alongside a PR number or URL, so a working-tree
// review runs through this same digest step. lib/gh.mjs's parsePrId does NOT accept
// it: its throw stops an uncommitted review from being published to a pull request.
function parsePrId(arg) {
  const id = parseReviewId(arg);
  if (id === null || id === 'working') {
    return id;
  }
  return Number(id);
}

// The shared lib/diff parser, so the digest and the verifier agree on every path
// (quoted and non-ASCII ones included). Only path and hunks leave this script: the
// per-file body is verifier input and must not be rendered into agent prompts.
function parseDiff(diff) {
  const files = Array.from(parseDiffFiles(diff).values()).map((entry) => ({
    path: entry.path,
    hunks: entry.hunks,
  }));
  return files;
}

// The marker post-comments.mjs stamps on every published comment. GitHub
// attributes a comment to the account that owns the token - usually the
// developer's own - so the author cannot tell a bot comment from a developer's.
// The marker can, and it is the same literal prefix post-comments.mjs and
// cross-PR review tooling match on.
const BOT_MARKER_PREFIX = '**[review-pr';

// Thread bodies are pasted verbatim into every dispatched agent's prompt. Long enough
// to carry a full finding plus an answer.
const THREAD_EXCERPT_MAX = 800;

// Walking a parent chain on data fetched from a paginated API: the guard stops a
// cycle a malformed page could introduce, and a chain whose parent is not in the
// live set roots at the highest ancestor actually held.
const PARENT_CHAIN_MAX = 50;

function isBotComment(raw) {
  return String(raw ?? '').trimStart().startsWith(BOT_MARKER_PREFIX);
}

function excerpt(text) {
  const flat = String(text ?? '').replace(/\r\n/g, '\n').trim();
  if (flat.length <= THREAD_EXCERPT_MAX) {
    return flat;
  }
  const cut = flat.length - THREAD_EXCERPT_MAX;
  return `${flat.slice(0, THREAD_EXCERPT_MAX)}\n[... recortado, ${cut} caracteres mas]`;
}

function authorOf(comment) {
  const name = comment.author;
  if (typeof name === 'string' && name.trim() !== '') {
    return name.trim();
  }
  return 'desconocido';
}

function describeComment(comment) {
  const raw = typeof comment.body === 'string' ? comment.body : '';
  return {
    id: comment.id,
    author: authorOf(comment),
    createdAt: typeof comment.createdAt === 'string' ? comment.createdAt : '',
    isBot: isBotComment(raw),
    body: excerpt(raw),
  };
}

// Only a review comment on a line can be replied to: GitHub threads those and
// nothing else. A conversation comment or a review body stands alone.
function isReplyable(comment) {
  return comment.kind === 'inline';
}

// Groups the PR's comments into threads: one root plus its replies, flattened, so
// a reply-to-a-reply lands in the same thread as the finding it descends from.
// Only review comments thread; every other comment is a thread of its own.
function buildThreads(existingComments) {
  const live = existingComments.filter((c) => c && Number.isInteger(c.id));
  const byId = new Map(live.filter(isReplyable).map((c) => [c.id, c]));

  const rootOf = (comment) => {
    let current = comment;
    for (let step = 0; step < PARENT_CHAIN_MAX; step += 1) {
      const parentId = current.parentId;
      if (!Number.isInteger(parentId) || !byId.has(parentId) || parentId === current.id) {
        return current;
      }
      current = byId.get(parentId);
    }
    return current;
  };

  const grouped = new Map();
  for (const comment of live) {
    const root = isReplyable(comment) ? rootOf(comment) : comment;
    const key = `${root.kind}:${root.id}`;
    if (!grouped.has(key)) {
      grouped.set(key, { root, replies: [] });
    }
    if (comment !== root) {
      grouped.get(key).replies.push(comment);
    }
  }

  const threads = [];
  for (const { root, replies } of grouped.values()) {
    const ordered = replies.slice().sort((a, b) => String(a.createdAt ?? '').localeCompare(String(b.createdAt ?? '')));
    const describedRoot = describeComment(root);
    const describedReplies = ordered.map(describeComment);
    const last = describedReplies.length > 0 ? describedReplies[describedReplies.length - 1] : describedRoot;
    const line = Number.isInteger(root.line) ? root.line : (Number.isInteger(root.originalLine) ? root.originalLine : null);
    threads.push({
      // The id an agent puts in `parentId` to reply into this thread.
      parentId: root.id,
      kind: typeof root.kind === 'string' ? root.kind : null,
      replyable: isReplyable(root),
      path: typeof root.path === 'string' ? root.path : null,
      line,
      rootIsBot: describedRoot.isBot,
      developerReplied: describedReplies.some((r) => !r.isBot),
      // The last word in the thread is the developer's: the bot owes an answer.
      // A thread the bot already answered is NOT active.
      awaitingBotReply: !last.isBot,
      root: describedRoot,
      replies: describedReplies,
    });
  }
  return threads.sort((a, b) => a.parentId - b.parentId);
}

function activeThreadsOf(threads) {
  return threads.filter((t) => t.replyable && t.rootIsBot && t.developerReplied && t.awaitingBotReply);
}

// Rendered into every agent's prompt. Only ACTIVE threads appear: a bot finding a
// developer answered and nobody answered back.
function buildThreadsMarkdown(activeThreads, commentsTruncated) {
  // Nothing to say: emit no section at all, not a "(ninguno)" heading. A
  // working-tree review takes this path on every run.
  if (activeThreads.length === 0 && !commentsTruncated) {
    return '';
  }
  const lines = ['## Hilos abiertos (el developer respondio y el bot aun no)', ''];
  if (commentsTruncated) {
    lines.push(
      '> ADVERTENCIA: la lista de comentarios del PR viene truncada por paginacion.',
      '> Puede haber hilos abiertos que no aparecen aqui. No asumas que esta lista es completa.',
      '',
    );
  }
  if (activeThreads.length === 0) {
    lines.push('  (ninguno visible; ver la advertencia de truncado)');
    return lines.join('\n');
  }
  lines.push(
    'Responde SOLO los hilos cuyo hallazgo pertenece a tu concern, con `parentId` igual al id indicado.',
    'Nunca inventes un `parentId`: solo los ids listados aqui son validos.',
    '',
  );
  for (const thread of activeThreads) {
    const anchor = thread.path === null
      ? '(comentario general, sin ancla)'
      : `${thread.path}${thread.line === null ? '' : `:L${thread.line}`}`;
    lines.push(`### parentId: ${thread.parentId} - ${anchor}`);
    lines.push('');
    lines.push(`- observacion original (bot):`);
    lines.push(indentBlock(thread.root.body));
    for (const reply of thread.replies) {
      const who = reply.isBot ? 'bot' : `developer ${reply.author}`;
      lines.push(`- respuesta de ${who}:`);
      lines.push(indentBlock(reply.body));
    }
    lines.push('');
  }
  return lines.join('\n').trimEnd();
}

function indentBlock(text) {
  return String(text)
    .split('\n')
    .map((line) => `      ${line}`)
    .join('\n');
}

// Only extra-profile work gets the line.
function extraProfileLines(scope) {
  if (scope === null) {
    return [];
  }
  if (scope.suggestedProfile === null && scope.zoneFiles.length === 0) {
    return [];
  }
  const outside = scope.outsideZoneFiles.length === 0
    ? 'none'
    : scope.outsideZoneFiles.join(', ');
  const lines = [
    `- ${scope.profile}: ${scope.zoneFiles.length} file(s) in the ${scope.profile} zone; outside the zone: ${outside}`,
  ];
  return lines;
}

function buildDigestMarkdown({ prId, title, source, destination, lenientMode, lenientPattern, files, stats, threadsMarkdown, extraScope }) {
  const filesBlock = files.length === 0
    ? '  (none - empty diff)'
    : files.map((f) => `  - ${f.path} (${f.hunks.length} hunk${f.hunks.length === 1 ? '' : 's'})`).join('\n');
  const lenientLine = lenientPattern === null
    ? `- lenientMode: no (no lenient destination pattern configured; strict review)`
    : `- lenientMode (destination matches /${lenientPattern}/): ${lenientMode ? 'yes' : 'no'}`;
  return [
    '## PR ground truth (from fetched input.json - do not deviate)',
    '',
    `- prId: ${prId}`,
    `- title: ${title}`,
    `- source: ${source}`,
    `- destination: ${destination}`,
    lenientLine,
    `- stats: ${stats.filesCount} file(s), ${stats.diffChars} chars of diff`,
    ...extraProfileLines(extraScope),
    '- files touched (ONLY these paths are valid for inline comments):',
    filesBlock,
  ].concat(threadsMarkdown === '' ? [] : ['', threadsMarkdown]).join('\n');
}

// Files that cannot carry a logic or security defect: documentation, lockfiles and
// plain data only. This list decides whether a whole reviewer is skipped, so keep it
// narrow. Never add `.json` or other config, `.gitignore` or `.gitattributes`.
const INERT_EXTENSIONS = new Set([
  '.md', '.markdown', '.txt', '.rst', '.adoc',
  '.csv', '.tsv',
  '.svg', '.png', '.jpg', '.jpeg', '.gif', '.ico', '.webp',
]);

const INERT_BASENAMES = new Set([
  'package-lock.json', 'yarn.lock', 'pnpm-lock.yaml', 'poetry.lock',
  'Gemfile.lock', 'composer.lock', 'pubspec.lock', 'go.sum', 'Cargo.lock',
  'LICENSE', 'CHANGELOG.md',
]);

function extensionOf(path) {
  const base = path.split('/').pop() ?? path;
  const dot = base.lastIndexOf('.');
  if (dot <= 0) {
    return '';
  }
  return base.slice(dot).toLowerCase();
}

function basenameOf(path) {
  return path.split('/').pop() ?? path;
}

function isInert(path) {
  return INERT_BASENAMES.has(basenameOf(path)) || INERT_EXTENSIONS.has(extensionOf(path));
}

// Directory segments that hold tests in the common layouts of every stack.
const TEST_DIR_SEGMENTS = new Set(['test', 'tests', '__tests__', 'spec', 'specs', 'e2e', 'integration_test']);

// Basenames of a test file: `x.test.ts`, `x.spec.js`, `x_test.go`, `test_x.py`,
// `XTest.java`, `XTests.cs`, `x_spec.rb`, and pytest's `conftest.py` anywhere.
const TEST_BASENAME_PATTERNS = [
  /\.(?:test|spec)\.[A-Za-z0-9]+$/,
  /_(?:test|spec)\.[A-Za-z0-9]+$/,
  /^test_[^/]+\.py$/,
  /^conftest\.py$/,
  /[a-z0-9](?:Test|Tests|IT)\.(?:java|kt|kts|scala|cs|groovy)$/,
];

/**
 * True for a file that is a test by its location or its name. Decides whether the
 * test reviewer is dispatched, so a miss skips it: keep the patterns inclusive.
 */
function isTestFile(path) {
  const segments = path.split('/');
  const base = segments[segments.length - 1];
  const inTestDir = segments.slice(0, -1).some((segment) => TEST_DIR_SEGMENTS.has(segment.toLowerCase()));
  const hasTestName = TEST_BASENAME_PATTERNS.some((pattern) => pattern.test(base));
  return inTestDir || hasTestName;
}

// Per-concern surface: does this diff contain anything the concern could possibly
// find a defect in? A concern with no surface is not dispatched at all. The
// dispatcher MUST report every skip.
//
// Decides whether this change is the extra profile's work, and what that profile
// would dispatch for it. Two independent signals, either one enough: the source
// branch starts with `<name>/`, or every touched file lives inside the configured
// zone. Null when no extra profile is configured, so the digest stays as it was.
//
// `outsideZoneFiles` is what makes a PR mixed: zone work plus a change elsewhere.
// The extra profile then also dispatches the code concerns for that part.
function extraProfileScope(extra, source, files) {
  if (extra === null) {
    return null;
  }
  const paths = files.map((f) => f.path);
  const inZone = (path) => extra.zoneMatcher !== null && extra.zoneMatcher(path);
  const zoneFiles = paths.filter((p) => inZone(p));
  const outsideZoneFiles = paths.filter((p) => !inZone(p));
  const byBranch = source.startsWith(extra.branchPrefix);
  const byPaths = paths.length > 0 && outsideZoneFiles.length === 0;

  let reason = null;
  if (byBranch) {
    reason = `the source branch '${source}' follows the ${extra.branchPrefix}* convention`;
  } else if (byPaths) {
    reason = `every touched file is inside the ${extra.name} zone (${extra.zone})`;
  }

  const scope = {
    profile: extra.name,
    zone: extra.zone,
    byBranch,
    byPaths,
    zoneFiles,
    outsideZoneFiles,
    outsideZoneSubstantive: outsideZoneFiles.filter((p) => !isInert(p)),
    mixed: zoneFiles.length > 0 && outsideZoneFiles.length > 0,
    suggestedProfile: reason === null ? null : extra.name,
    reason,
  };
  return scope;
}

// `secrets` always has surface: a credential can leak into any file, documentation
// and plain data included. Every other code concern needs a non-inert file, so a
// documentation-only change runs `secrets` alone.
//
// `logic` needs a non-test, non-inert file; `test` a test file that is not inert.
// `security`, `patterns` and `architecture` are opt-in (a flag, a design reference or
// the `full` profile); their surface only says whether there is code to review.
//
// The extra concern, when configured, has surface when the change is its work at
// all: a file in its zone, or a `<name>/` source branch. On such a branch a diff
// with no zone file yet is still reviewable - it may carry what the branch must not.
function surfaceByConcern(files, extraScope) {
  const paths = files.map((f) => f.path);
  const substantive = paths.filter((p) => !isInert(p));
  const hasCode = substantive.length > 0;
  const testFiles = substantive.filter((p) => isTestFile(p));
  const hasTests = testFiles.length > 0;
  const nonTestCode = substantive.filter((p) => !isTestFile(p));
  const hasNonTestCode = nonTestCode.length > 0;
  const inertReason = 'every changed file is documentation, a lockfile or plain data';
  const codeSurface = { hasSurface: hasCode, reason: hasCode ? null : inertReason };
  let logicReason = null;
  if (!hasCode) {
    logicReason = inertReason;
  } else if (!hasNonTestCode) {
    logicReason = 'only test files changed';
  }
  let testReason = null;
  if (!hasCode) {
    testReason = inertReason;
  } else if (!hasTests) {
    testReason = 'no test file changed';
  }
  const surface = {
    logic: { hasSurface: hasNonTestCode, reason: logicReason },
    security: { ...codeSurface },
    architecture: { ...codeSurface },
    patterns: { ...codeSurface },
    secrets: { hasSurface: paths.length > 0, reason: paths.length > 0 ? null : 'empty diff' },
    test: { hasSurface: hasTests, reason: testReason },
    substantiveFiles: substantive,
    inertFiles: paths.filter((p) => isInert(p)),
    testFiles,
    // What the `default` profile dispatches on this diff before any flag:
    // secrets, plus logic and test where they have surface.
    defaultConcerns: [],
  };
  for (const concern of ['secrets', 'logic', 'test']) {
    if (surface[concern].hasSurface) {
      surface.defaultConcerns.push(concern);
    }
  }
  if (extraScope !== null) {
    const hasExtraSurface = extraScope.zoneFiles.length > 0 || extraScope.byBranch;
    surface[extraScope.profile] = {
      hasSurface: hasExtraSurface,
      reason: hasExtraSurface
        ? null
        : `no file inside the ${extraScope.profile} zone, and the source branch does not start with ${extraScope.profile}/`,
    };
  }
  return surface;
}

async function main() {
  const usage = 'Usage: digest.mjs <pr-id|url|working>';
  rejectUnknownFlags('digest.mjs', usage, {});
  const arg = process.argv[2];
  const prId = parsePrId(arg);
  if (prId === null) {
    process.stderr.write(`${usage}\n`);
    process.exit(2);
  }

  const inputPath = join(INPUT_DIR, `${prId}.input.json`);
  let raw;
  try {
    raw = readFileSync(inputPath, 'utf-8');
  } catch (err) {
    throw new Error(`Cannot read ${inputPath}: ${err.message}`);
  }
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new Error(`${inputPath} is not valid JSON: ${err.message}`);
  }

  const meta = parsed.meta ?? {};
  const title = (typeof meta.title === 'string') ? meta.title.trim() : '';
  const source = typeof meta.source?.branch === 'string' ? meta.source.branch : '';
  const destination = typeof meta.destination?.branch === 'string' ? meta.destination.branch : '';
  const diff = (typeof parsed.diff === 'string') ? parsed.diff : '';

  const missing = [];
  if (title === '') {
    missing.push('meta.title');
  }
  if (source === '') {
    missing.push('meta.source.branch');
  }
  if (destination === '') {
    missing.push('meta.destination.branch');
  }
  if (diff.trim() === '') {
    missing.push('diff');
  }
  if (missing.length > 0) {
    throw new Error(`input.json missing required fields: ${missing.join(', ')}`);
  }

  const files = parseDiff(diff);
  const matcher = lenientDestMatcher();
  const lenientPattern = matcher === null ? null : matcher.source;
  const lenientMode = matcher === null ? false : matcher.test(destination);
  const stats = { filesCount: files.length, diffChars: diff.length };
  const extraScope = extraProfileScope(resolveExtraProfile(), source, files);

  const existingComments = Array.isArray(parsed.existingComments) ? parsed.existingComments : [];
  const commentsTruncated = Boolean(parsed.pagination?.commentsTruncated);
  const threads = buildThreads(existingComments);
  const activeThreads = activeThreadsOf(threads);
  const threadsMarkdown = buildThreadsMarkdown(activeThreads, commentsTruncated);

  const output = {
    prId,
    title,
    source,
    destination,
    lenientMode,
    lenientPattern,
    files,
    stats,
    head: parsed.head ?? null,
    contextLines: parsed.contextLines ?? parsed.contextRequested ?? null,
    surface: surfaceByConcern(files, extraScope),
    // Present only when an extra profile is configured. `suggestedProfile` is what the
    // dispatcher switches to when no --profile was passed, and `outsideZoneSubstantive`
    // is what adds the code concerns to a mixed PR.
    ...(extraScope === null ? {} : { extraProfile: extraScope }),
    // Every thread on the PR, grouped. `activeThreads` is the subset an agent may
    // reply to; the full list is kept so the dispatcher can report what it saw.
    threads,
    activeThreads,
    // The set of ids a `parentId` may take. verify-report.mjs re-derives this
    // from input.json rather than trusting the digest, so the two must agree.
    validParentIds: threads.filter((t) => t.replyable).flatMap((t) => [t.parentId, ...t.replies.map((r) => r.id)]),
    commentsTruncated,
    inputPath,
    digestMarkdown: buildDigestMarkdown({ prId, title, source, destination, lenientMode, lenientPattern, files, stats, threadsMarkdown, extraScope }),
  };
  process.stdout.write(JSON.stringify(output, null, 2));
}

main().catch((err) => {
  process.stderr.write(`digest.mjs failed: ${err.message}\n`);
  process.exitCode = 1;
});
