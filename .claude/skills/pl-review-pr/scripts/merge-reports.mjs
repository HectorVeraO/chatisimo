#!/usr/bin/env node
// Merges the per-concern review reports into the single
// <review-id>.<profile>.report.json that verify-report.mjs consumes.
//
// Invariants:
//  1. `evidence` is copied byte-for-byte. When concerns fold onto one location
//     with different evidence, every distinct string is kept as a candidate.
//  2. A missing per-concern report means that concern DID NOT RUN; it goes to
//     concernsSkipped, never "reviewed, no findings".
//  3. `parentId` is preserved and is part of the merge key.
import { readFileSync, writeFileSync } from 'node:fs';
import { buildGeneralComment, normalizeConcernVerdict, normalizeSeverity, SEVERITY_RANK, verdictFor } from './lib/general-comment.mjs';
import { join } from 'node:path';
import { parseReviewId } from './lib/diff.mjs';
import { parseProfileArg, profilesFor, resolveExtraProfile } from './lib/extra-profile.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

const INPUT_DIR = '.me/review-pr';

// The profile is part of every report path (`<review-id>.<profile>.report.*`); the
// input packet `<review-id>.input.json` is shared across profiles. The list lives in
// lib/extra-profile.mjs.
const EXTRA = resolveExtraProfile();
const PROFILES = profilesFor(EXTRA);

// The extra profile's concern carries the profile's own name, or null when disabled.
const EXTRA_CONCERN = EXTRA === null ? null : EXTRA.name;

function parseProfile() {
  return parseProfileArg(process.argv, EXTRA);
}

// Every code concern the dispatchers can fan out to. Dispatchers MUST pass
// --concerns with exactly what they dispatched; the defaults below are for a manual run.
const ALL_CONCERNS = ['logic', 'security', 'architecture', 'patterns', 'secrets', 'test'];

// What the `default` profile can run: secrets always, logic on non-test code, test
// on test files. patterns, security and architecture join only through --concerns.
const DEFAULT_PROFILE_CONCERNS = ['secrets', 'logic', 'test'];

// The extra concern is known but never part of the manual-run default.
const KNOWN_CONCERNS = EXTRA_CONCERN === null ? ALL_CONCERNS : [...ALL_CONCERNS, EXTRA_CONCERN];

// The fallback set for a manual run with no --concerns. The extra profile carries
// its own concern plus secrets; a mixed PR adds the code concerns explicitly
// through --concerns.
function defaultConcernsFor(profile) {
  if (EXTRA_CONCERN !== null && profile === EXTRA_CONCERN) {
    return [EXTRA_CONCERN, 'secrets'];
  }
  if (profile === 'full') {
    return ALL_CONCERNS;
  }
  return DEFAULT_PROFILE_CONCERNS;
}

function usage() {
  return `Usage: merge-reports.mjs <pr-id|url|working> [--profile ${PROFILES.join('|')}] [--concerns ${KNOWN_CONCERNS.join(',')}]`;
}

// Label shown to the developer on a merged comment, in Spanish like post-comments.mjs.
const CONCERN_LABELS = {
  logic: 'logica',
  security: 'seguridad',
  architecture: 'arquitectura',
  patterns: 'patrones',
  secrets: 'secretos',
  test: 'tests',
};


function parseConcernList(arg, profile) {
  if (arg === undefined || arg === null || arg === '') {
    return defaultConcernsFor(profile);
  }
  const requested = String(arg)
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '');
  const unknown = requested.filter((c) => !KNOWN_CONCERNS.includes(c));
  if (unknown.length > 0) {
    throw new Error(`Unknown concern(s): ${unknown.join(', ')}. Known: ${KNOWN_CONCERNS.join(', ')}`);
  }
  // The extra checklist belongs to its own gate. Letting it ride along on another
  // profile would publish its blockers under that profile's marker, where the extra
  // profile's re-review delta can never find them.
  if (EXTRA_CONCERN !== null && requested.includes(EXTRA_CONCERN) && profile !== EXTRA_CONCERN) {
    throw new Error(`The ${EXTRA_CONCERN} concern only runs under --profile ${EXTRA_CONCERN} (got --profile ${profile}).`);
  }
  if (requested.length === 0) {
    throw new Error('--concerns was given but resolved to an empty list.');
  }
  return requested;
}

