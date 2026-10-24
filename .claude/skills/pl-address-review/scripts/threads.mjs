#!/usr/bin/env node
// The review-thread access pl-address-review needs, and nothing more:
//
//   threads.mjs list <pr>          every review thread of the PR, with its comments
//   threads.mjs resolve <threadId> resolves one thread
//
// Both run fixed GraphQL documents; no query text ever comes from the command line.
// The GitHub client (token, repository, base URL, retries) is pl-review-pr's
// lib/gh.mjs: the two skills are always emitted together.
//
// Exit codes: 0 ok, 1 GitHub or environment failure, 2 usage.
import { pathToFileURL } from 'node:url';
import { ghGraphql, parsePrId, repoPath, resolveReviewThread } from '../../pl-review-pr/scripts/lib/gh.mjs';

const USAGE = 'Usage: threads.mjs list <pr-number|pr-url>\n       threads.mjs resolve <threadId>';

// Upper bound on thread pages (100 threads each), so a malformed cursor chain cannot
// loop forever.
const MAX_PAGES = 50;

// A GraphQL node id, e.g. `PRRT_kwDOABCD12`: base64url-ish characters only.
const THREAD_ID_PATTERN = /^[A-Za-z0-9_=-]+$/;

const THREADS_QUERY = `query AddressReviewThreads($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id isResolved isOutdated path line
          comments(first: 100) {
            pageInfo { hasNextPage endCursor }
            nodes { databaseId author { login } body url createdAt }
          }
        }
      }
    }
  }
}`;

/**
 * Validates the command line. Returns `{ command, pr }` or `{ command, threadId }`,
 * or `{ errors }` with every problem found.
 */
export function parseCommandLine(args) {
  if (!Array.isArray(args)) {
    throw new TypeError('parseCommandLine: args must be an array');
  }
  const errors = [];
  const flags = args.filter((a) => typeof a === 'string' && a.startsWith('-'));
  const positionals = args.filter((a) => !(typeof a === 'string' && a.startsWith('-')));
  if (flags.length > 0) {
    errors.push(`unknown flag(s): ${flags.join(', ')}`);
  }
  const [command, target, ...extra] = positionals;
  if (extra.length > 0) {
    errors.push(`unexpected argument(s): ${extra.join(' ')}`);
  }
  if (command !== 'list' && command !== 'resolve') {
    errors.push(command === undefined ? 'missing subcommand (list or resolve)' : `unknown subcommand: ${command}`);
  }
  let pr = null;
  if (command === 'list') {
    if (target === undefined) {
      errors.push('list needs a PR number or URL');
    } else {
      try {
        pr = parsePrId(target);
      } catch (err) {
        errors.push(err.message);
      }
    }
  }
  const isValidThreadId = typeof target === 'string' && THREAD_ID_PATTERN.test(target);
  if (command === 'resolve' && !isValidThreadId) {
    errors.push(target === undefined ? 'resolve needs a thread id' : `invalid thread id: ${target}`);
  }
  if (errors.length > 0) {
    return { errors };
  }
  if (command === 'list') {
    return { command, pr };
  }
  return { command, threadId: target };
}

function normalizeComment(node) {
  const comment = {
    id: Number.isInteger(node?.databaseId) ? node.databaseId : null,
    author: typeof node?.author?.login === 'string' ? node.author.login : null,
    body: typeof node?.body === 'string' ? node.body : '',
    url: typeof node?.url === 'string' ? node.url : null,
    createdAt: typeof node?.createdAt === 'string' ? node.createdAt : null,
  };
  return comment;
}

function normalizeThread(node) {
  const comments = Array.isArray(node?.comments?.nodes) ? node.comments.nodes : [];
  const thread = {
    id: node.id,
    isResolved: node.isResolved === true,
    isOutdated: node.isOutdated === true,
    path: typeof node.path === 'string' ? node.path : null,
    line: Number.isInteger(node.line) ? node.line : null,
    comments: comments.map(normalizeComment),
    commentsTruncated: node?.comments?.pageInfo?.hasNextPage === true,
  };
  return thread;
}

/**
 * Every review thread of the PR, following the cursor up to MAX_PAGES.
 */
async function listThreads(repo, pr) {
  const [owner, name] = repo.split('/');
  const threads = [];
  let after = null;
  let hasNext = true;
  for (let page = 0; page < MAX_PAGES && hasNext; page += 1) {
    const data = await ghGraphql(THREADS_QUERY, { owner, name, number: pr, after });
    const connection = data?.repository?.pullRequest?.reviewThreads;
    if (!connection || !Array.isArray(connection.nodes)) {
      throw new Error(`no review threads returned for PR ${pr} (does it exist in ${repo}?)`);
    }
    threads.push(...connection.nodes.map(normalizeThread));
    hasNext = connection.pageInfo?.hasNextPage === true;
    after = connection.pageInfo?.endCursor ?? null;
  }
  const result = { pr, repo, threads, truncated: hasNext };
  return result;
}

async function main() {
  const parsed = parseCommandLine(process.argv.slice(2));
  if (parsed.errors !== undefined) {
    process.stderr.write(`threads.mjs: ${parsed.errors.join('; ')}\n${USAGE}\n`);
    process.exitCode = 2;
    return;
  }
  if (parsed.command === 'list') {
    const result = await listThreads(repoPath(), parsed.pr);
    process.stdout.write(JSON.stringify(result, null, 2) + '\n');
    return;
  }
  const resolved = await resolveReviewThread(parsed.threadId);
  process.stdout.write(JSON.stringify({ threadId: parsed.threadId, isResolved: resolved }, null, 2) + '\n');
  if (!resolved) {
    process.stderr.write(`threads.mjs: GitHub did not report thread ${parsed.threadId} as resolved\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    process.stderr.write(`threads.mjs failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
