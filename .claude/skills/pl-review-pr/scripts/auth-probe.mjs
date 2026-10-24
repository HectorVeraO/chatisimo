#!/usr/bin/env node
// Pre-flight for the review flow: confirms a GitHub token is available, that the
// API accepts it (`GET /user`), which OAuth scopes it carries, and that it can see
// the repository. A key in settings.local.json's `env` block overrides the
// user-level variable even when it is "", so an empty value is reported by name.
// Token values are NEVER printed, not even truncated.
import { apiBase, ghApi, repoPath, resolveToken } from './lib/gh.mjs';
import { rejectUnknownFlags } from './lib/cli.mjs';

// The exact names the scripts look up; no aliases. None is required: without a
// token variable, lib/gh.mjs asks `gh auth token`.
const OPTIONAL = ['GH_TOKEN', 'GITHUB_TOKEN', 'GH_REPO', 'GITHUB_API_URL', 'REVIEW_PR_AUTHOR', 'REVIEW_PR_MODEL'];

// What a failing status means for this probe, read the same way everywhere.
const STATUS_MEANING = {
  401: 'credentials rejected: the token is invalid or expired; run `gh auth login` or fix GH_TOKEN/GITHUB_TOKEN',
  403: 'authenticated, but the token lacks permission (or a rate limit was hit)',
  404: 'path or base URL not found: check GITHUB_API_URL, or (for the repository) GH_REPO and whether this account can see it',
};

function describe(name) {
  const raw = process.env[name];
  if (raw === undefined) {
    return { name, state: 'unset' };
  }
  if (raw.trim() === '') {
    // The shadowing case, called out by name because it looks identical to
    // "unset" from the failing script's side but has a different fix.
    return {
      name,
      state: 'empty',
      hint: 'set to an empty string - if your user environment defines this, remove the key from .claude/settings.local.json rather than blanking it',
    };
  }
  return { name, state: 'set', length: raw.trim().length };
}

// `x-oauth-scopes` is sent for classic tokens and `gh` OAuth tokens only; a
// fine-grained or app token has none, which is not a failure.
function parseScopes(headers) {
  const raw = headers.get('x-oauth-scopes');
  if (typeof raw !== 'string') {
    return null;
  }
  const scopes = raw.split(',').map((s) => s.trim()).filter((s) => s !== '');
  return scopes;
}

function failure(step, err) {
  const status = Number.isInteger(err.status) ? err.status : null;
  const meaning = status === null ? null : (STATUS_MEANING[status] ?? null);
  const entry = { step, status, meaning, error: err.message };
  return entry;
}

function fail(report, message) {
  process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  process.stderr.write(`auth-probe.mjs: ${message}\n`);
  process.exitCode = 1;
}

async function main() {
  rejectUnknownFlags('auth-probe.mjs', 'Usage: auth-probe.mjs', {});
  const env ={ optional: OPTIONAL.map(describe) };
  const report = { ok: false, apiUrl: apiBase(), env };

  const token = resolveToken();
  report.token = token === null ? { present: false } : { present: true, source: token.source };
  if (token === null) {
    fail(report, 'no GitHub token: GH_TOKEN and GITHUB_TOKEN are unset and `gh auth token` returned none. Run `gh auth login`.');
    return;
  }

  let repo = null;
  const problems = [];
  try {
    repo = repoPath();
  } catch (err) {
    problems.push({ step: 'repo', status: null, meaning: null, error: err.message });
  }

  try {
    const user = await ghApi('user', { raw: true });
    report.user = typeof user.body?.login === 'string' ? user.body.login : null;
    report.scopes = parseScopes(user.headers);
  } catch (err) {
    problems.push(failure('GET /user', err));
  }

  if (repo !== null) {
    try {
      const info = await ghApi(`repos/${repo}`);
      report.repo = typeof info?.full_name === 'string' ? info.full_name : repo;
      report.permissions = info?.permissions ?? null;
    } catch (err) {
      problems.push(failure(`GET /repos/${repo}`, err));
    }
  }

  if (problems.length > 0) {
    report.problems = problems;
    const names = problems.map((p) => `${p.step}${p.status === null ? '' : ` -> HTTP ${p.status}`}${p.meaning === null ? '' : ` (${p.meaning})`}`).join('; ');
    fail(report, `failed: ${names}`);
    return;
  }
  report.ok = true;
  process.stdout.write(JSON.stringify(report) + '\n');
}

main().catch((err) => {
  process.stderr.write(`auth-probe.mjs failed: ${err.message}\n`);
  process.exitCode = 1;
});
