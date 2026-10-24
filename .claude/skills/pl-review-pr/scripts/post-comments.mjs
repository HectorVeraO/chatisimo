#!/usr/bin/env node
// Publishes a verified review to a GitHub pull request: thread replies first, then
// one review submission carrying the general comment as its body, every anchorable
// finding as an inline comment, and the verdict as its event.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import {
  ghApi,
  listPrComments,
  listReviewThreads,
  parsePrId,
  repoPath,
  resolveReviewThread,
} from './lib/gh.mjs';
import { formatFindingBody, isFinding, isGeneral, normalizeSeverity } from './lib/general-comment.mjs';
import { parseProfileArg, profilesFor, resolveExtraProfile } from './lib/extra-profile.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

// The bot marker. `MARKER_PREFIX` is the part that must never change: the
// re-review detection of tooling that batches reviews across PRs matches it literally to tell an
// already-reviewed PR from a new one, and PRs reviewed before profiles existed
// carry the bare `**[review-pr ·` form. Matching on the prefix recognises both.
//
// The profile is appended so a `default` review and a `full` review on the same PR
// are two distinguishable reviews rather than one overwriting the other's marker.
const MARKER_PREFIX = '**[review-pr';

// Machine-readable stamp of the commit this review actually looked at.
// rereview-delta.mjs reads it back on the next run to compute the delta, so it
// must survive on the PR rather than on disk: the dispatcher's pre-flight wipes
// local artifacts, and a review may have been produced on another machine.
function headStamp(sha) {
  return typeof sha === 'string' && sha.length >= 7
    ? `\n\n<!-- review-pr:head=${sha} -->`
    : '';
}

// The profile list lives in lib/extra-profile.mjs, shared with merge-reports.mjs,
// verify-report.mjs and rereview-delta.mjs. The extra profile, when enabled,
// publishes under `**[review-pr:<name>`, the marker pl-address-review reads to
// switch into its non-technical vocabulary mode.
const EXTRA = resolveExtraProfile();
const PROFILES = profilesFor(EXTRA);

// Which review profile produced this report. Passed by the dispatcher; defaults
// to `default` for a manual run without the flag.
//
// Resolved ONCE at module load, so an invalid flag fails before any network call.
const REVIEW_PROFILE = parseProfileArg(process.argv, EXTRA);

function reviewProfile() {
  return REVIEW_PROFILE;
}

// CAREFUL: re-review detection in tooling that batches reviews across PRs matches this exact
// string, anchored at offset 0, to tell an already-reviewed PR from a new one.
// Change the wording only together with that tooling.
function header() {
  return `${MARKER_PREFIX}:${reviewProfile()} · agente Claude Code]**`;
}

// The author shown in the footer. Per-developer: read from REVIEW_PR_AUTHOR
// (set in .claude/settings.local.json, gitignored). Falls back to the login of
// the account the token belongs to, then to a neutral label.
function reviewAuthor(login) {
  const handle = process.env.REVIEW_PR_AUTHOR;
  if (handle && handle.trim() !== '') {
    return handle.trim();
  }
  if (typeof login === 'string' && login !== '') {
    return login;
  }
  return 'el revisor configurado';
}

// The model that produced the review, for the AI-generation disclosure. The
// dispatcher knows which model it actually resolved for THIS run, so it passes
// --model; REVIEW_PR_MODEL is the per-developer fallback for a manual run.
//
// When neither is set the parenthetical is omitted entirely, never guessed.
function reviewModel() {
  const flagIndex = process.argv.indexOf('--model');
  if (flagIndex !== -1) {
    const value = process.argv[flagIndex + 1];
    if (value !== undefined && value.trim() !== '' && !value.startsWith('--')) {
      return value.trim();
    }
  }
  const fromEnv = process.env.REVIEW_PR_MODEL;
  if (fromEnv && fromEnv.trim() !== '') {
    return fromEnv.trim();
  }
  return null;
}

// AI-generation disclosure. Every published comment carries it: the tool that
// produced it, the model when known, and the account it was posted from (GitHub
// attributes the comment to whoever owns the token).
// CAREFUL: the classifier also matches the `No representa una revisión humana`
// line of this footer - see the note on `header()`. The attribution line above it
// varies per developer and per model and is NOT matched, so it is free to change.
function footer(login) {
  const model = reviewModel();
  const invocation = `/pl-review-pr --profile ${reviewProfile()}`;
  const attribution = model === null
    ? `\`${invocation}\``
    : `\`${invocation}\` (${model})`;
  return [
    '---',
    `_Observación generada por ${attribution} desde la cuenta de ${reviewAuthor(login)}.`,
    'No representa una revisión humana. Responde al desarrollador para discusión._',
  ].join('\n');
}

