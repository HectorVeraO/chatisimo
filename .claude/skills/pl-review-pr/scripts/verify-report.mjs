#!/usr/bin/env node
// Deterministic post-verifier. Drops every comment an agent could not ground, then
// rebuilds the general comment from the survivors so its counts cannot lie.
//
// Gate order matters: anchoring first (path, line, evidence, parentId), then the
// re-review delta, so a fabrication is never reported as a scope drop.

import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { parseDiff, describeHunks, normalizeEol, parseReviewId } from './lib/diff.mjs';
import { isInDelta, describeDelta, DEFAULT_TOLERANCE_LINES } from './lib/delta.mjs';
import { buildGeneralComment, isGeneral, bodyWords, verdictFor, normalizeSeverity, formatFindingBody } from './lib/general-comment.mjs';
import { parseProfileArg, profilesFor, resolveExtraProfile } from './lib/extra-profile.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

const INPUT_DIR = '.me/review-pr';
const LEGACY_INPUT_DIR = '.agents/review-pr';
const EVIDENCE_MIN_LENGTH = 20;

// Drop reasons that mean a comment could not be anchored to the diff at all. Only
// these feed `fabricationSuspected`: a comment dropped as `outside-rereview-delta`
// was anchored correctly and filtered for scope, which is not fabrication.
const ANCHORING_FAILURES = new Set([
  'invalid-parent-id',
  'parent-not-in-pr',
  'parent-not-replyable',
  'path-not-in-diff',
  'line-outside-hunks',
  'missing-evidence',
  'evidence-too-short',
  'evidence-not-in-this-file',
  'evidence-not-in-diff',
]);

// The profile list lives in lib/extra-profile.mjs, shared with merge-reports.mjs,
// post-comments.mjs and rereview-delta.mjs.
const EXTRA = resolveExtraProfile();
const PROFILES = profilesFor(EXTRA);

function parseProfile() {
  return parseProfileArg(process.argv, EXTRA);
}

// Accepts the literal 'working' alongside a PR number or URL, so the same verifier
// gates a working-tree review. lib/gh.mjs's parsePrId does NOT: its throw stops an
// uncommitted review from being published to a pull request.
function parsePrId(arg) {
  const id = parseReviewId(arg);
  if (id === null || id === 'working') {
    return id;
  }
  return Number(id);
}

function readIfExists(...candidates) {
  for (const path of candidates) {
    if (existsSync(path)) {
      return { path, raw: readFileSync(path, 'utf-8') };
    }
  }
  return null;
}

/**
 * The comment ids the PR actually carries, split by whether a reply can target
 * them: `replyable` holds review comments on a line (the only kind GitHub
 * threads), `standalone` conversation comments and review bodies. An invented
 * parentId is in neither and is dropped.
 */
export function collectParentIds(existingComments) {
  const replyable = new Set();
  const standalone = new Set();
  if (!Array.isArray(existingComments)) {
    return { replyable, standalone };
  }
  for (const comment of existingComments) {
    if (!comment || !Number.isInteger(comment.id)) {
      continue;
    }
    if (comment.kind === 'inline') {
      replyable.add(comment.id);
    } else {
      standalone.add(comment.id);
    }
  }
  return { replyable, standalone };
}

/**
 * Destination-side line ranges per path, from the parsed diff: the lines a review
 * comment can anchor to. post-comments.mjs moves any other finding into the body.
 */
export function anchorableRangesOf(fileMap) {
  const ranges = {};
  for (const [path, entry] of fileMap) {
    ranges[path] = entry.hunks.map((h) => [h.startLine, h.endLine]);
  }
  return ranges;
}

// A merged comment may carry several evidence candidates, one per concern that
// flagged the same location (see merge-reports.mjs). Each is a verbatim string a
// reviewer wrote; none is ever rewritten.
function evidenceCandidates(evidence) {
  const list = Array.isArray(evidence) ? evidence : [evidence];
  const candidates = list.filter((e) => typeof e === 'string');
  return candidates;
}

