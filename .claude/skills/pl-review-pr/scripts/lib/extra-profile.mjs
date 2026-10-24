// The optional extra review profile: one project-defined gate beside default|full,
// with its own concern, its own reviewer agent and a section of the general comment
// written for a reader who does not program.
//
// Rendered by the agent-infra generator into the EXTRA_PROFILE constant (the profile name,
// which is also its concern name, e.g. 'cert') and EXTRA_PROFILE_ZONE (comma-separated
// path globs, e.g. 'e2e/tests/modules/**,e2e/src/modules/**'). When the name renders
// EMPTY there is no extra profile, and every script behaves exactly as a
// default|full engine: same profile list, same usage text, same digest, same merge.
//
// Everything else derives from the name, so no further token exists: the concern is
// the name, the agent is `<name>-reviewer`, the marker is `**[review-pr:<name>`, and
// a source branch `<name>/...` marks the change as this profile's work.
//
// Pure functions only.

// Raw template literals, so a rendered value may contain a quote or a backslash.
// The generator must still refuse a value containing a backtick or `${`, which a
// raw template cannot carry.
const EXTRA_PROFILE = String.raw``;
const EXTRA_PROFILE_ZONE = String.raw``;

/**
 * The profiles every engine accepts, with or without an extra profile.
 */
export const BASE_PROFILES = ['default', 'full'];

// Profile names an older engine published under. Never accepted, and never reusable
// as the extra profile: PRs already carry markers with these names.
const RETIRED_PROFILES = ['dev', 'arch'];

// Names the extra profile may never take: a collision would let it shadow a base
// profile's artifacts or a code concern's report path.
const RESERVED_NAMES = [...BASE_PROFILES, ...RETIRED_PROFILES, 'logic', 'security', 'architecture', 'patterns', 'secrets', 'test', 'working'];

const NAME_PATTERN = /^[a-z][a-z0-9-]*$/;

function isUnrendered(value) {
  return value.startsWith('{{');
}

// A rendered constant always wins, and a rendered EMPTY name means disabled. The env
// fallback applies only to the UNRENDERED template (the generic engine run out of
// ~/.claude, and this template's own test suite).
function rawSetting(constant, envName, env) {
  const value = typeof constant === 'string' ? constant.trim() : '';
  if (!isUnrendered(value)) {
    return value;
  }
  const fromEnv = env[envName];
  return typeof fromEnv === 'string' ? fromEnv.trim() : '';
}

function escapeRegex(text) {
  return text.replace(/[.+^${}()|[\]\\]/g, '\\$&');
}

/**
 * Converts one path glob into an anchored regex source. `**` spans directories,
 * `*` stays inside one segment, `?` is one non-slash character; a trailing `/**`
 * also matches the directory's files at any depth.
 */
export function globToRegexSource(glob) {
  if (typeof glob !== 'string' || glob.trim() === '') {
    throw new Error('globToRegexSource: glob must be a non-empty string');
  }
  let source = '';
  const text = glob.trim();
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === '*' && text[i + 1] === '*') {
      const followedBySlash = text[i + 2] === '/';
      source += followedBySlash ? '(?:.*/)?' : '.*';
      i += followedBySlash ? 3 : 2;
      continue;
    }
    if (ch === '*') {
      source += '[^/]*';
      i += 1;
      continue;
    }
    if (ch === '?') {
      source += '[^/]';
      i += 1;
      continue;
    }
    source += escapeRegex(ch);
    i += 1;
  }
  const anchored = `^${source}$`;
  return anchored;
}

/**
 * Builds a predicate over repo-relative paths from a comma-separated glob list, or
 * null when the list is empty.
 */
export function zoneMatcherFrom(zoneList) {
  const globs = String(zoneList ?? '')
    .split(',')
    .map((g) => g.trim())
    .filter((g) => g !== '');
  if (globs.length === 0) {
    return null;
  }
  const patterns = globs.map((g) => new RegExp(globToRegexSource(g)));
  const matcher = (path) => patterns.some((re) => re.test(path));
  return matcher;
}

/**
 * The active extra profile, or null when disabled. Throws, with every problem found,
 * when the configured name or zone is unusable.
 */
export function resolveExtraProfile(env = process.env) {
  const name = rawSetting(EXTRA_PROFILE, 'REVIEW_PR_EXTRA_PROFILE', env);
  if (name === '') {
    return null;
  }
  const zone = rawSetting(EXTRA_PROFILE_ZONE, 'REVIEW_PR_EXTRA_PROFILE_ZONE', env);

  const errors = [];
  if (!NAME_PATTERN.test(name)) {
    errors.push(`extra profile name '${name}' must match ${NAME_PATTERN.source}`);
  }
  if (RESERVED_NAMES.includes(name)) {
    errors.push(`extra profile name '${name}' is reserved (${RESERVED_NAMES.join(', ')})`);
  }
  let zoneMatcher = null;
  try {
    zoneMatcher = zoneMatcherFrom(zone);
  } catch (err) {
    errors.push(`extra profile zone '${zone}' is unusable: ${err.message}`);
  }
  if (errors.length > 0) {
    throw new Error(`Invalid extra review profile: ${errors.join('; ')}`);
  }

  const extra = {
    name,
    zone,
    zoneMatcher,
    branchPrefix: `${name}/`,
  };
  return extra;
}

/**
 * Every profile the scripts accept: the base two, plus the extra one when enabled.
 */
export function profilesFor(extra) {
  if (extra === null || extra === undefined) {
    return BASE_PROFILES;
  }
  const profiles = [...BASE_PROFILES, extra.name];
  return profiles;
}

/**
 * Profiles whose concerns a `full` review, or a pre-profile one, already ran. The
 * extra profile is never here: no `full` review applied its checklist.
 */
export const COVERED_BY_FULL = new Set(BASE_PROFILES);

/**
 * The profile a script uses when no `--profile` is passed.
 */
export const DEFAULT_PROFILE = 'default';

/**
 * Parses `--profile` from an argv array against the active profile list. Defaults to
 * `default`; the extra profile is never a default here - only the dispatcher infers it.
 */
export function parseProfileArg(argv, extra) {
  const profiles = profilesFor(extra);
  const flagIndex = argv.indexOf('--profile');
  if (flagIndex === -1) {
    return DEFAULT_PROFILE;
  }
  const value = argv[flagIndex + 1];
  if (value === undefined || !profiles.includes(value.trim())) {
    throw new Error(`--profile must be one of ${profiles.join(', ')}`);
  }
  return value.trim();
}