// The body is laid out as a list first (`formatFindingBody`), so a reviewer that
// skipped the blank line before the bullets still renders as one.
function wrap(body, sha, login) {
  const trimmed = String(formatFindingBody(body) ?? '').trim();
  return `${header()}\n\n${trimmed}\n\n${footer(login)}${headStamp(sha)}`;
}

// The footer's attribution line names the model and the posting account, which
// vary per run and per developer. Compared verbatim, a re-run with another
// `--model` or REVIEW_PR_AUTHOR would re-post every comment it already published.
const ATTRIBUTION_LINE = /^_Observación generada por .*$/m;

// Both sides go through `formatFindingBody`, so a comment posted before the
// layout was normalized still dedups against its normalized re-run.
function normalize(text) {
  const flat = String(formatFindingBody(String(text ?? '').replace(/\r\n/g, '\n'))).trim();
  return flat.replace(ATTRIBUTION_LINE, '_Observacion generada por <attribution>');
}

function isTopLevelKind(existing) {
  return existing.kind === 'review' || existing.kind === 'general';
}

/**
 * True when an existing PR comment (lib/gh.mjs normalized shape) already carries
 * this wrapped body at the same place: the same thread root for a reply, the same
 * path and line for an inline comment, a review body or conversation comment for
 * the general comment. The footer's attribution line is ignored, so a re-run with
 * another model or author does not re-post.
 */
export function isDuplicate(wrappedBody, existing, path, line, parentId) {
  if (!existing || typeof existing.body !== 'string') {
    return false;
  }
  if (normalize(existing.body) !== normalize(wrappedBody)) {
    return false;
  }

  const existingParentId = Number.isInteger(existing.parentId) ? existing.parentId : null;
  if (parentId !== null) {
    return existing.kind === 'inline' && existingParentId === parentId;
  }
  if (existingParentId !== null) {
    return false;
  }
  if (path === null && line === null) {
    return isTopLevelKind(existing);
  }
  if (existing.kind !== 'inline' || existing.path !== path) {
    return false;
  }
  if (line !== null && existing.line !== line && existing.originalLine !== line) {
    return false;
  }
  return true;
}

// Severity tally of the report being published. The reviewer's own tiers are the
// source of truth; nothing here re-judges a finding.
function countSeverities(report) {
  let blockers = 0;
  let warnings = 0;
  for (const comment of report.comments) {
    // The general comment inherits the report's verdict as its severity; it is not a finding.
    if (isGeneral(comment)) {
      continue;
    }
    // Canonicalised the same way the verifier does it, so an unreadable severity on
    // a hand-built report still counts as a blocker and the gate fails closed.
    const severity = normalizeSeverity(comment.severity).severity;
    if (severity === 'blocker') {
      blockers += 1;
      continue;
    }
    // A warning, or an `info` that is not a reply (`info` is valid on replies only),
    // is pending: it stops the approval like a blocker.
    if (isFinding(comment)) {
      warnings += 1;
    }
  }
  return { blockers, warnings };
}

// Which review state this report asks for. The decision is deterministic and the
// same for every profile: zero pending findings approves, any pending finding
// (blocker OR warning) requests changes. Severity only measures how much a finding
// matters, never whether it must be resolved. The tally is the only input; the
// verdict must merely be one verify-report.mjs writes, so a missing or unknown
// verdict (a report that never passed the verifier's shape) requests changes: the
// gate fails closed.
//
// The marker below is matched literally by the review-open-prs sweep to tell an
// engine with this policy from an older one; change it only with that skill.
// review-policy: deterministic-v2
const KNOWN_VERDICTS = new Set(['approve', 'comment', 'block']);

/**
 * The review state a verified report asks for, with its severity tally.
 */
export function resolveReviewAction(report) {
  const { blockers, warnings } = countSeverities(report);
  const approvable = blockers === 0 && warnings === 0 && KNOWN_VERDICTS.has(report.verdict);
  const resolution = {
    action: approvable ? 'approve' : 'request_changes',
    blockers,
    warnings,
  };
  return resolution;
}

/**
 * The GitHub review event for an action. GitHub refuses APPROVE and
 * REQUEST_CHANGES from the PR's own author, so an own PR gets COMMENT and the
 * verdict travels in the body (`ownPrVerdictLine`).
 */
