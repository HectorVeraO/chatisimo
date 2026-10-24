// Re-review delta: which destination-side lines changed since the last bot review.
// New anchored findings must fall inside it; unchanged code is answered in its thread.
// Pure functions only.

import { parseFileHeader, parseHunkHeader, normalizeEol } from './diff.mjs';
// Profiles whose concerns a `full` review, or a pre-profile one, already ran; the
// optional extra profile is never among them (see lib/extra-profile.mjs).
import { COVERED_BY_FULL } from './extra-profile.mjs';

// Lines around a changed line that still count as inside the delta.
export const DEFAULT_TOLERANCE_LINES = 3;

// The stamp `post-comments.mjs` writes into the published review body: the only
// record of which commit a review looked at, read from the PR because the review may
// have run on another machine.
export const HEAD_STAMP_RE = /<!--\s*review-pr:head=([0-9a-f]{7,40})\s*-->/;

// Marker prefix of a bot comment. The profiled form is `**[review-pr:default · ...`; the
// pre-profile form `**[review-pr · ...` covers the same profiles a `full` review does.
const MARKER_PREFIX = '**[review-pr';


function commentBody(comment) {
  if (!comment || typeof comment !== 'object') {
    return '';
  }
  return typeof comment.body === 'string' ? comment.body : '';
}

// Which review in this comment covers `profile`: the profile itself, `full` (a full
// or pre-profile review), or null. The marker must OPEN the body; a human reply that
// quotes the bot never counts.
function coveringProfile(body, profile) {
  const text = body.trimStart();
  if (!text.startsWith(MARKER_PREFIX)) {
    return null;
  }
  const after = text.slice(MARKER_PREFIX.length);
  if (after.startsWith(' ')) {
    return COVERED_BY_FULL.has(profile) ? 'full' : null;
  }
  if (!after.startsWith(':')) {
    return null;
  }
  const seen = after.slice(1).split(/[\s·\]]/)[0];
  if (seen === profile) {
    return profile;
  }
  if (seen === 'full' && COVERED_BY_FULL.has(profile)) {
    return 'full';
  }
  return null;
}

function commentTimestamp(comment) {
  const raw = comment && (comment.updatedAt || comment.createdAt);
  const t = raw ? Date.parse(raw) : NaN;
  return Number.isNaN(t) ? 0 : t;
}

// Only a top-level comment carries a valid stamp: a submitted review's body or a
// PR conversation comment. A review comment on a line (`inline`) never does.
function isTopLevel(comment) {
  return comment.kind === 'review' || comment.kind === 'general';
}

/**
 * The newest stamped bot review covering this profile, as `{ sha, coveredBy }`, where
 * `coveredBy` is the profile itself or `full`; null when there is none.
 */
export function findLastReview(existingComments, profile) {
  if (!Array.isArray(existingComments)) {
    return null;
  }
  let best = null;
  for (const comment of existingComments) {
    if (!comment || !isTopLevel(comment)) {
      continue;
    }
    const body = commentBody(comment);
    const coveredBy = coveringProfile(body, profile);
    if (coveredBy === null) {
      continue;
    }
    const m = body.match(HEAD_STAMP_RE);
    if (!m) {
      continue;
    }
    const at = commentTimestamp(comment);
    if (best === null || at >= best.at) {
      best = { sha: m[1], coveredBy, at };
    }
  }
  if (best === null) {
    return null;
  }
  const review = { sha: best.sha, coveredBy: best.coveredBy };
  return review;
}

/**
 * The newest stamped sha from a bot general comment covering this profile, or null.
 */
export function findLastReviewedSha(existingComments, profile) {
  const review = findLastReview(existingComments, profile);
  return review === null ? null : review.sha;
}

/**
 * True when a bot comment covering this profile exists at all, stamped or not: a PR
 * reviewed before the head stamp existed, as opposed to one never reviewed.
 */
export function hasPriorReview(existingComments, profile) {
  if (!Array.isArray(existingComments)) {
    return false;
  }
  return existingComments.some(
    (c) => c && coveringProfile(commentBody(c), profile) !== null,
  );
}

/**
 * Destination-side changed-line ranges per file, from `git diff --unified=0` output.
 * A pure-deletion hunk is recorded as its join line.
 */
export function parseDeltaDiff(diff) {
  const files = new Map();
  let currentPath = null;
  for (const line of normalizeEol(diff).split('\n')) {
    if (line.startsWith('diff --git ')) {
      currentPath = parseFileHeader(line);
      if (currentPath !== null && !files.has(currentPath)) {
        files.set(currentPath, []);
      }
      continue;
    }
    if (currentPath === null || !line.startsWith('@@')) {
      continue;
    }
    const hunk = parseHunkHeader(line);
    if (hunk === null) {
      // Deletion-only hunk `@@ -a,b +c,0 @@`: record line c; the tolerance covers its neighbours.
      const m = line.match(/^@@ -\d+(?:,\d+)? \+(\d+),0 @@/);
      if (m) {
        const at = Math.max(1, Number(m[1]));
        files.get(currentPath).push([at, at]);
      }
      continue;
    }
    files.get(currentPath).push([hunk.startLine, hunk.endLine]);
  }
  const out = {};
  for (const [path, ranges] of files) {
    out[path] = mergeRanges(ranges);
  }
  return out;
}

/**
 * Sorts and merges overlapping or adjacent [start, end] line ranges.
 */
export function mergeRanges(ranges) {
  const sorted = [...ranges].sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const merged = [];
  for (const [start, end] of sorted) {
    const last = merged[merged.length - 1];
    if (last && start <= last[1] + 1) {
      last[1] = Math.max(last[1], end);
      continue;
    }
    merged.push([start, end]);
  }
  return merged;
}

/**
 * True when path:line is inside the re-review delta (always true outside delta mode).
 * A file-level comment (no line) is inside when its file is in the delta.
 */
export function isInDelta(delta, path, line, tolerance = DEFAULT_TOLERANCE_LINES) {
  if (!delta || delta.mode !== 'delta' || !delta.files) {
    return true;
  }
  const ranges = delta.files[path];
  if (!Array.isArray(ranges)) {
    return false;
  }
  if (!Number.isInteger(line)) {
    return ranges.length > 0;
  }
  return ranges.some(([start, end]) => line >= start - tolerance && line <= end + tolerance);
}

/**
 * One-line description of the review scope, for the general comment.
 */
export function describeDelta(delta) {
  if (!delta || delta.mode !== 'delta') {
    const reason = delta && delta.reason ? delta.reason : 'sin revision previa';
    return `full diff (${reason})`;
  }
  const files = Object.keys(delta.files || {}).length;
  return `re-review delta desde ${delta.lastReviewedSha} (${files} archivo(s))`;
}