/**
 * Classifies one reviewer comment against the diff, the PR's comment ids
 * (`collectParentIds`) and
 * the re-review delta. Returns { kept, kind } plus { reason, detail } when dropped,
 * and { evidence } - the candidate that grounded it - when an anchored comment is kept.
 */
export function classifyComment(comment, fileMap, normalizedDiff, validParentIds, delta) {
  const candidates = evidenceCandidates(comment.evidence);
  if (candidates.length > 1) {
    return classifyAcrossCandidates(comment, candidates, fileMap, normalizedDiff, validParentIds, delta);
  }
  const rawPath = (typeof comment.path === 'string' && comment.path.length > 0) ? comment.path : null;
  const rawLine = Number.isInteger(comment.line) ? comment.line : null;
  const rawEvidence = candidates.length === 1 ? candidates[0] : null;
  const hasParent = comment.parentId !== undefined && comment.parentId !== null;

  if (hasParent) {
    if (!Number.isInteger(comment.parentId) || comment.parentId <= 0) {
      return {
        kept: false,
        kind: 'reply',
        reason: 'invalid-parent-id',
        detail: `parentId must be a positive integer, got ${JSON.stringify(comment.parentId)}`,
      };
    }
    if (!validParentIds.replyable.has(comment.parentId) && validParentIds.standalone.has(comment.parentId)) {
      return {
        kept: false,
        kind: 'reply',
        reason: 'parent-not-replyable',
        detail: `parentId ${comment.parentId} is a conversation comment or a review body; GitHub threads replies only under a review comment on a line`,
      };
    }
    if (!validParentIds.replyable.has(comment.parentId)) {
      return {
        kept: false,
        kind: 'reply',
        reason: 'parent-not-in-pr',
        detail: `parentId ${comment.parentId} is not a comment on this PR (input.json.existingComments holds ${validParentIds.replyable.size} replyable comment id(s))`,
      };
    }
  }

  // A reply with no path answers inside an already-anchored thread, so it carries no
  // new claim to ground. A reply WITH a path falls through to the same gates as any
  // fresh comment. Neither passes through the delta gate.
  if (rawPath === null) {
    return { kept: true, kind: hasParent ? 'reply' : 'general' };
  }

  const fileEntry = fileMap.get(rawPath);
  if (!fileEntry) {
    return {
      kept: false,
      kind: 'inline',
      reason: 'path-not-in-diff',
      detail: `path '${rawPath}' is not in the diff's file list`,
    };
  }

  if (rawLine !== null) {
    const inHunk = fileEntry.hunks.some((h) => rawLine >= h.startLine && rawLine <= h.endLine);
    if (!inHunk) {
      return {
        kept: false,
        kind: 'inline',
        reason: 'line-outside-hunks',
        detail: `line ${rawLine} is outside hunks for ${rawPath} (hunks: ${describeHunks(fileEntry.hunks)})`,
      };
    }
  }

  const kind = rawLine === null ? 'file-level' : 'inline';

  if (rawEvidence === null || rawEvidence.trim() === '') {
    return {
      kept: false,
      kind,
      reason: 'missing-evidence',
      detail: `comment on ${rawPath}${rawLine !== null ? `:${rawLine}` : ''} has no 'evidence' field (verbatim diff snippet required for ${kind} comments)`,
    };
  }

  const normalizedEvidence = normalizeEol(rawEvidence).trim();
  if (normalizedEvidence.length < EVIDENCE_MIN_LENGTH) {
    return {
      kept: false,
      kind,
      reason: 'evidence-too-short',
      detail: `evidence is ${normalizedEvidence.length} chars; minimum ${EVIDENCE_MIN_LENGTH} required to be specific enough for grounding`,
    };
  }

  // Scoped to this file's own diff section, not the whole diff. Evidence found nowhere
  // and evidence found in ANOTHER file are reported as separate reasons.
  if (!normalizeEol(fileEntry.body).includes(normalizedEvidence)) {
    if (normalizedDiff.includes(normalizedEvidence)) {
      return {
        kept: false,
        kind,
        reason: 'evidence-not-in-this-file',
        detail: `evidence is a verbatim line of the diff but NOT of ${rawPath}'s own section - the finding is anchored to the wrong file (first 80 chars: ${JSON.stringify(normalizedEvidence.slice(0, 80))})`,
      };
    }
    return {
      kept: false,
      kind,
      reason: 'evidence-not-in-diff',
      detail: `evidence string is not a verbatim substring of input.json.diff (first 80 chars: ${JSON.stringify(normalizedEvidence.slice(0, 80))})`,
    };
  }

  const tolerance = Number.isInteger(delta && delta.toleranceLines)
    ? delta.toleranceLines
    : DEFAULT_TOLERANCE_LINES;
  if (!isInDelta(delta, rawPath, rawLine, tolerance)) {
    return {
      kept: false,
      kind,
      reason: 'outside-rereview-delta',
      detail: `${rawPath}${rawLine !== null ? `:${rawLine}` : ''} did not change since the last review (${delta.lastReviewedSha}); a live finding on unchanged code belongs in its existing thread as a reply`,
    };
  }

  return { kept: true, kind, evidence: rawEvidence };
}