export function reviewEvent(action, ownPr) {
  if (ownPr) {
    return 'COMMENT';
  }
  return action === 'approve' ? 'APPROVE' : 'REQUEST_CHANGES';
}

/**
 * The verdict line appended to the body of a review on the reviewer's own PR.
 */
export function ownPrVerdictLine(action) {
  const verdict = action === 'approve' ? 'APPROVE' : 'REQUEST_CHANGES';
  return `**Veredicto: ${verdict}** (publicado como COMMENT: GitHub no permite aprobar ni pedir cambios en un PR propio).`;
}

// Upper bound on the parent chain walked to find a thread root, so a malformed
// (cyclic) parent chain from the API cannot loop forever.
const MAX_THREAD_DEPTH = 100;

/**
 * True for a reply that asks to close its thread: severity `info`, a positive
 * integer `parentId` and `resolvesThread: true`. verify-report.mjs strips the flag
 * from any other comment; this re-checks it so a hand-built report fails closed.
 */
export function asksToResolveThread(comment) {
  if (!comment || comment.resolvesThread !== true) {
    return false;
  }
  if (!Number.isInteger(comment.parentId) || comment.parentId <= 0) {
    return false;
  }
  return normalizeSeverity(comment.severity).severity === 'info';
}

/**
 * The top-level review comment of the thread `commentId` belongs to, walking
 * `parentId` through the PR's existing comments. GitHub accepts a reply only to a
 * thread's top-level comment, and a re-review often answers the developer's reply.
 * When a parent is not in `existingValues`, the last id reached is returned.
 */
export function threadRootId(commentId, existingValues) {
  const byId = new Map();
  for (const existing of existingValues ?? []) {
    if (existing && existing.kind === 'inline' && Number.isInteger(existing.id)) {
      byId.set(existing.id, existing);
    }
  }
  let current = commentId;
  for (let depth = 0; depth < MAX_THREAD_DEPTH; depth += 1) {
    const parentId = byId.get(current)?.parentId;
    if (!Number.isInteger(parentId)) {
      return current;
    }
    current = parentId;
  }
  return current;
}

/**
 * Which threads to resolve for the replies in `comments` that asked for it, given
 * the PR's existing comments and its review threads (lib/gh.mjs
 * `listReviewThreads`). One entry per distinct root, in report order: `resolve`
 * holds `{ root, threadId }` for the threads still open, `alreadyResolved` the
 * roots of resolved ones, `unknown` the roots no thread holds.
 */
export function planThreadResolutions(comments, existingValues, threads) {
  const resolve = [];
  const alreadyResolved = [];
  const unknown = [];
  const seen = new Set();
  for (const comment of comments ?? []) {
    if (!asksToResolveThread(comment)) {
      continue;
    }
    const root = threadRootId(comment.parentId, existingValues);
    if (seen.has(root)) {
      continue;
    }
    seen.add(root);
    const thread = (threads ?? []).find((t) => t.commentIds.includes(root));
    if (thread === undefined) {
      unknown.push(root);
    } else if (thread.isResolved) {
      alreadyResolved.push(root);
    } else {
      resolve.push({ root, threadId: thread.threadId });
    }
  }
  const plan = { resolve, alreadyResolved, unknown };
  return plan;
}

function isAnchorable(anchorableRanges, path, line) {
  if (!anchorableRanges || typeof anchorableRanges !== 'object') {
    return true;
  }
  const ranges = anchorableRanges[path];
  if (!Array.isArray(ranges)) {
    return false;
  }
  return ranges.some(([start, end]) => line >= start && line <= end);
}

function sameRange(anchorableRanges, path, startLine, line) {
  if (!anchorableRanges || typeof anchorableRanges !== 'object') {
    return false;
  }
  const ranges = anchorableRanges[path] ?? [];
  return ranges.some(([start, end]) => startLine >= start && line <= end);
}

/**
 * Splits a verified report's comments the way GitHub can take them: `replies`
 * (a `parentId`), `inline` (a path and a line GitHub can anchor), `inBody` (a
 * file-level finding or a line outside the anchorable ranges, published inside
 * the review body), and the `general` comment.
 */
