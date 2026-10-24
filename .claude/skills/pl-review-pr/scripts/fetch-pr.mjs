#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { ghApi, ghApiAll, listPrComments, parsePrId, repoPath } from './lib/gh.mjs';
import { parseDiff, parseFileHeader } from './lib/diff.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

// File extensions whose diff payload is never worth handing to a reviewer:
// images, fonts, media, archives and compiled artifacts. A file with one of
// these extensions stays listed in the diff so the change remains visible, but
// its payload is replaced by a single placeholder line. Credential-shaped
// extensions (.pem, .key, .jks) are deliberately absent: their payload is small
// and a reviewer has to be able to read it.
const NON_REVIEWABLE_EXTENSIONS = new Set([
  'png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'ico', 'icns', 'tif', 'tiff',
  'svg', 'svgz', 'psd', 'ai', 'eps',
  'ttf', 'otf', 'woff', 'woff2', 'eot',
  'mp3', 'wav', 'flac', 'ogg', 'oga', 'ogv', 'mp4', 'm4a', 'm4v', 'mov', 'avi',
  'mkv', 'webm',
  'zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar', 'dmg', 'iso',
  'jar', 'war', 'ear', 'aar', 'apk', 'aab', 'ipa',
  'so', 'dll', 'dylib', 'exe', 'bin', 'obj', 'class', 'pyc', 'pyo', 'pyd',
  'wasm', 'pdb',
  'pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx', 'odt', 'ods', 'odp',
]);

// The metadata lines git emits between a 'diff --git' line and the first
// payload line. The header region ends at the first line that is not one of them,
// so no payload survives in a suppressed block.
const HEADER_LINE_PREFIXES = [
  'old mode ',
  'new mode ',
  'new file mode ',
  'deleted file mode ',
  'similarity index ',
  'dissimilarity index ',
  'rename from ',
  'rename to ',
  'copy from ',
  'copy to ',
  'index ',
  '--- ',
  '+++ ',
];

// The shared lib/diff header parser, so a quoted (non-ASCII or spaced) path is
// recognised here exactly as the digest and the verifier recognise it.
function blockPath(block) {
  const firstLine = block.split('\n', 1)[0];
  return parseFileHeader(firstLine);
}

function hasNonReviewableExtension(path) {
  if (typeof path !== 'string' || path === '') {
    return false;
  }
  const lastDot = path.lastIndexOf('.');
  const lastSlash = path.lastIndexOf('/');
  // Covers both "no extension at all" and a dotfile whose leading dot is not one.
  if (lastDot <= lastSlash + 1) {
    return false;
  }
  const extension = path.slice(lastDot + 1).toLowerCase();
  return NON_REVIEWABLE_EXTENSIONS.has(extension);
}

// Git renders a binary change either as a base64 'GIT binary patch' payload or
// as a single 'Binary files ... differ' line. Neither form carries a '@@' hunk
// header, so suppressing it can never remove a reviewable line range.
function isBinaryPayloadLine(line) {
  if (line.startsWith('GIT binary patch')) {
    return true;
  }
  const binaryFilesLine = line.startsWith('Binary file') && line.includes('differ');
  return binaryFilesLine;
}

// Splits a diff block into the header region git emits before any payload and
// the first payload line, which is what identifies the block as text or binary.
function readBlockHeader(lines) {
  const header = [lines[0]];
  let index = 1;
  while (index < lines.length) {
    const isMetadata = HEADER_LINE_PREFIXES.some((prefix) => lines[index].startsWith(prefix));
    if (!isMetadata) {
      break;
    }
    header.push(lines[index]);
    index += 1;
  }
  const firstPayloadLine = (index < lines.length) ? lines[index] : '';
  return { header, payloadStart: index, firstPayloadLine };
}