// The comment is kept when ANY candidate grounds it, and it is published with that
// candidate. When none does, the first candidate's failure is reported: candidates
// are ordered most severe contributor first, so that is the most relevant one.
function classifyAcrossCandidates(comment, candidates, fileMap, normalizedDiff, validParentIds, delta) {
  let first = null;
  for (const evidence of candidates) {
    const result = classifyComment({ ...comment, evidence }, fileMap, normalizedDiff, validParentIds, delta);
    if (result.kept) {
      return result;
    }
    if (first === null) {
      first = result;
    }
  }
  const failed = { ...first, detail: `${first.detail} (none of ${candidates.length} evidence candidates grounded it)` };
  return failed;
}

/**
 * Why a comment may not carry `resolvesThread`, or null when it may (or carries
 * none). Only an `info` reply with a positive integer `parentId` closes a thread:
 * the flag on anything else is stripped rather than the comment dropped, so a
 * stray flag never costs a real finding and never resolves a thread.
 */
export function resolvesThreadProblem(comment) {
  if (comment.resolvesThread === undefined) {
    return null;
  }
  if (comment.resolvesThread !== true) {
    return `resolvesThread must be true when present, got ${JSON.stringify(comment.resolvesThread)}`;
  }
  if (!Number.isInteger(comment.parentId) || comment.parentId <= 0) {
    return 'resolvesThread needs a reply: parentId must be a positive integer';
  }
  if (comment.severity !== 'info') {
    return `resolvesThread needs severity info, got ${JSON.stringify(comment.severity)}`;
  }
  return null;
}

/**
 * Verifies a merged report against its input packet and optional re-review delta.
 * Returns the verdict, the rebuilt comment list (general comment first), and the
 * counts and drop details the caller reports.
 */