export function planReview(comments, anchorableRanges) {
  const plan = { general: null, replies: [], inline: [], inBody: [] };
  for (const comment of comments ?? []) {
    if (isGeneral(comment)) {
      plan.general = comment;
      continue;
    }
    if (Number.isInteger(comment.parentId) && comment.parentId > 0) {
      plan.replies.push(comment);
      continue;
    }
    const hasPath = typeof comment.path === 'string' && comment.path.length > 0;
    const hasLine = Number.isInteger(comment.line);
    if (hasPath && hasLine && isAnchorable(anchorableRanges, comment.path, comment.line)) {
      plan.inline.push(comment);
      continue;
    }
    plan.inBody.push(comment);
  }
  return plan;
}

function inBodySection(comments) {
  if (comments.length === 0) {
    return '';
  }
  const blocks = comments.map((c) => {
    const where = typeof c.path === 'string'
      ? `\`${c.path}${Number.isInteger(c.line) ? `:${c.line}` : ''}\``
      : '(sin ubicacion)';
    return `${where}\n\n${String(formatFindingBody(c.body) ?? '').trim()}`;
  });
  const section = `**Observaciones sin linea anclable en el diff de GitHub:**\n\n${blocks.join('\n\n---\n\n')}`;
  return section;
}

/**
 * The review body before wrapping: the general comment, the findings GitHub cannot
 * anchor, and, on an own PR, the verdict line.
 */
export function composeReviewBody(plan, action, ownPr) {
  const parts = [String(plan.general?.body ?? '').trim()];
  const section = inBodySection(plan.inBody);
  if (section !== '') {
    parts.push(section);
  }
  if (ownPr) {
    parts.push(ownPrVerdictLine(action));
  }
  const body = parts.filter((p) => p !== '').join('\n\n');
  return body;
}

function inlinePayload(comment, anchorableRanges, login) {
  const payload = {
    path: comment.path,
    line: comment.line,
    side: 'RIGHT',
    body: wrap(comment.body, null, login),
  };
  // A range comment is optional: kept only when both ends sit in one hunk.
  const startLine = comment.startLine;
  const hasRange = Number.isInteger(startLine) && startLine < comment.line
    && sameRange(anchorableRanges, comment.path, startLine, comment.line);
  if (hasRange) {
    payload.start_line = startLine;
    payload.start_side = 'RIGHT';
  }
  return payload;
}

// Refuses a report that fails the offline gates, before any network call: only a
// report verify-report.mjs wrote (its sole writer of `verifierMeta`) for THIS PR
// and profile may be published. Every mismatch is collected and reported at once.
//
// merge-reports.mjs builds its output object field by field rather than spreading
// the concern reports, so a `verifierMeta` planted in one is not carried through.
// Do not "simplify" that merge to a spread.
function assertPublishable(report, reportPath, prId) {
  const meta = report.verifierMeta;
  if (!meta || typeof meta !== 'object' || typeof meta.verifiedAt !== 'string' || meta.verifiedAt === '') {
    throw new Error(
      `Refusing to publish: ${reportPath} carries no verifierMeta, so it has not passed verify-report.mjs. `
      + 'Run `node <scripts>/verify-report.mjs <pr-id> --profile <profile>` and publish its output '
      + '(.me/review-pr/<pr-id>.<profile>.report.verified.json) instead. '
      + 'Never publish <pr-id>.<profile>.report.json.',
    );
  }
  const mismatches = [];
  if (meta.prId !== prId) {
    mismatches.push(`the report was verified for PR ${JSON.stringify(meta.prId ?? null)}, not PR ${prId}`);
  }
  if (meta.profile !== REVIEW_PROFILE) {
    mismatches.push(`the report was verified under profile ${JSON.stringify(meta.profile ?? null)}, not --profile ${REVIEW_PROFILE}`);
  }
  if (mismatches.length > 0) {
    throw new Error(`Refusing to publish ${reportPath}: ${mismatches.join('; ')}. Re-run verify-report.mjs for this PR and profile.`);
  }
  return meta;
}

// The comment already on the PR that carries this wrapped body at this place, or
// null. Called by ghApi before it retries a write that failed ambiguously (5xx or
// timeout): GitHub may have stored it anyway, and posting again would duplicate it.
// A failed listing throws, so the write is reported as failed instead of sent blind.
async function findLanded(repo, prId, wrapped, path, line, parentId) {
  const fresh = await listPrComments(repo, prId);
  const landed = fresh.comments.find((existing) => isDuplicate(wrapped, existing, path, line, parentId));
  return landed ?? null;
}

