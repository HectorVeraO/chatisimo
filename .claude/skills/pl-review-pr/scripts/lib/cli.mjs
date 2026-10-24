// Command-line flag checking shared by every engine script: a flag a script does not
// know is a usage error (exit 2), never silently ignored.
//
// Pure functions, apart from rejectUnknownFlags writing to stderr and exiting.

// A token that looks like a flag: `-x`, `--name`, `--name=value`. A negative number
// is a value, not a flag.
function isFlagToken(token) {
  const looksLikeFlag = typeof token === 'string' && token.startsWith('-') && token !== '-';
  const isNegativeNumber = /^-\d+$/.test(String(token));
  return looksLikeFlag && !isNegativeNumber;
}

/**
 * Every flag in `args` that is neither in `valueFlags` (a flag followed by its value,
 * which is skipped) nor in `booleanFlags`, in order of appearance, duplicates removed.
 */
export function findUnknownFlags(args, { valueFlags = [], booleanFlags = [] } = {}) {
  if (!Array.isArray(args)) {
    throw new TypeError('findUnknownFlags: args must be an array');
  }
  const unknown = [];
  let index = 0;
  while (index < args.length) {
    const token = args[index];
    const name = typeof token === 'string' ? token.split('=')[0] : token;
    const takesValue = valueFlags.includes(name) && !String(token).includes('=');
    if (takesValue) {
      index += 2;
      continue;
    }
    const isUnknown = isFlagToken(token) && !valueFlags.includes(name) && !booleanFlags.includes(name);
    if (isUnknown && !unknown.includes(name)) {
      unknown.push(name);
    }
    index += 1;
  }
  return unknown;
}

/**
 * Exits 2 with one message naming every unknown flag in `process.argv`, followed by
 * `usage`. Returns normally when every flag is known.
 */
export function rejectUnknownFlags(scriptName, usage, spec, argv = process.argv) {
  if (typeof scriptName !== 'string' || scriptName === '' || typeof usage !== 'string' || usage === '') {
    throw new TypeError('rejectUnknownFlags: scriptName and usage must be non-empty strings');
  }
  const unknown = findUnknownFlags(argv.slice(2), spec);
  if (unknown.length === 0) {
    return;
  }
  const label = unknown.length === 1 ? 'unknown flag' : 'unknown flags';
  process.stderr.write(`${scriptName}: ${label}: ${unknown.join(', ')}\n${usage}\n`);
  process.exit(2);
}