export function verifyReport({ report, input, delta }) {
  const diffString = typeof input.diff === 'string' ? input.diff : '';
  const fileMap = parseDiff(diffString);
  const normalizedDiff = normalizeEol(diffString);
  const validParentIds = collectParentIds(input.existingComments);

  const kept = [];
  const dropped = [];
  let inlineCandidates = 0;
  let fileLevelCandidates = 0;
  let replyCandidates = 0;
  const severityCoerced = [];
  const resolvesThreadStripped = [];

  for (const rawComment of report.comments) {
    // The general comment is regenerated below from the survivors, so the incoming
    // one is never carried through: keeping it would republish stale counts.
    if (isGeneral(rawComment)) {
      continue;
    }
    // Severity is canonicalised before anything counts it, and an unreadable one is
    // a blocker (lib/general-comment.mjs, normalizeSeverity). The published comment
    // carries the canonical value, so the publisher's tally sees the same tier.
    const normalized = normalizeSeverity(rawComment.severity);
    if (normalized.coerced) {
      severityCoerced.push({
        path: rawComment.path ?? null,
        line: Number.isInteger(rawComment.line) ? rawComment.line : null,
        parentId: rawComment.parentId ?? null,
        original: normalized.original,
        severity: normalized.severity,
      });
    }
    const comment = { ...rawComment, severity: normalized.severity };
    const resolveProblem = resolvesThreadProblem(comment);
    if (resolveProblem !== null) {
      delete comment.resolvesThread;
      resolvesThreadStripped.push({
        path: comment.path ?? null,
        line: Number.isInteger(comment.line) ? comment.line : null,
        parentId: comment.parentId ?? null,
        severity: comment.severity,
        detail: resolveProblem,
      });
    }
    const classification = classifyComment(comment, fileMap, normalizedDiff, validParentIds, delta);
    if (classification.kind === 'inline') {
      inlineCandidates += 1;
    } else if (classification.kind === 'file-level') {
      fileLevelCandidates += 1;
    }
    if (comment.parentId !== undefined && comment.parentId !== null) {
      replyCandidates += 1;
    }
    if (classification.kept) {
      // Laid out exactly as post-comments.mjs publishes it, so the verified report
      // shows the body that lands on the PR.
      const survivor = { ...comment };
      if (typeof comment.body === 'string') {
        survivor.body = formatFindingBody(comment.body);
      }
      survivor.bodyWords = bodyWords(survivor.body);
      if (typeof classification.evidence === 'string') {
        survivor.evidence = classification.evidence;
      }
      kept.push(survivor);
    } else {
      dropped.push({
        path: comment.path ?? null,
        line: Number.isInteger(comment.line) ? comment.line : null,
        parentId: comment.parentId ?? null,
        severity: comment.severity ?? null,
        reason: classification.reason,
        detail: classification.detail,
        bodyPreview: String(comment.body ?? '').slice(0, 160),
      });
    }
  }

  const general = report.general ?? {};
  // Only the surviving findings decide: a concern whose findings were all dropped
  // leaves nothing pending, whatever verdict it declared.
  const verdict = verdictFor(kept);
  const generalComment = buildGeneralComment({
    verdict,
    ran: general.ran ?? [],
    notRun: general.notRun ?? [],
    summaries: general.summaries ?? [],
    comments: kept,
    scopeNote: delta ? describeDelta(delta) : null,
    // The report's own profile decides whether the general comment carries the
    // extra profile's non-technical section; it is rebuilt here from the survivors.
    profile: typeof report.profile === 'string' ? report.profile : null,
  });

  const anchoringFailures = dropped.filter((d) => ANCHORING_FAILURES.has(d.reason)).length;
  const fabricationSuspected = anchoringFailures > 0 && kept.every((c) => !c.path);
  const overBudget = kept.filter((c) => (c.bodyWords ?? 0) > 95).length;

  return {
    verdict,
    comments: [generalComment, ...kept],
    kept: kept.length,
    dropped,
    inlineCandidates,
    fileLevelCandidates,
    replyCandidates,
    validParentIds: validParentIds.replyable.size,
    anchorableRanges: anchorableRangesOf(fileMap),
    fabricationSuspected,
    severityCoerced,
    resolvesThreadStripped,
    overBudget,
    filesInDiff: Array.from(fileMap.keys()),
  };
}

