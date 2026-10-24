// The single general comment of a review.
//
// It is built twice: `merge-reports.mjs` produces it so a review always posts at least
// one comment, and `verify-report.mjs` rebuilds it from the SURVIVING comments.
//
// Pure functions only, apart from reading the extra-profile configuration.

import { resolveExtraProfile } from './extra-profile.mjs';

/**
 * Rank of each accepted severity tier; higher is more severe.
 */
export const SEVERITY_RANK = { info: 0, warning: 1, blocker: 2 };

/**
 * Canonical severity of a reviewer-written value, and whether it had to be coerced.
 *
 * Case and surrounding whitespace are ignored, so `Blocker` is `blocker`. Anything
 * outside the three tiers - `critical`, a typo, a missing field - becomes `blocker`
 * (fail closed). `coerced` lets the verifier report which comments were reclassified.
 */
export function normalizeSeverity(raw) {
  const text = typeof raw === 'string' ? raw.trim().toLowerCase() : '';
  if (Object.hasOwn(SEVERITY_RANK, text)) {
    return { severity: text, coerced: false, original: raw };
  }
  return { severity: 'blocker', coerced: true, original: raw === undefined ? null : raw };
}

function severityOf(comment) {
  return normalizeSeverity(comment ? comment.severity : undefined).severity;
}

/**
 * True for a comment that raises something. An `info` reply raises nothing: an
 * acknowledgement, or an answer to a question. Published, but never counted as a
 * finding anywhere. `info` is valid on replies only, so an `info` comment that is not
 * a reply is a pending finding like any other: the gate fails closed.
 */
export function isFinding(comment) {
  if (severityOf(comment) !== 'info') {
    return true;
  }
  const isReply = Boolean(comment) && Number.isInteger(comment.parentId) && comment.parentId > 0;
  return !isReply;
}

/**
 * True for the single general comment of a review.
 */
export function isGeneral(comment) {
  return Boolean(comment) && comment.isGeneral === true;
}

/**
 * The review verdict, derived ONLY from the tally of the given comments:
 * `block` with any blocker, `comment` with only warnings, `approve` with none.
 * `suggestion` and `question` are always warning-tier. Both `block` and
 * `comment` stop the approval for every profile. A concern's own verdict never
 * escalates it: a finding that did not survive verification is not pending.
 */
export function verdictFor(comments) {
  const findings = comments.filter((c) => !isGeneral(c) && isFinding(c));
  if (findings.some((c) => severityOf(c) === 'blocker')) {
    return 'block';
  }
  if (findings.length > 0) {
    return 'comment';
  }
  return 'approve';
}

/**
 * A concern's own verdict, corrected against the same rule this module applies to
 * merged comments: an `info` reply is an acknowledgement, not a finding, so a concern
 * reporting `comment` while raising nothing above `info` is recorded as `approve`.
 * Recorded in `general.concernVerdicts` for traceability only: the review verdict
 * comes from the surviving findings alone (`verdictFor`).
 */
export function normalizeConcernVerdict(report) {
  if (report.verdict !== 'comment') {
    return report.verdict;
  }
  const comments = report.comments ?? [];
  const hasFinding = comments.some((comment) => !isGeneral(comment) && isFinding(comment));
  return hasFinding ? 'comment' : 'approve';
}

const HEADLINE = {
  approve: '**Revisado - sin observaciones.**',
  comment: '**Revisado - sin bloqueantes, con observaciones.**',
  block: '**Revisado - con observaciones bloqueantes.**',
};

// The approval policy, the same for every profile: every finding stops the
// approval until it is resolved. Severity only measures its impact.
function approvalBullet(verdict) {
  if (verdict === 'block') {
    return '- El PR no queda aprobado hasta corregir cada bloqueante y resolver cada observacion.';
  }
  if (verdict === 'comment') {
    return '- El PR no queda aprobado hasta resolver cada observacion: la severidad mide el impacto, no si hay que atenderla.';
  }
  return null;
}

function audienceNoteLine(comment) {
  const rule = Number.isInteger(comment.rule) ? ` (regla ${comment.rule})` : '';
  const note = typeof comment.audienceNote === 'string' ? comment.audienceNote.trim() : '';
  if (note === '') {
    return `- Observacion tecnica sin traduccion${rule}: la explica quien mantiene el proyecto.`;
  }
  return `- ${note}${rule}`;
}

// The part of an extra-profile review written for a reader who does not program.
// Built only from the comments that survived the verifier, never from a summary a
// reviewer wrote before verification.
function audienceSection(findings) {
  const blocking = findings.filter((c) => severityOf(c) === 'blocker');
  const observations = findings.filter((c) => severityOf(c) !== 'blocker');
  const lines = ['**En palabras simples:**', ''];
  if (findings.length === 0) {
    lines.push('- El cambio cumple las reglas que se revisan de forma automatica. Falta la revision de quien mantiene el proyecto.');
    return lines.join('\n');
  }
  // Every observation stops the approval; the split only tells the reader which
  // ones weigh more.
  lines.push(`Hay ${findings.length} cosa(s) que resolver antes de que el cambio se apruebe:`);
  if (blocking.length > 0) {
    lines.push('', 'Bloqueantes:', '');
    lines.push(...blocking.map(audienceNoteLine));
  }
  if (observations.length > 0) {
    lines.push('', 'Observaciones de menor impacto, que tambien impiden la aprobacion:', '');
    lines.push(...observations.map(audienceNoteLine));
  }
  return lines.join('\n');
}

