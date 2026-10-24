#!/usr/bin/env node
// Computes the re-review delta for one PR and profile, and writes it to
// .me/review-pr/<review-id>.<profile>.delta.json. It first removes this profile's
// previous reports, so run it before dispatching the reviewers.
//
// Fails OPEN, always: every path that cannot produce a trustworthy delta writes
// `mode: "full"` with a reason instead of a partial one.

import { readFileSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { findLastReview, hasPriorReview, parseDeltaDiff, DEFAULT_TOLERANCE_LINES } from './lib/delta.mjs';
import { parseProfileArg, profilesFor, resolveExtraProfile } from './lib/extra-profile.mjs';
import { parseReviewId } from './lib/diff.mjs';
import { clearProfileReports } from './lib/artifacts.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

const INPUT_DIR = '.me/review-pr';
const LEGACY_INPUT_DIR = '.agents/review-pr';

const EXTRA = resolveExtraProfile();
const PROFILES = profilesFor(EXTRA);

function parseProfile() {
  return parseProfileArg(process.argv, EXTRA);
}

function resolveInputPath(reviewId) {
  const primary = join(INPUT_DIR, `${reviewId}.input.json`);
  if (existsSync(primary)) {
    return primary;
  }
  const legacy = join(LEGACY_INPUT_DIR, `${reviewId}.input.json`);
  if (!existsSync(legacy)) {
    return primary;
  }
  process.stderr.write(`rereview-delta.mjs: WARN using the legacy input ${legacy}; it may be stale. Re-fetch into ${INPUT_DIR}.\n`);
  return legacy;
}

function git(args) {
  const bin = process.env.GIT_BIN || 'git';
  return spawnSync(bin, args, { encoding: 'utf-8', maxBuffer: 64 * 1024 * 1024 });
}

function commitExists(sha) {
  return git(['cat-file', '-e', `${sha}^{commit}`]).status === 0;
}

function full(reason, extra = {}) {
  return { mode: 'full', reason, ...extra };
}

/**
 * The whole delta decision, expressed over injectable primitives so the tests can
 * drive every branch without a real repository.
 *
 * `coveredByFullReview` is true when a `full` review already covers this profile on
 * this exact head; with `force` that case becomes a full review of the whole diff.
 */
export function resolveDelta({ existingComments, profile, headSha, deps, force = false }) {
  const { commitExists: exists, diff, isAncestor } = deps;

  if (!headSha) {
    return full('input.json no trae head.sha');
  }
  const lastReview = findLastReview(existingComments, profile);
  if (lastReview === null) {
    // The prior review predates the head stamp: review everything; this run leaves the stamp.
    if (hasPriorReview(existingComments, profile)) {
      return full(
        'la revision previa de este perfil es anterior al stamp de head: esta corrida revisa el diff '
        + 'completo y deja el stamp para la siguiente',
        { legacyReview: true },
      );
    }
    return full('sin revision previa: es la primera revision de este PR para este perfil');
  }
  const lastReviewedSha = lastReview.sha;
  if (lastReviewedSha === headSha) {
    const coveredByFull = profile !== 'full' && lastReview.coveredBy === 'full';
    if (coveredByFull && force) {
      return full('--force: una revision full ya cubre este head y se revisa de nuevo el diff completo', { lastReviewedSha });
    }
    return {
      mode: 'delta',
      reason: 'el head no cambio desde la ultima revision',
      lastReviewedSha,
      headSha,
      files: {},
      toleranceLines: DEFAULT_TOLERANCE_LINES,
      noChangesSinceLastReview: true,
      coveredByFullReview: coveredByFull,
    };
  }
  if (!exists(lastReviewedSha)) {
    return full(`el commit ${lastReviewedSha} de la revision anterior no esta en este clon`, { lastReviewedSha });
  }
  if (!exists(headSha)) {
    return full(`el commit ${headSha} del head no esta en este clon`, { lastReviewedSha });
  }
  if (!isAncestor(lastReviewedSha, headSha)) {
    return full(
      `la rama fue reescrita: ${lastReviewedSha} ya no es ancestro de ${headSha}`,
      { lastReviewedSha },
    );
  }
  const raw = diff(lastReviewedSha, headSha);
  if (raw === null) {
    return full('git diff fallo al calcular el delta', { lastReviewedSha });
  }
  return {
    mode: 'delta',
    reason: null,
    lastReviewedSha,
    headSha,
    files: parseDeltaDiff(raw),
    toleranceLines: DEFAULT_TOLERANCE_LINES,
    noChangesSinceLastReview: false,
  };
}

async function main() {
  const usage = `Usage: rereview-delta.mjs <pr-id|url|working> [--profile ${PROFILES.join('|')}] [--force]`;
  rejectUnknownFlags('rereview-delta.mjs', usage, { valueFlags: ['--profile'], booleanFlags: ['--force'] });
  const reviewId = parseReviewId(process.argv[2]);
  if (reviewId === null) {
    process.stderr.write(`${usage}\n`);
    process.exit(2);
  }
  const profile = parseProfile();
  const force = process.argv.includes('--force');
  const outPath = join(INPUT_DIR, `${reviewId}.${profile}.delta.json`);

  const clearedReports = clearProfileReports(INPUT_DIR, reviewId, profile);

  let result;
  if (reviewId === 'working') {
    result = full('revision de working tree: no hay revision previa que comparar');
  } else {
    const inputPath = resolveInputPath(reviewId);
    let input;
    try {
      input = JSON.parse(readFileSync(inputPath, 'utf-8'));
    } catch (err) {
      throw new Error(`Cannot read ${inputPath}: ${err.message}`);
    }
    const headSha = input.head && typeof input.head.sha === 'string' ? input.head.sha : null;
    result = resolveDelta({
      existingComments: input.existingComments,
      profile,
      headSha,
      force,
      deps: {
        commitExists,
        isAncestor: (a, b) => git(['merge-base', '--is-ancestor', a, b]).status === 0,
        diff: (a, b) => {
          const r = git([
            '-c', 'core.quotepath=false',
            'diff', '--unified=0', '--no-renames', '--no-color', `${a}..${b}`,
          ]);
          return r.status === 0 ? r.stdout : null;
        },
      },
    });
  }

  const payload = {
    reviewId,
    profile,
    generatedAt: new Date().toISOString(),
    ...result,
  };
  mkdirSync(INPUT_DIR, { recursive: true });
  writeFileSync(outPath, JSON.stringify(payload, null, 2));

  process.stdout.write(JSON.stringify({
    reviewId,
    profile,
    mode: payload.mode,
    reason: payload.reason ?? null,
    lastReviewedSha: payload.lastReviewedSha ?? null,
    headSha: payload.headSha ?? null,
    filesInDelta: payload.files ? Object.keys(payload.files).length : 0,
    noChangesSinceLastReview: payload.noChangesSinceLastReview ?? false,
    coveredByFullReview: payload.coveredByFullReview ?? false,
    legacyReview: payload.legacyReview ?? false,
    clearedReports,
    deltaPath: outPath,
  }, null, 2) + '\n');
}

const invokedDirectly = process.argv[1] && process.argv[1].endsWith('rereview-delta.mjs');
if (invokedDirectly) {
  main().catch((err) => {
    process.stderr.write(`rereview-delta.mjs failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