async function main() {
  const usage = `Usage: verify-report.mjs <pr-id|url|working> [--profile ${PROFILES.join('|')}]`;
  rejectUnknownFlags('verify-report.mjs', usage, { valueFlags: ['--profile'] });
  const arg = process.argv[2];
  const prId = parsePrId(arg);
  if (prId === null) {
    process.stderr.write(`${usage}\n`);
    process.exit(2);
  }

  const profile = parseProfile();
  const inputRead = readIfExists(
    join(INPUT_DIR, `${prId}.input.json`),
    join(LEGACY_INPUT_DIR, `${prId}.input.json`),
  );
  if (inputRead === null) {
    throw new Error(`Cannot read ${join(INPUT_DIR, `${prId}.input.json`)}`);
  }
  if (inputRead.path.startsWith(LEGACY_INPUT_DIR)) {
    process.stderr.write(`verify-report.mjs: WARN using the legacy input ${inputRead.path}; it may be stale. Re-fetch into ${INPUT_DIR}.\n`);
  }
  const reportPath = join(INPUT_DIR, `${prId}.${profile}.report.json`);
  const verifiedPath = join(INPUT_DIR, `${prId}.${profile}.report.verified.json`);

  let reportRaw;
  try {
    reportRaw = readFileSync(reportPath, 'utf-8');
  } catch (err) {
    throw new Error(`Cannot read ${reportPath}: ${err.message}`);
  }

  const input = JSON.parse(inputRead.raw);
  const report = JSON.parse(reportRaw);
  if (!Array.isArray(report.comments)) {
    throw new Error('Report JSON must have an array `comments` field.');
  }

  // A missing delta file is not an error: it means the dispatcher did not run
  // rereview-delta.mjs, which is the correct state for a first review.
  const deltaRead = readIfExists(join(INPUT_DIR, `${prId}.${profile}.delta.json`));
  const delta = deltaRead === null ? null : JSON.parse(deltaRead.raw);

  const result = verifyReport({ report, input, delta });

  const verifiedReport = {
    ...report,
    verdict: result.verdict,
    comments: result.comments,
    verifierMeta: {
      verifiedAt: new Date().toISOString(),
      // What this verified report belongs to. post-comments.mjs refuses to publish
      // it to any other PR or under any other profile marker.
      prId,
      profile,
      // The commit this review looked at. post-comments.mjs stamps it into the
      // published general comment, and rereview-delta.mjs reads it back on the next
      // run to compute the delta.
      headSha: (input.head && typeof input.head.sha === 'string') ? input.head.sha : null,
      kept: result.kept,
      dropped: result.dropped.length,
      droppedDetails: result.dropped,
      deltaMode: delta ? delta.mode : 'full',
      deltaReason: delta ? (delta.reason ?? null) : 'rereview-delta.mjs no se ejecuto',
      lastReviewedSha: delta ? (delta.lastReviewedSha ?? null) : null,
      commentsOverBudget: result.overBudget,
      severityCoerced: result.severityCoerced,
      resolvesThreadStripped: result.resolvesThreadStripped,
      // The lines GitHub accepts a review comment on, per path. post-comments.mjs
      // routes a finding outside them into the review body, because GitHub rejects
      // a review as a whole when one of its inline comments cannot be anchored.
      anchorableRanges: result.anchorableRanges,
    },
  };
  writeFileSync(verifiedPath, JSON.stringify(verifiedReport, null, 2));

  const summary = {
    prId,
    profile,
    verdict: result.verdict,
    kept: result.kept,
    dropped: result.dropped.length,
    inlineCandidates: result.inlineCandidates,
    fileLevelCandidates: result.fileLevelCandidates,
    replyCandidates: result.replyCandidates,
    validParentIds: result.validParentIds,
    deltaMode: delta ? delta.mode : 'full',
    deltaReason: delta ? (delta.reason ?? null) : 'rereview-delta.mjs no se ejecuto',
    commentsOverBudget: result.overBudget,
    fabricationSuspected: result.fabricationSuspected,
    severityCoerced: result.severityCoerced,
    resolvesThreadStripped: result.resolvesThreadStripped,
    verifiedPath,
    droppedDetails: result.dropped,
  };
  if (result.fabricationSuspected) {
    summary.filesInDiff = result.filesInDiff;
  }
  process.stdout.write(JSON.stringify(summary, null, 2) + '\n');

  if (result.fabricationSuspected) {
    process.exitCode = 2;
  }
}

const invokedDirectly = process.argv[1] && process.argv[1].endsWith('verify-report.mjs');
if (invokedDirectly) {
  main().catch((err) => {
    process.stderr.write(`verify-report.mjs failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