function isExtraProfile(profile) {
  if (typeof profile !== 'string' || profile === '') {
    return false;
  }
  const extra = resolveExtraProfile();
  return extra !== null && extra.name === profile;
}

/**
 * Builds the single general comment of a review. `profile` only matters for the
 * extra profile, whose comment gains a section written for a non-technical reader.
 */
export function buildGeneralComment({
  verdict,
  ran = [],
  notRun = [],
  summaries = [],
  comments = [],
  scopeNote = null,
  profile = null,
}) {
  if (!Object.hasOwn(HEADLINE, verdict)) {
    throw new Error(`buildGeneralComment: verdict must be one of ${Object.keys(HEADLINE).join(', ')}, got ${JSON.stringify(verdict)}`);
  }
  const findings = comments.filter((c) => !isGeneral(c) && isFinding(c));
  const blockers = findings.filter((c) => severityOf(c) === 'blocker').length;
  const warnings = findings.length - blockers;
  const replies = comments.filter((c) => !isGeneral(c) && !isFinding(c)).length;

  const bullets = [];
  bullets.push(`- Revisiones ejecutadas: ${ran.length > 0 ? ran.join(', ') : 'ninguna'}.`);
  if (notRun.length > 0) {
    bullets.push(`- Revisiones NO ejecutadas: ${notRun.join(', ')}.`);
  }
  if (scopeNote) {
    bullets.push(`- Alcance: ${scopeNote}.`);
  }
  bullets.push(`- Observaciones: ${blockers} bloqueantes, ${warnings} no bloqueantes.`);
  if (replies > 0) {
    bullets.push(`- Respuestas en hilos existentes: ${replies} (no cuentan como observacion).`);
  }
  const approval = approvalBullet(verdict);
  if (approval !== null) {
    bullets.push(approval);
  }
  for (const { concern, summary } of summaries) {
    if (summary) {
      bullets.push(`- ${concern}: ${summary}`);
    }
  }

  const sections = [HEADLINE[verdict], `**Por que:**\n\n${bullets.join('\n')}`];
  if (isExtraProfile(profile)) {
    sections.push(audienceSection(findings));
  }

  const general = {
    isGeneral: true,
    severity: verdict === 'block' ? 'blocker' : (verdict === 'comment' ? 'comment' : 'approved'),
    body: sections.join('\n\n'),
  };
  return general;
}

/**
 * Words of a comment body, code fences excluded. Reported per comment; never a reason
 * to drop one.
 */
export function bodyWords(body) {
  const withoutFences = String(body ?? '').replace(/```[\s\S]*?```/g, ' ');
  const words = withoutFences.trim().split(/\s+/).filter((w) => w !== '');
  return words.length;
}

// The four fixed markers of a finding body (reviewer contract, "The shape").
const FINDING_MARKER = String.raw`\*\*(?:Problem \(What\)|Evidence|Reason \(Why\)|Solution \(How\)):\*\*`;
const HAS_FINDING_MARKER = new RegExp(FINDING_MARKER);
// Whitespace, plus an optional `*` or `-` bullet, right before a marker: where a
// marker that does not start its own line is split off.
const BEFORE_MARKER = new RegExp(String.raw`\s+(?:[*-]\s+)?(?=${FINDING_MARKER})`);
const MARKER_SEGMENT = new RegExp(String.raw`^\s*(?:[*-]\s+)?(${FINDING_MARKER}.*)$`);
const FENCE_LINE = /^\s*(?:```|~~~)/;
const BARE_BULLET = /^\s*[*-]?\s*$/;

/**
 * A finding body laid out so every Markdown renderer shows it as a list: each of
 * the four markers (`Problem (What)`, `Evidence`, `Reason (Why)`, `Solution (How)`)
 * starts its own `* ` line, and a blank line separates the first one from the
 * text above it. Without that blank line a stricter renderer folds the bullets
 * into the title paragraph.
 *
 * Mechanical and idempotent: fenced code blocks are never touched, nothing but
 * the line breaks and bullets around the markers changes, and a body with no
 * marker (a reply, the general comment, anything not a string) is returned as is.
 */
export function formatFindingBody(body) {
  if (typeof body !== 'string' || !HAS_FINDING_MARKER.test(body)) {
    return body;
  }
  const out = [];
  let inFence = false;
  // Whether the current paragraph already holds a marker bullet: only the first
  // bullet of a list needs the blank line, a later one continues the list.
  let inList = false;
  for (const line of body.replace(/\r\n/g, '\n').split('\n')) {
    if (FENCE_LINE.test(line)) {
      inFence = !inFence;
      out.push(line);
      continue;
    }
    if (inFence) {
      out.push(line);
      continue;
    }
    if (line.trim() === '') {
      inList = false;
      out.push(line);
      continue;
    }
    const segments = line.split(BEFORE_MARKER).filter((segment) => !BARE_BULLET.test(segment));
    for (const segment of segments) {
      const marker = MARKER_SEGMENT.exec(segment);
      if (marker === null) {
        out.push(segment);
        continue;
      }
      const previous = out.length > 0 ? out[out.length - 1] : '';
      if (!inList && previous.trim() !== '') {
        out.push('');
      }
      out.push(`* ${marker[1]}`);
      inList = true;
    }
  }
  const formatted = out.join('\n');
  return formatted;
}