// A suppressed block keeps its 'diff --git' line so digest.mjs still lists the
// file, and drops every '@@' hunk header along with the payload so the digest
// advertises no line range whose backing content is gone.
//
// The placeholder is emitted as a diff context line -- leading space, no digits
// -- so a file-level finding about the asset can quote it verbatim as evidence.
// The suppressed hunk and byte counts stay out of the line and are reported in
// input.json's strippedFiles instead.
function suppressBlockPayload(block, path) {
  const lines = block.split('\n');
  const { header, payloadStart } = readBlockHeader(lines);
  const droppedLines = lines.slice(payloadStart);
  const hunksSuppressed = droppedLines.filter((line) => line.startsWith('@@')).length;
  const bytesOmitted = droppedLines.join('\n').length;
  const placeholder = ` [review-pr] content omitted for ${path}: non-reviewable asset or binary payload`;
  const rebuilt = `${header.join('\n')}\n${placeholder}\n`;
  return { block: rebuilt, hunksSuppressed, bytesOmitted };
}

/**
 * Replaces the payload of every binary or non-reviewable file block with a
 * placeholder line, and lists what was stripped. Runs once, before input.json is
 * written, so digest.mjs and verify-report.mjs both read the same stripped diff.
 */
export function stripNonReviewableContent(diff) {
  if (typeof diff !== 'string') {
    throw new Error('stripNonReviewableContent requires the diff as a string');
  }
  const parts = diff.split(/(?=^diff --git )/m);
  const strippedFiles = [];
  const rebuiltParts = parts.map((part) => {
    if (!part.startsWith('diff --git ')) {
      return part;
    }
    const path = blockPath(part);
    if (path === null) {
      return part;
    }
    const { firstPayloadLine } = readBlockHeader(part.split('\n'));
    const suppress = hasNonReviewableExtension(path) || isBinaryPayloadLine(firstPayloadLine);
    if (!suppress) {
      return part;
    }
    const suppressed = suppressBlockPayload(part, path);
    strippedFiles.push({
      path,
      hunksSuppressed: suppressed.hunksSuppressed,
      bytesOmitted: suppressed.bytesOmitted,
    });
    return suppressed.block;
  });
  const stripped = { diff: rebuiltParts.join(''), strippedFiles };
  return stripped;
}

function isBlank(value) {
  if (value === null || value === undefined) {
    return true;
  }
  if (typeof value !== 'string') {
    return false;
  }
  return value.trim() === '';
}

/**
 * The host-neutral `meta` block of the packet, built from a GitHub pull request
 * object. digest.mjs and build-working-input.mjs read the same shape.
 */
export function normalizePullRequest(pull) {
  if (!pull || typeof pull !== 'object') {
    throw new Error('normalizePullRequest: pull request object required');
  }
  const meta = {
    number: Number.isInteger(pull.number) ? pull.number : null,
    title: typeof pull.title === 'string' ? pull.title : '',
    body: typeof pull.body === 'string' ? pull.body : '',
    url: typeof pull.html_url === 'string' ? pull.html_url : null,
    state: typeof pull.state === 'string' ? pull.state : null,
    draft: pull.draft === true,
    author: typeof pull.user?.login === 'string' ? pull.user.login : null,
    source: {
      branch: typeof pull.head?.ref === 'string' ? pull.head.ref : '',
      sha: typeof pull.head?.sha === 'string' ? pull.head.sha : null,
      repo: typeof pull.head?.repo?.full_name === 'string' ? pull.head.repo.full_name : null,
    },
    destination: {
      branch: typeof pull.base?.ref === 'string' ? pull.base.ref : '',
      sha: typeof pull.base?.sha === 'string' ? pull.base.sha : null,
    },
  };
  return meta;
}

// Every missing field is reported at once, so one run shows everything wrong.
function validateMeta(meta, prId) {
  const required = [
    ['meta.title', meta.title],
    ['meta.source.branch', meta.source.branch],
    ['meta.destination.branch', meta.destination.branch],
  ];
  const missing = required.filter(([, v]) => isBlank(v));
  if (missing.length > 0) {
    const names = missing.map(([n]) => n).join(', ');
    throw new Error(`PR ${prId} metadata is incomplete; missing or empty: ${names}`);
  }
}

function validateDiff(diff, prId) {
  if (typeof diff !== 'string' || diff.trim() === '') {
    throw new Error(`PR diff is empty for PR ${prId}; refusing to emit partial input.json`);
  }
}

// GitHub serves the PR diff with its own fixed context (3 lines) and takes no
// context parameter. Its hunks are exactly the lines a review comment may anchor
// to, so the packet keeps them as served; reviewers read more of a file with
// `git show <head.sha>:<path>`.
const GITHUB_DIFF_CONTEXT_LINES = 3;