// A concern report is only usable when it carries the two fields the merge
// depends on. Anything else is recorded as a skip with the reason.
function loadConcernReport(reviewId, profile, concern) {
  const path = join(INPUT_DIR, `${reviewId}.${profile}.report.${concern}.json`);
  let raw;
  try {
    raw = readFileSync(path, 'utf-8');
  } catch (err) {
    return { ok: false, concern, path, reason: 'report-missing', detail: err.message };
  }

  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    return { ok: false, concern, path, reason: 'report-unparseable', detail: err.message };
  }

  if (!Array.isArray(parsed.comments)) {
    return {
      ok: false,
      concern,
      path,
      reason: 'comments-not-an-array',
      detail: 'report JSON must carry an array `comments` field',
    };
  }

  const verdict = typeof parsed.verdict === 'string' ? parsed.verdict : null;
  if (verdict === null) {
    return {
      ok: false,
      concern,
      path,
      reason: 'verdict-missing',
      detail: 'report JSON must carry a string `verdict` field',
    };
  }

  return { ok: true, concern, path, report: parsed };
}

// The extra concern's checklist row, a positive integer; anything else is a reviewer
// typo and is dropped rather than published as a wrong citation. Read only from the
// extra concern's comments: no code concern carries a checklist row.
function ruleOf(comment, concern) {
  if (EXTRA_CONCERN === null || concern !== EXTRA_CONCERN) {
    return null;
  }
  const rule = comment.rule;
  if (Number.isInteger(rule) && rule >= 1) {
    return rule;
  }
  return null;
}

// The extra concern's restatement of a finding for a reader who does not program.
function audienceNoteOf(comment, concern) {
  if (EXTRA_CONCERN === null || concern !== EXTRA_CONCERN) {
    return null;
  }
  if (typeof comment.audienceNote !== 'string') {
    return null;
  }
  const note = comment.audienceNote.trim();
  return note === '' ? null : note;
}

// The first line of a range comment: an integer below `line`, else undefined.
// verify-report.mjs and post-comments.mjs decide whether the range is anchorable.
function rangeStartOf(comment) {
  const isRange = Number.isInteger(comment.line) && Number.isInteger(comment.startLine)
    && comment.startLine < comment.line;
  return isRange ? comment.startLine : undefined;
}

// Two concerns on the same line keep the wider range, so neither loses its span.
function widerRangeStart(current, candidate) {
  if (current === undefined) {
    return candidate;
  }
  if (candidate === undefined) {
    return current;
  }
  return Math.min(current, candidate);
}

// parentId + path + line is the merge key. Two reviewers flagging the same
// location produce ONE comment carrying both concern labels. Wording differences
// do not prevent the merge.
//
// `parentId` is part of the key: a reply into an existing thread and a fresh
// finding at the same path:line are different comments.
//
// An extra-concern finding also keys on its checklist row, since rows about
// something missing anchor file-level on the same file.
function commentKey(comment, concern) {
  const parent = Number.isInteger(comment.parentId) ? String(comment.parentId) : '';
  const path = (typeof comment.path === 'string' && comment.path.length > 0) ? comment.path : '';
  const line = Number.isInteger(comment.line) ? String(comment.line) : '';
  const base = `${parent}::${path}::${line}`;
  const rule = ruleOf(comment, concern);
  return rule === null ? base : `${base}::rule-${rule}`;
}

function labelFor(concern) {
  const label = CONCERN_LABELS[concern];
  if (label === undefined) {
    return concern;
  }
  return label;
}

// A comment body opens with its kind marker: **`issue`** <title>. The concern goes
// INSIDE that marker rather than in front of it, so the rendered line is
//   **`issue (seguridad)`** Riesgo de inyeccion SQL...
// instead of two stacked labels.
//
// Falls back to prefixing when the body does not open with a marker.
const KIND_MARKER = /^\*\*`([a-z]+)`\*\*/;

function labelBody(body, concern) {
  const text = String(body ?? '').trim();
  const label = labelFor(concern);
  if (KIND_MARKER.test(text)) {
    return text.replace(KIND_MARKER, (_match, kind) => `**\`${kind} (${label})\`**`);
  }
  return `**[${label}]** ${text}`;
}

function higherSeverity(a, b) {
  const rankA = SEVERITY_RANK[a] ?? 0;
  const rankB = SEVERITY_RANK[b] ?? 0;
  if (rankB > rankA) {
    return b;
  }
  return a;
}

// Evidence candidates of one merged location, most severe contributor first, with
// exact duplicates collapsed. A single candidate stays a plain string, which is the
// shape every reviewer writes and every verifier already accepts.
function evidenceOf(candidates) {
  const ordered = candidates
    .map((candidate, index) => ({ ...candidate, index }))
    .sort((x, y) => (SEVERITY_RANK[y.severity] - SEVERITY_RANK[x.severity]) || (x.index - y.index));
  const distinct = [];
  for (const { evidence } of ordered) {
    if (typeof evidence === 'string' && !distinct.includes(evidence)) {
      distinct.push(evidence);
    }
  }
  if (distinct.length === 0) {
    return undefined;
  }
  return distinct.length === 1 ? distinct[0] : distinct;
}

