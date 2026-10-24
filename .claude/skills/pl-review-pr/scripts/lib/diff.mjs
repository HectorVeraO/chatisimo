// Unified-diff parsing shared by the verifier and the re-review delta.
//
// Pure functions only: no filesystem, no process, no git.

/**
 * Parses a review id argument: a PR number, a PR URL, or the literal 'working'.
 * Returns 'working', the PR number as a decimal string, or null when the argument
 * is absent, and throws on anything else. lib/gh.mjs keeps its own stricter copy
 * that rejects 'working', so an uncommitted review never reaches a pull request.
 */
export function parseReviewId(arg) {
  if (arg === undefined || arg === null || arg === '') {
    return null;
  }
  const str = String(arg);
  if (str === 'working') {
    return 'working';
  }
  const urlMatch = str.match(/\/pull\/(\d+)/);
  if (urlMatch) {
    return urlMatch[1];
  }
  if (/^\d+$/.test(str)) {
    return str;
  }
  throw new Error(`Cannot parse review id from argument: ${arg}`);
}

/**
 * Destination-side range of one hunk header. A hunk with `+c,0` deletes only and
 * has no destination line, so it yields null rather than an inverted range.
 */
export function parseHunkHeader(line) {
  const m = line.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
  if (!m) {
    return null;
  }
  const newStart = Number(m[3]);
  const newLen = m[4] !== undefined ? Number(m[4]) : 1;
  if (newLen === 0) {
    return null;
  }
  return {
    startLine: newStart,
    endLine: newStart + newLen - 1,
  };
}

// `git diff` quotes a path containing a space, a quote or a non-ASCII byte, and
// escapes it C-style. Callers that shell out should also set `-c core.quotepath=false`.
/**
 * Unquotes a C-style quoted git path. An unquoted path is returned unchanged.
 */
export function unquotePath(raw) {
  if (raw.length < 2 || raw[0] !== '"' || raw[raw.length - 1] !== '"') {
    return raw;
  }
  const inner = raw.slice(1, -1);
  // Octal escapes are UTF-8 BYTES, not code points: collect the bytes, then decode
  // them once.
  const bytes = [];
  const encoder = new TextEncoder();
  for (let i = 0; i < inner.length; i += 1) {
    if (inner[i] !== '\\') {
      for (const b of encoder.encode(inner[i])) {
        bytes.push(b);
      }
      continue;
    }
    i += 1;
    const c = inner[i];
    if (c === 'n') {
      bytes.push(0x0a);
    } else if (c === 't') {
      bytes.push(0x09);
    } else if (c === 'r') {
      bytes.push(0x0d);
    } else if (c >= '0' && c <= '7') {
      bytes.push(parseInt(inner.slice(i, i + 3), 8) & 0xff);
      i += 2;
    } else {
      for (const b of encoder.encode(c)) {
        bytes.push(b);
      }
    }
  }
  return new TextDecoder('utf-8').decode(Uint8Array.from(bytes));
}

/**
 * Splits a `diff --git` header into its two paths. Returns the destination path,
 * unquoted, or null when the line is not a header this parser understands.
 */
export function parseFileHeader(line) {
  const m = line.match(/^diff --git (?:"a\/.+?"|a\/.+?) (?:"b\/(.+)"|b\/(.+))$/);
  if (!m) {
    return null;
  }
  const quoted = m[1] !== undefined;
  const value = quoted ? `"${m[1]}"` : m[2];
  return unquotePath(value);
}

// The metadata lines git emits between a `diff --git` line and the first payload
// line. The same list fetch-pr.mjs uses to find where a block's payload starts.
const HEADER_LINE_PREFIXES = [
  'old mode ',
  'new mode ',
  'new file mode ',
  'deleted file mode ',
  'similarity index ',
  'dissimilarity index ',
  'rename from ',
  'rename to ',
  'copy from ',
  'copy to ',
  'index ',
  '--- ',
  '+++ ',
];

/**
 * True for a git diff metadata line (mode, index, rename, `---`/`+++` path lines).
 * Only meaningful for lines between a `diff --git` header and the first payload line.
 */
export function isHeaderLine(line) {
  return HEADER_LINE_PREFIXES.some((prefix) => line.startsWith(prefix));
}

/**
 * Converts CRLF line endings to LF.
 */
export function normalizeEol(s) {
  return String(s).replace(/\r\n/g, '\n');
}

/**
 * Parses a unified diff into a Map of destination path -> { path, hunks, body }.
 * Each entry carries its own body: every line from its `diff --git` header up to
 * the next one. That slice is what an evidence string is checked against, so a
 * line lifted from another file in the same diff cannot ground a finding here.
 */
export function parseDiff(diff) {
  const files = new Map();
  let currentPath = null;
  let inHeader = false;
  for (const line of normalizeEol(diff).split('\n')) {
    if (line.startsWith('diff --git ')) {
      currentPath = parseFileHeader(line);
      inHeader = true;
      if (currentPath !== null && !files.has(currentPath)) {
        files.set(currentPath, { path: currentPath, hunks: [], bodyLines: [] });
      }
      continue;
    }
    if (!currentPath) {
      continue;
    }
    // The metadata between `diff --git` and the first payload line (`index`, mode
    // lines, `--- a/<path>`, `+++ b/<path>`) is not code. Left in the body, the
    // `+++ b/<path>` line alone would ground any evidence that quotes the path.
    if (inHeader && isHeaderLine(line)) {
      continue;
    }
    inHeader = false;
    const entry = files.get(currentPath);
    entry.bodyLines.push(line);
    if (line.startsWith('@@')) {
      const hunk = parseHunkHeader(line);
      if (hunk !== null) {
        entry.hunks.push(hunk);
      }
    }
  }
  for (const entry of files.values()) {
    entry.body = entry.bodyLines.join('\n');
    delete entry.bodyLines;
  }
  return files;
}

/**
 * Renders hunk ranges as a short human-readable list.
 */
export function describeHunks(hunks) {
  if (hunks.length === 0) {
    return '(no hunks)';
  }
  return hunks.map((h) => `${h.startLine}-${h.endLine}`).join(', ');
}