// Posts every reply into its thread. A reply that is already on the PR is skipped
// but still counts as on the PR for thread resolution, so a retry converges.
async function postReplies(ctx, replies) {
  const outcome = { posted: 0, skipped: 0, repliedThreads: [], closingReplies: [], errors: [] };
  for (const c of replies) {
    const root = threadRootId(c.parentId, ctx.existing);
    const wrapped = wrap(c.body, null, ctx.login);
    const duplicate = ctx.existing.some((existing) => isDuplicate(wrapped, existing, null, null, root));
    if (duplicate) {
      outcome.skipped += 1;
      if (asksToResolveThread(c)) {
        outcome.closingReplies.push(c);
      }
      continue;
    }
    try {
      await ghApi(`repos/${ctx.repo}/pulls/${ctx.prId}/comments/${root}/replies`, {
        method: 'POST',
        body: { body: wrapped },
        alreadyApplied: () => findLanded(ctx.repo, ctx.prId, wrapped, null, null, root),
      });
      outcome.posted += 1;
      outcome.repliedThreads.push(root);
      if (asksToResolveThread(c)) {
        outcome.closingReplies.push(c);
      }
    } catch (err) {
      outcome.errors.push({ comment: { parentId: c.parentId, severity: c.severity }, error: err.message });
    }
  }
  return outcome;
}

// Resolves each planned thread. A failure is reported and never fails the run:
// the reply that closes the thread is already on the PR.
async function resolveThreads(ctx, closingReplies) {
  const outcome = { resolvedThreads: [], alreadyResolvedThreads: [], resolveErrors: [] };
  const wanted = closingReplies.filter(asksToResolveThread);
  if (wanted.length === 0) {
    return outcome;
  }
  let threads;
  try {
    threads = (await listReviewThreads(ctx.repo, ctx.prId)).threads;
  } catch (err) {
    for (const c of wanted) {
      outcome.resolveErrors.push({ thread: threadRootId(c.parentId, ctx.existing), error: err.message });
    }
    return outcome;
  }
  const plan = planThreadResolutions(wanted, ctx.existing, threads);
  outcome.alreadyResolvedThreads.push(...plan.alreadyResolved);
  for (const root of plan.unknown) {
    outcome.resolveErrors.push({ thread: root, error: `no review thread holds comment ${root}` });
  }
  for (const { root, threadId } of plan.resolve) {
    try {
      await resolveReviewThread(threadId);
      outcome.resolvedThreads.push(root);
    } catch (err) {
      outcome.resolveErrors.push({ thread: root, error: err.message });
    }
  }
  return outcome;
}

// The review this login already submitted with this body, or null.
async function findLandedReview(ctx, wrappedBody) {
  const fresh = await listPrComments(ctx.repo, ctx.prId);
  const landed = fresh.comments.find((existing) => existing.kind === 'review'
    && existing.author === ctx.login
    && isDuplicate(wrappedBody, existing, null, null, null));
  return landed ?? null;
}

// Submits the one review: body, inline comments, event. Skipped when the body and
// every inline comment are already on the PR, so a re-run never duplicates it.
async function submitReview(ctx, report, plan, failClosedReason) {
  const resolved = resolveReviewAction(report);
  const action = failClosedReason === null ? resolved.action : 'request_changes';
  const event = reviewEvent(action, ctx.ownPr);
  const wrappedBody = wrap(composeReviewBody(plan, action, ctx.ownPr), ctx.headSha, ctx.login);
  const newInline = plan.inline.filter((c) => {
    const wrapped = wrap(c.body, null, ctx.login);
    return !ctx.existing.some((existing) => isDuplicate(wrapped, existing, c.path, c.line, null));
  });
  const bodyOnPr = ctx.existing.some((existing) => isDuplicate(wrappedBody, existing, null, null, null));
  const review = {
    event,
    action,
    ownPr: ctx.ownPr,
    blockers: resolved.blockers,
    warnings: resolved.warnings,
    inlineComments: newInline.length,
    inBody: plan.inBody.length,
    submitted: false,
    ok: true,
  };
  if (failClosedReason !== null) {
    review.reason = failClosedReason;
  }
  const skippedInline = plan.inline.length - newInline.length;
  if (newInline.length === 0 && bodyOnPr) {
    const duplicate = 'duplicate: the review body and every inline comment are already on the PR';
    review.reason = failClosedReason === null ? duplicate : `${failClosedReason}; ${duplicate}`;
    return { review, posted: 0, skipped: skippedInline + 1 };
  }
  const payload = {
    body: wrappedBody,
    event,
    comments: newInline.map((c) => inlinePayload(c, ctx.anchorableRanges, ctx.login)),
  };
  if (ctx.headSha !== null) {
    payload.commit_id = ctx.headSha;
  }
  try {
    const created = await ghApi(`repos/${ctx.repo}/pulls/${ctx.prId}/reviews`, {
      method: 'POST',
      body: payload,
      alreadyApplied: () => findLandedReview(ctx, wrappedBody),
    });
    review.submitted = true;
    review.id = created && Number.isInteger(created.id) ? created.id : null;
    return { review, posted: newInline.length + 1, skipped: skippedInline };
  } catch (err) {
    review.ok = false;
    review.error = err.message;
    return { review, posted: 0, skipped: skippedInline };
  }
}

