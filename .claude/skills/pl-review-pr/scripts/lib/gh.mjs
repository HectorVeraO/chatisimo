// The GitHub host layer: REST and GraphQL calls, credentials, the repository slug,
// the PR id parser, and the one normalized shape every script reads PR comments in.
import { spawnSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

const DEFAULT_API_URL = 'https://api.github.com';

// A hung connection must fail the run instead of stalling a review indefinitely.
const REQUEST_TIMEOUT_MS = 30000;

// Upper bound on pages followed by ghApiAll. At per_page=100 this is 5000 items;
// past it the result is reported as truncated rather than looping forever on a
// malformed `Link` chain.
const MAX_PAGES = 50;

// Transient failures are retried: a network error or timeout, and these statuses.
// Every other 4xx is the caller's mistake and fails on the first answer, except a
// 403 that carries a rate-limit signal (GitHub's secondary rate limit).
const MAX_ATTEMPTS = 3;
const RETRYABLE_STATUSES = new Set([429, 502, 503, 504]);
const DEFAULT_RETRY_BASE_MS = 1000;

// A Retry-After longer than this many base delays is clamped, so a server cannot
// park the run for minutes (10 s with the default base).
const RETRY_AFTER_CAP_FACTOR = 10;

// What each status the probe and the scripts can meet means, appended to the error.
const STATUS_HINTS = {
  401: 'credentials rejected: the token is invalid or expired (run `gh auth login`, or fix GH_TOKEN/GITHUB_TOKEN)',
  403: 'authenticated, but the token lacks permission for this call (or a rate limit was hit)',
  404: 'path or base URL not found, or a private repository this token cannot see',
  422: 'GitHub rejected the payload (for a review: a line outside the PR diff, or a stale commit)',
};

/**
 * The REST base URL: GITHUB_API_URL without a trailing slash, else https://api.github.com.
 */
function apiBase() {
  const raw = process.env.GITHUB_API_URL;
  const base = (typeof raw === 'string' && raw.trim() !== '') ? raw.trim() : DEFAULT_API_URL;
  const trimmed = base.replace(/\/+$/, '');
  return trimmed;
}

/**
 * The GraphQL endpoint for the REST base: `https://api.github.com/graphql`, and
 * `https://<host>/api/graphql` for a GitHub Enterprise `https://<host>/api/v3` base.
 */
function graphqlUrl() {
  const base = apiBase();
  if (/\/api\/v3$/.test(base)) {
    return base.replace(/\/api\/v3$/, '/api/graphql');
  }
  return `${base}/graphql`;
}

// The host `gh auth token --hostname` expects: github.com for the public API, the
// API host itself for an Enterprise base.
function ghHostname() {
  const host = new URL(apiBase()).host;
  if (host === 'api.github.com') {
    return 'github.com';
  }
  return host;
}

function ghBin() {
  if (process.platform === 'win32') {
    return 'gh.exe';
  }
  return 'gh';
}

/**
 * Where the token comes from: GH_TOKEN, else GITHUB_TOKEN, else `gh auth token`.
 * Returns { token, source } or null when none yields one. The token is never logged.
 */
function resolveToken() {
  for (const name of ['GH_TOKEN', 'GITHUB_TOKEN']) {
    const value = process.env[name];
    if (typeof value === 'string' && value.trim() !== '') {
      return { token: value.trim(), source: name };
    }
  }
  const result = spawnSync(ghBin(), ['auth', 'token', '--hostname', ghHostname()], { encoding: 'utf-8' });
  if (result.error || result.status !== 0) {
    return null;
  }
  const token = (result.stdout ?? '').trim();
  if (token === '') {
    return null;
  }
  return { token, source: 'gh auth token' };
}

let cachedToken = null;

function readToken() {
  if (cachedToken !== null) {
    return cachedToken;
  }
  const resolved = resolveToken();
  if (resolved === null) {
    throw new Error(
      `No GitHub token: GH_TOKEN and GITHUB_TOKEN are unset and \`gh auth token --hostname ${ghHostname()}\` returned none. Run \`gh auth login\`.`,
    );
  }
  cachedToken = resolved.token;
  return cachedToken;
}

// An absolute URL (a pagination `next` link) is accepted only on the configured
// API's own origin. The Authorization header goes with every request, so any other
// origin would leak the token.
function buildUrl(pathOrUrl) {
  if (typeof pathOrUrl !== 'string' || pathOrUrl.length === 0) {
    throw new TypeError('ghApi: path must be a non-empty string');
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(pathOrUrl)) {
    const expected = new URL(apiBase()).origin;
    const actual = new URL(pathOrUrl).origin;
    if (actual !== expected) {
      throw new Error(`ghApi: refusing to send credentials to ${actual} (only ${expected} is allowed)`);
    }
    return pathOrUrl;
  }
  const clean = pathOrUrl.startsWith('/') ? pathOrUrl.slice(1) : pathOrUrl;
  return `${apiBase()}/${clean}`;
}

// The base backoff delay: REVIEW_PR_RETRY_BASE_MS when it is a non-negative integer,
// else one second. Attempt n waits base * 2^(n-1) unless the server sent Retry-After.
function retryBaseMs() {
  const raw = process.env.REVIEW_PR_RETRY_BASE_MS;
  if (raw !== undefined && /^\d+$/.test(raw.trim())) {
    return Number(raw.trim());
  }
  return DEFAULT_RETRY_BASE_MS;
}

// Milliseconds to wait before the next attempt. A Retry-After in whole seconds is
// honored up to the cap; an HTTP-date or unreadable value falls back to backoff.
function retryDelayMs(attempt, retryAfterHeader) {
  const base = retryBaseMs();
  const backoff = base * (2 ** (attempt - 1));
  const header = typeof retryAfterHeader === 'string' ? retryAfterHeader.trim() : '';
  if (!/^\d+$/.test(header)) {
    return backoff;
  }
  const requested = Number(header) * 1000;
  const delay = Math.min(requested, base * RETRY_AFTER_CAP_FACTOR);
  return delay;
}

// A 403 is a rate limit, not a permission problem, when GitHub says so in headers.
function isRateLimited(outcome) {
  if (outcome.status !== 403) {
    return false;
  }
  const hasRetryAfter = typeof outcome.headers.get('retry-after') === 'string';
  const exhausted = outcome.headers.get('x-ratelimit-remaining') === '0';
  return hasRetryAfter || exhausted;
}

// One HTTP exchange. Returns { status, ok, text, headers } or throws on a network
// error or timeout, which the caller treats as transient.
async function sendOnce(url, method, headers, payload) {
  const resp = await fetch(url, { method, headers, body: payload, signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
  const text = await resp.text();
  const outcome = { status: resp.status, ok: resp.ok, text, headers: resp.headers };
  return outcome;
}

function parseBody(text, accept, method, url) {
  if (text === '') {
    return null;
  }
  if (accept.includes('json')) {
    try {
      return JSON.parse(text);
    } catch (err) {
      throw new Error(`ghApi ${method} ${url} -> invalid JSON: ${err.message}`);
    }
  }
  return text;
}

function httpError(method, url, outcome) {
  const hint = STATUS_HINTS[outcome.status];
  const suffix = hint === undefined ? '' : ` (${hint})`;
  const error = new Error(`ghApi ${method} ${url} -> HTTP ${outcome.status}${suffix}: ${outcome.text.slice(0, 500)}`);
  error.status = outcome.status;
  return error;
}

/**
 * One authenticated GitHub REST call. Returns the parsed JSON body (text for a
 * non-JSON `accept`, null for an empty body), or `{ body, headers }` with `raw: true`.
 * Throws on a non-2xx status (the error carries `status`) or a timeout.
 *
 * A network error, a timeout, HTTP 429/502/503/504 or a rate-limited 403 is retried,
 * up to MAX_ATTEMPTS in total. A non-idempotent call passes `alreadyApplied`: before
 * a retry that follows an ambiguous failure (anything but a rate limit, which the
 * server did not process), it is awaited, and a non-null answer means the first
 * attempt did land, so that answer is returned and nothing is sent again. When it
 * throws, the call fails with that error rather than risk a duplicate write.
 */
async function ghApi(path, options = {}) {
  const {
    method = 'GET',
    body = null,
    accept = 'application/vnd.github+json',
    alreadyApplied = null,
    raw = false,
  } = options;
  const url = buildUrl(path);
  const headers = {
    Authorization: `Bearer ${readToken()}`,
    Accept: accept,
    'X-GitHub-Api-Version': '2022-11-28',
    'User-Agent': 'pl-review-pr',
  };
  let payload;
  if (body !== null) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  for (let attempt = 1; ; attempt += 1) {
    let outcome = null;
    let failure = null;
    try {
      outcome = await sendOnce(url, method, headers, payload);
    } catch (err) {
      const kind = err.name === 'TimeoutError' ? 'timeout' : 'network error';
      failure = new Error(`ghApi ${method} ${url} -> ${kind}: ${err.message}`);
    }
    if (outcome !== null && outcome.ok) {
      const parsed = parseBody(outcome.text, accept, method, url);
      if (raw) {
        return { body: parsed, headers: outcome.headers };
      }
      return parsed;
    }
    if (outcome !== null) {
      failure = httpError(method, url, outcome);
    }
    const rateLimited = outcome !== null && (outcome.status === 429 || isRateLimited(outcome));
    const retryable = outcome === null || RETRYABLE_STATUSES.has(outcome.status) || rateLimited;
    if (!retryable || attempt >= MAX_ATTEMPTS) {
      throw failure;
    }
    await sleep(retryDelayMs(attempt, outcome === null ? null : outcome.headers.get('retry-after')));
    if (!rateLimited && alreadyApplied !== null) {
      const landed = await alreadyApplied();
      if (landed !== null && landed !== undefined) {
        return landed;
      }
    }
  }
}

/**
 * The `rel="next"` URL of a `Link` header, or null.
 */
function nextLink(linkHeader) {
  if (typeof linkHeader !== 'string' || linkHeader === '') {
    return null;
  }
  for (const part of linkHeader.split(',')) {
    const m = part.match(/<([^>]+)>\s*;\s*rel="?next"?/);
    if (m) {
      return m[1];
    }
  }
  return null;
}

/**
 * Every item of a paginated array endpoint, following `Link: rel="next"` up to
 * MAX_PAGES. Returns { values, truncated }: truncated is true only when pages remain.
 */
async function ghApiAll(path) {
  const values = [];
  let next = path;
  for (let page = 0; page < MAX_PAGES && next; page += 1) {
    const resp = await ghApi(next, { raw: true });
    if (Array.isArray(resp.body)) {
      values.push(...resp.body);
    }
    next = nextLink(resp.headers.get('link'));
  }
  const result = { values, truncated: Boolean(next) };
  return result;
}

/**
 * One GraphQL operation. Returns `data`; throws on an HTTP failure and on a 200
 * that carries an `errors` array, which is how GraphQL reports a failed operation.
 */
async function ghGraphql(query, variables = {}) {
  if (typeof query !== 'string' || query.trim() === '') {
    throw new TypeError('ghGraphql: query must be a non-empty string');
  }
  const resp = await ghApi(graphqlUrl(), { method: 'POST', body: { query, variables } });
  if (!resp || typeof resp !== 'object') {
    throw new Error('ghGraphql: empty response');
  }
  if (Array.isArray(resp.errors) && resp.errors.length > 0) {
    const messages = resp.errors.map((e) => (e && e.message) || JSON.stringify(e)).join('; ');
    throw new Error(`ghGraphql: ${messages}`);
  }
  return resp.data ?? null;
}

/**
 * Parses a PR number or PR URL. Returns the number, or null when absent; throws on
 * anything else, including 'working', so an uncommitted review never reaches a PR.
 */
function parsePrId(arg) {
  if (arg === undefined || arg === null || arg === '') {
    return null;
  }
  const str = String(arg);
  const urlMatch = str.match(/\/pull\/(\d+)/);
  if (urlMatch) {
    return Number(urlMatch[1]);
  }
  if (/^\d+$/.test(str)) {
    return Number(str);
  }
  throw new Error(`Cannot parse PR id from argument: ${arg}`);
}

const SLUG_PATTERN = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

// `owner/repo` from a GitHub remote URL (https, ssh or scp-like), or null.
function slugFromRemote(url) {
  const m = String(url).trim().match(/[/:]([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+?)(?:\.git)?\/?$/);
  if (!m) {
    return null;
  }
  return `${m[1]}/${m[2]}`;
}

/**
 * The `owner/repo` slug: GH_REPO (gh's own variable; a `host/` prefix is dropped),
 * else the `origin` remote. Throws when neither yields one.
 */
function repoPath() {
  const fromEnv = process.env.GH_REPO;
  if (typeof fromEnv === 'string' && fromEnv.trim() !== '') {
    const parts = fromEnv.trim().split('/');
    const slug = parts.slice(-2).join('/');
    if (!SLUG_PATTERN.test(slug)) {
      throw new Error(`GH_REPO must be "owner/repo" (optionally "host/owner/repo"), got ${JSON.stringify(fromEnv)}`);
    }
    return slug;
  }
  const gitBin = process.platform === 'win32' ? 'git.exe' : 'git';
  const result = spawnSync(gitBin, ['remote', 'get-url', 'origin'], { encoding: 'utf-8' });
  const slug = result.status === 0 ? slugFromRemote(result.stdout) : null;
  if (slug === null) {
    throw new Error('Cannot resolve the repository: set GH_REPO="owner/repo" or run inside a clone whose `origin` points at GitHub.');
  }
  return slug;
}

function userLogin(user) {
  if (user && typeof user.login === 'string' && user.login !== '') {
    return user.login;
  }
  return null;
}

function intOrNull(value) {
  return Number.isInteger(value) ? value : null;
}

/**
 * One PR review comment (`GET /pulls/{n}/comments`) in the normalized shape. The
 * only kind a reply can target: GitHub threads review comments and nothing else.
 */
function normalizeReviewComment(comment) {
  const normalized = {
    id: comment.id,
    kind: 'inline',
    body: typeof comment.body === 'string' ? comment.body : '',
    author: userLogin(comment.user),
    createdAt: comment.created_at ?? null,
    updatedAt: comment.updated_at ?? null,
    path: typeof comment.path === 'string' ? comment.path : null,
    line: intOrNull(comment.line),
    originalLine: intOrNull(comment.original_line),
    side: typeof comment.side === 'string' ? comment.side : null,
    parentId: intOrNull(comment.in_reply_to_id),
    reviewId: intOrNull(comment.pull_request_review_id),
  };
  return normalized;
}

/**
 * One PR conversation comment (`GET /issues/{n}/comments`) in the normalized shape.
 */
function normalizeIssueComment(comment) {
  const normalized = {
    id: comment.id,
    kind: 'general',
    body: typeof comment.body === 'string' ? comment.body : '',
    author: userLogin(comment.user),
    createdAt: comment.created_at ?? null,
    updatedAt: comment.updated_at ?? null,
    path: null,
    line: null,
    originalLine: null,
    side: null,
    parentId: null,
    reviewId: null,
  };
  return normalized;
}

/**
 * One submitted review (`GET /pulls/{n}/reviews`) in the normalized shape, its body
 * being the review's top-level text; null for a pending review or an empty body.
 */
function normalizeReview(review) {
  if (!review || review.state === 'PENDING') {
    return null;
  }
  const body = typeof review.body === 'string' ? review.body : '';
  if (body.trim() === '') {
    return null;
  }
  const normalized = {
    id: review.id,
    kind: 'review',
    body,
    author: userLogin(review.user),
    createdAt: review.submitted_at ?? null,
    updatedAt: review.submitted_at ?? null,
    path: null,
    line: null,
    originalLine: null,
    side: null,
    parentId: null,
    reviewId: review.id,
    state: typeof review.state === 'string' ? review.state : null,
  };
  return normalized;
}

/**
 * Every comment of a PR in the normalized shape: review comments, conversation
 * comments and submitted review bodies. Returns { comments, truncated }.
 */
async function listPrComments(repo, prId) {
  const base = `repos/${repo}`;
  const inline = await ghApiAll(`${base}/pulls/${prId}/comments?per_page=100`);
  const general = await ghApiAll(`${base}/issues/${prId}/comments?per_page=100`);
  const reviews = await ghApiAll(`${base}/pulls/${prId}/reviews?per_page=100`);
  const comments = [
    ...inline.values.map(normalizeReviewComment),
    ...general.values.map(normalizeIssueComment),
    ...reviews.values.map(normalizeReview).filter((r) => r !== null),
  ];
  const result = { comments, truncated: inline.truncated || general.truncated || reviews.truncated };
  return result;
}

const REVIEW_THREADS_QUERY = `query ReviewThreads($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { id isResolved comments(first: 100) { nodes { databaseId } } }
      }
    }
  }
}`;

/**
 * The PR's review threads as [{ threadId, isResolved, commentIds }], following the
 * cursor up to MAX_PAGES. Returns { threads, truncated }.
 */
async function listReviewThreads(repo, prId) {
  const [owner, name] = repo.split('/');
  const threads = [];
  let after = null;
  let hasNext = true;
  for (let page = 0; page < MAX_PAGES && hasNext; page += 1) {
    const data = await ghGraphql(REVIEW_THREADS_QUERY, { owner, name, number: prId, after });
    const connection = data?.repository?.pullRequest?.reviewThreads;
    if (!connection || !Array.isArray(connection.nodes)) {
      throw new Error(`ghGraphql ReviewThreads: no reviewThreads for PR ${prId}`);
    }
    for (const node of connection.nodes) {
      const ids = (node?.comments?.nodes ?? []).map((c) => c?.databaseId).filter((id) => Number.isInteger(id));
      threads.push({ threadId: node.id, isResolved: node.isResolved === true, commentIds: ids });
    }
    hasNext = connection.pageInfo?.hasNextPage === true;
    after = connection.pageInfo?.endCursor ?? null;
  }
  const result = { threads, truncated: hasNext };
  return result;
}

const RESOLVE_THREAD_MUTATION = `mutation ResolveReviewThread($threadId: ID!) {
  resolveReviewThread(input: { threadId: $threadId }) { thread { id isResolved } }
}`;

/**
 * Resolves one review thread by its GraphQL node id.
 */
async function resolveReviewThread(threadId) {
  if (typeof threadId !== 'string' || threadId === '') {
    throw new TypeError('resolveReviewThread: threadId must be a non-empty string');
  }
  const data = await ghGraphql(RESOLVE_THREAD_MUTATION, { threadId });
  const resolved = data?.resolveReviewThread?.thread?.isResolved === true;
  return resolved;
}

export {
  apiBase,
  ghApi,
  ghApiAll,
  ghGraphql,
  graphqlUrl,
  listPrComments,
  listReviewThreads,
  nextLink,
  normalizeIssueComment,
  normalizeReview,
  normalizeReviewComment,
  parsePrId,
  repoPath,
  resolveReviewThread,
  resolveToken,
};
