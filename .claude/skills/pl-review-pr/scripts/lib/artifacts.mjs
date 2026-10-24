// Removal of one review's stale artifacts, so a previous run cannot feed the next one.
import { existsSync, readdirSync, rmSync } from 'node:fs';
import { join } from 'node:path';

/**
 * Removes `<reviewId>.<profile>.report.json`, `.report.verified.json` and every
 * `.report.<concern>.json` under `dir`. Other profiles' reports and the shared
 * `<reviewId>.input.json` are never touched. Returns the removed file names.
 */
export function clearProfileReports(dir, reviewId, profile) {
  if (typeof dir !== 'string' || dir === '') {
    throw new Error('clearProfileReports: dir must be a non-empty string');
  }
  if (typeof reviewId !== 'string' || reviewId === '' || typeof profile !== 'string' || profile === '') {
    throw new Error('clearProfileReports: reviewId and profile must be non-empty strings');
  }
  if (!existsSync(dir)) {
    return [];
  }
  const prefix = `${reviewId}.${profile}.report`;
  const stale = readdirSync(dir).filter((name) => name === `${prefix}.json`
    || (name.startsWith(`${prefix}.`) && name.endsWith('.json')));
  for (const name of stale) {
    rmSync(join(dir, name), { force: true });
  }
  return stale;
}