function mergeComments(loadedReports, severityCoerced) {
  const byKey = new Map();

  for (const loaded of loadedReports) {
    const concern = loaded.concern;
    for (const comment of loaded.report.comments) {
      // The single general comment is this script's job; an agent's general comment
      // is dropped.
      //
      // A REPLY is the one unanchored shape that survives: it carries `parentId`
      // and lands inside an already-anchored thread.
      const hasPath = typeof comment.path === 'string' && comment.path.length > 0;
      const parentId = Number.isInteger(comment.parentId) && comment.parentId > 0 ? comment.parentId : null;
      if (!hasPath && parentId === null) {
        continue;
      }

      const key = commentKey(comment, concern);
      const labelled = labelBody(comment.body, concern);
      const existing = byKey.get(key);
      // Canonical tier; an unreadable or missing one is a blocker, never a silent
      // warning (lib/general-comment.mjs, normalizeSeverity).
      const normalized = normalizeSeverity(comment.severity);
      if (normalized.coerced) {
        severityCoerced.push({ concern, path: hasPath ? comment.path : null, parentId, original: normalized.original });
      }
      const severity = normalized.severity;

      if (existing === undefined) {
        byKey.set(key, {
          parentId,
          path: hasPath ? comment.path : undefined,
          line: Number.isInteger(comment.line) ? comment.line : undefined,
          startLine: rangeStartOf(comment),
          severity,
          body: labelled,
          // Verbatim. Never normalised, never rebuilt - see the header note.
          evidenceCandidates: [{ evidence: comment.evidence, severity }],
          concerns: [concern],
          // Extra concern only: the checklist row and the non-technical note.
          rule: ruleOf(comment, concern),
          audienceNote: audienceNoteOf(comment, concern),
          resolvesThread: comment.resolvesThread === true,
        });
        continue;
      }

      // A merged reply closes its thread only when every concern that answered in
      // it verified the fix: one concern still holding it open keeps it open.
      existing.resolvesThread = existing.resolvesThread && comment.resolvesThread === true;
      existing.severity = higherSeverity(existing.severity, severity);
      existing.body = `${existing.body}\n\n---\n\n${labelled}`;
      existing.concerns.push(concern);
      existing.startLine = widerRangeStart(existing.startLine, rangeStartOf(comment));
      existing.evidenceCandidates.push({ evidence: comment.evidence, severity });
      // A code concern folded onto an extra-concern finding carries no note of its
      // own; the first one found is kept rather than lost to the fold.
      if (existing.rule === null) {
        existing.rule = ruleOf(comment, concern);
      }
      if (existing.audienceNote === null) {
        existing.audienceNote = audienceNoteOf(comment, concern);
      }
      // Every contributor's evidence stays a candidate, verbatim; verify-report.mjs
      // keeps the comment if any candidate grounds.
    }
  }

  const merged = [];
  for (const entry of byKey.values()) {
    const comment = {
      severity: entry.severity,
      body: entry.body,
      concerns: entry.concerns,
    };
    const evidence = evidenceOf(entry.evidenceCandidates);
    if (evidence !== undefined) {
      comment.evidence = evidence;
    }
    // Built field by field rather than spread, so a `verifierMeta` planted in a
    // concern report is never carried through. `parentId` and `path` are set only
    // when real: an undefined `path` on a reply must not become a null anchor.
    if (entry.parentId !== null) {
      comment.parentId = entry.parentId;
    }
    if (entry.path !== undefined) {
      comment.path = entry.path;
    }
    if (entry.line !== undefined) {
      comment.line = entry.line;
    }
    if (entry.line !== undefined && entry.startLine !== undefined) {
      comment.startLine = entry.startLine;
    }
    if (entry.rule !== null) {
      comment.rule = entry.rule;
    }
    if (entry.audienceNote !== null) {
      comment.audienceNote = entry.audienceNote;
    }
    // verify-report.mjs decides whether the flag is valid on this comment.
    if (entry.resolvesThread) {
      comment.resolvesThread = true;
    }
    merged.push(comment);
  }
  return merged;
}