// The PR and the account the token belongs to. The head the report was verified at
// must still be the PR's head: inline comments anchor to that commit's diff.
async function loadContext(repo, prId, meta) {
  const pull = await ghApi(`repos/${repo}/pulls/${prId}`);
  const me = await ghApi('user');
  const prHead = typeof pull?.head?.sha === 'string' ? pull.head.sha : null;
  const verifiedHead = typeof meta.headSha === 'string' && meta.headSha !== '' ? meta.headSha : null;
  if (verifiedHead !== null && prHead !== null && prHead !== verifiedHead) {
    throw new Error(
      `Refusing to publish: PR ${prId} head is ${prHead}, but the report was verified at ${verifiedHead}. `
      + 'Re-fetch the PR and run the review again.',
    );
  }
  const login = typeof me?.login === 'string' ? me.login : null;
  const author = typeof pull?.user?.login === 'string' ? pull.user.login : null;
  const listed = await listPrComments(repo, prId);
  const ctx = {
    repo,
    prId,
    login,
    ownPr: login !== null && login === author,
    headSha: verifiedHead,
    anchorableRanges: meta.anchorableRanges ?? null,
    existing: listed.comments,
    truncated: listed.truncated,
  };
  return ctx;
}

async function main() {
  const usage = `Usage: post-comments.mjs <pr-number|pr-url> <report.verified.json> [--profile ${PROFILES.join('|')}] [--model <name>]`;
  rejectUnknownFlags('post-comments.mjs', usage, { valueFlags: ['--profile', '--model'] });
  const prArg = process.argv[2];
  const reportPath = process.argv[3];
  const prId = parsePrId(prArg);
  if (prId === null || !reportPath) {
    process.stderr.write(`${usage}\n`);
    process.exit(2);
  }

  const report = JSON.parse(readFileSync(reportPath, 'utf-8'));
  if (!Array.isArray(report.comments)) {
    throw new Error('Report JSON must have an array `comments` field.');
  }
  const meta = assertPublishable(report, reportPath, prId);

  const repo = repoPath();
  const ctx = await loadContext(repo, prId, meta);
  const plan = planReview(report.comments, ctx.anchorableRanges);

  const replies = await postReplies(ctx, plan.replies);
  // Only a reply that is actually on the PR closes its thread; a failed reply
  // leaves the thread open, never resolved without its explanation.
  const threads = await resolveThreads(ctx, replies.closingReplies);

  // A finding that did not reach the PR must never be approved past: a failed
  // reply turns the verdict into request-changes, whatever the tally said.
  const failClosedReason = replies.errors.length > 0
    ? `fail-closed: ${replies.errors.length} reply(ies) failed to post`
    : null;
  const submission = await submitReview(ctx, report, plan, failClosedReason);

  const errors = [...replies.errors];
  if (!submission.review.ok) {
    errors.push({ comment: { review: true }, error: submission.review.error });
  }
  const summary = {
    prId,
    posted: replies.posted + submission.posted,
    skipped: replies.skipped + submission.skipped,
    repliedThreads: replies.repliedThreads,
    resolvedThreads: threads.resolvedThreads,
    alreadyResolvedThreads: threads.alreadyResolvedThreads,
    resolveErrors: threads.resolveErrors,
    errors,
    review: submission.review,
    warnings: ctx.truncated
      ? ['existing-comments truncated; duplicate check may be incomplete']
      : [],
  };
  process.stdout.write(JSON.stringify(summary, null, 2) + '\n');

  if (errors.length > 0) {
    process.exitCode = 1;
  }
}

// Only when run as a command. The decision helpers above are importable on their
// own, and an import must never submit a review.
if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`post-comments.mjs failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