// The commit the reviewed content belongs to. Reviewers read context at it
// (`git show <sha>:<path>`), never from the checkout; `headAvailableLocally` says
// whether this clone has it. A missing sha degrades the review to diff-only; it
// never fails it.
function resolveHead(meta) {
  const sha = isBlank(meta.source.sha) ? null : meta.source.sha;
  return {
    sha,
    branch: meta.source.branch,
    readFrom: 'git-show',
    headAvailableLocally: sha === null ? false : commitExistsLocally(sha),
    unavailableReason: sha === null
      ? 'PR metadata carries no head commit sha (head.sha)'
      : null,
  };
}

function commitExistsLocally(sha) {
  const gitBin = process.platform === 'win32' ? 'git.exe' : 'git';
  const result = spawnSync(gitBin, ['cat-file', '-e', `${sha}^{commit}`], { encoding: 'utf-8' });
  return result.status === 0;
}

function normalizeFile(file) {
  const entry = {
    path: file.filename,
    status: typeof file.status === 'string' ? file.status : null,
    previousPath: typeof file.previous_filename === 'string' ? file.previous_filename : null,
    additions: Number.isInteger(file.additions) ? file.additions : null,
    deletions: Number.isInteger(file.deletions) ? file.deletions : null,
  };
  return entry;
}

// Paths GitHub lists for the PR but the diff carries no block for (GitHub omits
// some from the unified diff, e.g. on a very large PR). Reported, never guessed.
function filesMissingFromDiff(files, diff) {
  const inDiff = new Set(parseDiff(diff).keys());
  const missing = files.map((f) => f.path).filter((path) => !inDiff.has(path));
  return missing;
}

// GitHub answers 406 when a PR's diff exceeds what its API renders (a size or
// file-count limit). Nothing partial can be reviewed from that, so the run stops with
// the one way forward: the local working-tree review on the checked-out branch.
const DIFF_TOO_LARGE_STATUS = 406;

async function fetchDiff(base, prId) {
  try {
    const diff = await ghApi(base, { accept: 'application/vnd.github.diff' });
    return diff;
  } catch (err) {
    if (err.status !== DIFF_TOO_LARGE_STATUS) {
      throw err;
    }
    throw new Error(
      `PR ${prId}: the PR diff is too large for the GitHub API (HTTP 406). `
      + 'Run the local working-tree review on the checked-out branch instead.',
    );
  }
}

async function main() {
  const usage = 'Usage: fetch-pr.mjs <pr-number|pr-url>';
  rejectUnknownFlags('fetch-pr.mjs', usage, {});
  const arg = process.argv[2];
  const prId = parsePrId(arg);
  if (prId === null) {
    process.stderr.write(`${usage}\n`);
    process.exit(2);
  }

  const repo = repoPath();
  const base = `repos/${repo}/pulls/${prId}`;

  const pull = await ghApi(base);
  const meta = normalizePullRequest(pull);
  validateMeta(meta, prId);

  const diff = await fetchDiff(base, prId);
  // Validated on the raw diff so a genuinely empty PR still fails, then stripped:
  // input.json carries only the reviewable form.
  validateDiff(diff, prId);
  const stripped = stripNonReviewableContent(diff);

  const listedFiles = await ghApiAll(`${base}/files?per_page=100`);
  const files = listedFiles.values.map(normalizeFile);

  // Every page of every comment source: GitHub lists oldest first, so reading only
  // the first page would miss the newest head stamp and the newest threads.
  const comments = await listPrComments(repo, prId);

  const output = {
    prId,
    repo,
    meta,
    head: resolveHead(meta),
    contextLines: GITHUB_DIFF_CONTEXT_LINES,
    diff: stripped.diff,
    strippedFiles: stripped.strippedFiles,
    files,
    filesMissingFromDiff: filesMissingFromDiff(files, diff),
    existingComments: comments.comments,
    pagination: { commentsTruncated: comments.truncated, filesTruncated: listedFiles.truncated },
  };

  process.stdout.write(JSON.stringify(output, null, 2));
}

// Only when run as a command, so the strip logic can be imported and tested.
if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`fetch-pr.mjs failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