async function main() {
  rejectUnknownFlags('merge-reports.mjs', usage(), { valueFlags: ['--profile', '--concerns'] });
  const reviewId = parseReviewId(process.argv[2]);
  if (reviewId === null) {
    process.stderr.write(`${usage()}\n`);
    process.exit(2);
  }

  const profile = parseProfile();

  let concerns;
  const concernsFlagIndex = process.argv.indexOf('--concerns');
  if (concernsFlagIndex === -1) {
    concerns = parseConcernList(null, profile);
  } else {
    const value = process.argv[concernsFlagIndex + 1];
    // A flag with no value is a dispatcher bug, not a request for the default list:
    // falling back would announce intentionally skipped concerns as NO ejecutadas.
    if (value === undefined || value.startsWith('--') || value.trim() === '') {
      throw new Error('--concerns requires a comma-separated list of concerns.');
    }
    concerns = parseConcernList(value, profile);
  }

  const loaded = [];
  const skipped = [];
  for (const concern of concerns) {
    const result = loadConcernReport(reviewId, profile, concern);
    if (result.ok) {
      loaded.push(result);
    } else {
      skipped.push(result);
    }
  }

  if (loaded.length === 0) {
    process.stderr.write(
      `merge-reports.mjs: no usable per-concern report found for review ${reviewId}. `
      + `Expected at least one of ${concerns.map((c) => `${reviewId}.${profile}.report.${c}.json`).join(', ')} under ${INPUT_DIR}. `
      + 'Nothing was merged - do NOT read this as a clean review.\n',
    );
    process.stdout.write(JSON.stringify({
      reviewId,
      concernsRequested: concerns,
      concernsRun: [],
      concernsSkipped: skipped.map((s) => ({ concern: s.concern, reason: s.reason, detail: s.detail })),
      merged: 0,
      verdict: null,
      usable: false,
    }, null, 2) + '\n');
    process.exitCode = 2;
    return;
  }

  const severityCoerced = [];
  const mergedComments = mergeComments(loaded, severityCoerced);
  // The general comment is rebuilt by verify-report.mjs from the surviving comments,
  // so everything it needs beyond those comments is persisted alongside them.
  const generalData = {
    ran: loaded.map((l) => labelFor(l.concern)),
    notRun: skipped.map((s) => `${labelFor(s.concern)} (${s.reason})`),
    summaries: loaded.map((l) => ({
      concern: labelFor(l.concern),
      summary: typeof l.report.summary === 'string' ? l.report.summary.trim() : '',
    })),
    concernVerdicts: loaded.map((l) => normalizeConcernVerdict(l.report)),
  };
  const verdict = verdictFor(mergedComments);
  const general = buildGeneralComment({ ...generalData, verdict, comments: mergedComments, profile });

  // The extra concern's non-technical summary, for the dispatcher's own presentation.
  // Copied explicitly because the merged report is built field by field; what gets
  // published is rebuilt from the surviving notes, never from this summary.
  const extraReport = EXTRA_CONCERN === null ? undefined : loaded.find((l) => l.concern === EXTRA_CONCERN);
  const audienceSummary = (extraReport !== undefined && typeof extraReport.report.audienceSummary === 'string')
    ? extraReport.report.audienceSummary.trim()
    : '';

  const modes = Array.from(new Set(loaded.map((l) => l.report.mode).filter((m) => typeof m === 'string')));
  const mergedReport = {
    reviewId,
    profile,
    prId: reviewId === 'working' ? null : Number(reviewId),
    mode: modes.length === 1 ? modes[0] : 'mixed',
    verdict,
    summary: loaded
      .map((l) => `${labelFor(l.concern)}: ${String(l.report.summary ?? '').trim()}`)
      .filter((s) => !s.endsWith(': '))
      .join(' | '),
    // The general comment goes FIRST so it is the first thing posted and the
    // first thing read in the verified report.
    comments: [general, ...mergedComments],
    general: generalData,
    mergeMeta: {
      concernsRequested: concerns,
      concernsRun: loaded.map((l) => l.concern),
      concernsSkipped: skipped.map((s) => ({ concern: s.concern, reason: s.reason, detail: s.detail })),
      perConcernVerdicts: Object.fromEntries(loaded.map((l) => [l.concern, l.report.verdict])),
      mergedFrom: loaded.reduce((total, l) => total + l.report.comments.length, 0),
      mergedInto: mergedComments.length,
      severityCoerced,
    },
    ...(audienceSummary === '' ? {} : { audienceSummary }),
  };

  const outPath = join(INPUT_DIR, `${reviewId}.${profile}.report.json`);
  writeFileSync(outPath, JSON.stringify(mergedReport, null, 2));

  process.stdout.write(JSON.stringify({
    reviewId,
    profile,
    verdict,
    concernsRequested: concerns,
    concernsRun: mergedReport.mergeMeta.concernsRun,
    concernsSkipped: mergedReport.mergeMeta.concernsSkipped,
    perConcernVerdicts: mergedReport.mergeMeta.perConcernVerdicts,
    mergedFrom: mergedReport.mergeMeta.mergedFrom,
    mergedInto: mergedReport.mergeMeta.mergedInto,
    severityCoerced,
    reportPath: outPath,
    usable: true,
  }, null, 2) + '\n');
}

main().catch((err) => {
  process.stderr.write(`merge-reports.mjs failed: ${err.message}\n`);
  process.exitCode = 1;
});
