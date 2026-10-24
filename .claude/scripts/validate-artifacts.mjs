#!/usr/bin/env node
// Deterministic validation of the .claude artifacts the agent-infra generator writes:
// agent definitions, skills, hook scripts, and the settings.json wiring between them.
//
// It catches the failures that produce no runtime error, where the harness silently does not
// register the artifact:
//
//   1. Frontmatter not starting at line 1 (the parser is start-anchored; an HTML comment
//      above the opening `---` voids the whole block).
//   2. An unquoted `argument-hint:` whose value starts with `[` (YAML reads a list).
//   3. An agent whose frontmatter `name:` disagrees with its filename. `subagent_type`
//      resolves against `name:`, while engine detection greps for the FILENAME.
//   4. An unquoted `description:` whose value contains `: ` further along the line
//      (e.g. "for logical correctness only: wrong output, crashes"); the harness YAML
//      parser drops the whole block.
//
// Generated-docs checks also cover the root AGENTS.md line budget (ERROR above budget * 1.10 rounded
// down, WARN above the budget up to that tolerance, WARN above three quarters of it; a budget above 500
// is an ERROR), the 120-line budget of each subproject index and each cookbook fragment (WARN), and
// the "Read before" routing table of each subproject index: every `cookbooks/*.md` must be named in a
// `| Task | Fragment |` table of its index, and every path that table names must exist (WARN).
//
// When .claude/references/.generated.json exists, every file it lists is hashed and compared with the
// recorded sha256 (WARN `generated-reference-edited` on a mismatch or a missing file). Each value is
// `sha256:<hex>`: a bare hex digest next to a path such as `secrets-reviewer.md` trips gitleaks'
// generic-api-key rule. A bare hex value is still accepted. The hash is taken
// over the file's UTF-8 text with every CR byte removed, so an LF and a CRLF checkout of the same bytes
// hash the same. No manifest, no check.
//
// Emitted Markdown (every .md under .claude/, every AGENTS.md at any depth, ONBOARDING.md, every
// cookbooks/*.md and the e2e suite README) is also scanned for a leftover template token, a
// surviving optional-block or delimiter marker, and a surviving generator maintenance comment.
// Emitted scripts and JSON under .claude/ (plus .mcp.json) are scanned for leftover tokens too.
// All of these are WARNs: the artifact still registers, but it tells the reader something no one
// substituted.
//
// Constant rendered by the generator when it copies this file into a repo (the raw template
// runs too: an unrendered or empty value falls back to the default and WARNs):
//   ROOT_LINE_BUDGET_SOURCE  - positive integer <= 500, root AGENTS.md line budget; same value as root.md (default 400)
// A rendered value that does not parse is a script failure (exit 1), never a silent default.
//
// Exit codes: 0 = all pass, 1 = the script itself failed, 3 = at least one ERROR.
// WARNs never change the exit code.
//
// Usage: node .claude/scripts/validate-artifacts.mjs [--root <repo-root>] [--json]
import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, basename, dirname, isAbsolute } from 'node:path';
import { createHash } from 'node:crypto';
import assert from 'node:assert';

const VALID_MODELS = new Set(['inherit', 'haiku', 'sonnet', 'opus', 'fable']);
// A full model id (e.g. `claude-haiku-5-5`) is accepted alongside the aliases.
const FULL_MODEL_ID = /^claude-[a-z0-9]+(?:-[a-z0-9]+)*$/;
const VALID_EFFORT = new Set(['low', 'medium', 'high', 'xhigh', 'max']);

// Agent frontmatter: `name` and `description` are required; the rest are validated when present.
const AGENT_REQUIRED = ['name', 'description'];
const AGENT_KNOWN = new Set([
  'name', 'description', 'model', 'effort', 'tools', 'isolation',
  'color', 'permissionMode', 'skills', 'disallowedTools', 'mcpServers', 'hooks', 'memory',
]);

const SKILL_REQUIRED = ['name', 'description'];
const SKILL_KNOWN = new Set([
  'name', 'description', 'argument-hint', 'allowed-tools', 'model',
  'disable-model-invocation', 'when_to_use',
]);

// Agent types the harness provides without a file in .claude/agents/.
const BUILT_IN_AGENT_TYPES = new Set(['general-purpose', 'Explore', 'Plan', 'fork', 'claude']);

// One definition of an AUTOGEN marker line, shared by every check that reads one.
function autogenMarkerPattern(name, edge) {
  assert(typeof name === 'string' && name.length > 0, 'autogenMarkerPattern: name must be non-empty');
  assert(edge === 'START' || edge === 'END', 'autogenMarkerPattern: edge must be START or END');
  const pattern = new RegExp(`<!--\\s*AGENTIC-SETUP:AUTOGEN:(${name})\\s+${edge}\\s*-->`);
  return pattern;
}

const AUTOGEN_START = autogenMarkerPattern('[A-Za-z0-9_-]+', 'START');
const AUTOGEN_END = autogenMarkerPattern('[A-Za-z0-9_-]+', 'END');

const findings = [];

function add(level, file, line, check, message) {
  findings.push({ level, file, line, check, message });
}

function error(file, line, check, message) {
  add('ERROR', file, line, check, message);
}

function warn(file, line, check, message) {
  add('WARN', file, line, check, message);
}

function readFlag(name, fallback) {
  const i = process.argv.indexOf(name);
  if (i === -1) {
    return fallback;
  }
  const v = process.argv[i + 1];
  if (v === undefined || v.startsWith('--')) {
    throw new Error(`${name} requires a value`);
  }
  return v;
}

function listDirs(path) {
  if (!existsSync(path)) {
    return [];
  }
  return readdirSync(path).filter((n) => statSync(join(path, n)).isDirectory());
}

function listFiles(path, ext) {
  if (!existsSync(path)) {
    return [];
  }
  return readdirSync(path).filter((n) => n.endsWith(ext) && statSync(join(path, n)).isFile());
}

// Minimal zero-dependency frontmatter reader for flat `key: value` scalars. It reports
// structural problems instead of tolerating them, matching what the harness rejects.
//
// Returns { ok, fields, order, endLine, reason }.
function parseFrontmatter(text) {
  // CRLF-tolerant: strip a trailing \r per line so core.autocrlf checkouts still match '---'.
  const lines = text.split('\n').map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l));
  if (lines[0] !== '---') {
    // Locate what is squatting on line 1, so the message names the real cause.
    const firstNonEmpty = lines.findIndex((l) => l.trim() !== '');
    const delimiterAt = lines.findIndex((l) => l === '---');
    return {
      ok: false,
      reason: delimiterAt === -1
        ? 'no `---` frontmatter delimiter anywhere in the file'
        : `frontmatter starts at line ${delimiterAt + 1}, not line 1`
          + (firstNonEmpty !== -1 && firstNonEmpty < delimiterAt
            ? ` (line ${firstNonEmpty + 1} begins ${JSON.stringify(lines[firstNonEmpty].slice(0, 40))})`
            : ''),
    };
  }

  const closing = lines.indexOf('---', 1);
  if (closing === -1) {
    return { ok: false, reason: 'opening `---` has no closing `---`' };
  }

  const fields = new Map();
  const order = [];
  for (let i = 1; i < closing; i += 1) {
    const raw = lines[i];
    if (raw.trim() === '' || raw.trimStart().startsWith('#')) {
      continue;
    }
    // A continuation line (indented) belongs to the previous key; folded scalars are
    // legal YAML, so this is not an error - just not something this reader splits.
    if (/^\s/.test(raw) && order.length > 0) {
      const prev = fields.get(order[order.length - 1]);
      prev.value = `${prev.value} ${raw.trim()}`;
      continue;
    }
    const m = raw.match(/^([A-Za-z][A-Za-z0-9_-]*):\s?(.*)$/);
    if (m === null) {
      return { ok: false, reason: `line ${i + 1} is not a \`key: value\` pair: ${JSON.stringify(raw.slice(0, 60))}` };
    }
    const key = m[1];
    if (fields.has(key)) {
      return { ok: false, reason: `duplicate key \`${key}\` at line ${i + 1}` };
    }
    // A block scalar (`>`, `|`, with optional chomping/indent) carries its text on the indented
    // lines below; that text is not a plain scalar, so the quoting checks do not apply to it.
    const blockScalar = /^[>|][-+]?\d*\s*$/.test(m[2].trim());
    fields.set(key, { value: m[2], line: i + 1, blockScalar });
    order.push(key);
  }

  return { ok: true, fields, order, endLine: closing + 1 };
}

// YAML treats a bare value starting with `[` or `{` as a flow collection, so
// `argument-hint: [--staged]` is a LIST. Quoting is the only fix.
function looksLikeUnquotedCollection(value) {
  const v = value.trim();
  if (v === '') {
    return false;
  }
  return v.startsWith('[') || v.startsWith('{');
}

function isQuoted(value) {
  const v = value.trim();
  if (v.length < 2) {
    return false;
  }
  return (v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"));
}

// A plain (unquoted) YAML scalar cannot safely contain `: ` (colon followed by
// whitespace) or end in a bare `:` -- the real parser reads that as the start of a
// nested mapping and the block becomes unparseable (e.g. a `for X only: a, b, c` clause).
function hasUnquotedEmbeddedColon(value) {
  const v = value.trim();
  if (v === '' || isQuoted(v)) {
    return false;
  }
  return /:(\s|$)/.test(v);
}

function unquote(value) {
  const v = value.trim();
  return isQuoted(v) ? v.slice(1, -1) : v;
}

function checkFrontmatterCommon(relPath, text, required, known, kind) {
  const fm = parseFrontmatter(text);
  if (!fm.ok) {
    error(relPath, 1, 'frontmatter-parse', `${fm.reason}. The parser is start-anchored: the ${kind} does not register at all and nothing reports it.`);
    return null;
  }

  for (const key of required) {
    if (!fm.fields.has(key)) {
      error(relPath, 1, 'frontmatter-required', `missing required field \`${key}\``);
      continue;
    }
    if (unquote(fm.fields.get(key).value) === '') {
      error(relPath, fm.fields.get(key).line, 'frontmatter-empty', `field \`${key}\` is empty`);
    }
  }

  for (const key of fm.order) {
    if (!known.has(key)) {
      warn(relPath, fm.fields.get(key).line, 'frontmatter-unknown', `unrecognised field \`${key}\` - a typo here is silently ignored by the harness`);
    }
  }

  for (const key of fm.order) {
    const entry = fm.fields.get(key);
    if (entry.blockScalar) {
      continue;
    }
    if (looksLikeUnquotedCollection(entry.value) && !isQuoted(entry.value)) {
      error(relPath, entry.line, 'frontmatter-unquoted-collection',
        `\`${key}\` starts with ${entry.value.trim()[0]} and is unquoted, so YAML parses it as a collection instead of a string. Wrap the value in double quotes.`);
    }
    if (hasUnquotedEmbeddedColon(entry.value)) {
      error(relPath, entry.line, 'frontmatter-unquoted-colon',
        `\`${key}\` is unquoted and contains \`: \` (or a trailing \`:\`) further in the value. A real YAML parser reads that as a nested mapping and the frontmatter block fails to parse -- the ${kind} then does not register, with no runtime error. Wrap the value in double quotes.`);
    }
  }

  return fm;
}

function validateAgent(root, rel) {
  const abs = join(root, rel);
  const text = readFileSync(abs, 'utf-8');
  const fm = checkFrontmatterCommon(rel, text, AGENT_REQUIRED, AGENT_KNOWN, 'agent');
  if (fm === null) {
    return null;
  }

  const fileName = basename(rel, '.md');
  const declared = fm.fields.has('name') ? unquote(fm.fields.get('name').value) : null;
  if (declared !== null && declared !== fileName) {
    error(rel, fm.fields.get('name').line, 'agent-name-mismatch',
      `frontmatter name \`${declared}\` != filename \`${fileName}\`. subagent_type resolves against name:, engine detection greps the filename - a mismatch half-works.`);
  }

  if (fm.fields.has('model')) {
    const model = unquote(fm.fields.get('model').value);
    const isKnownModel = VALID_MODELS.has(model) || FULL_MODEL_ID.test(model);
    if (!isKnownModel) {
      warn(rel, fm.fields.get('model').line, 'agent-model',
        `model \`${model}\` is not one of ${Array.from(VALID_MODELS).join(', ')} or a full claude-* id - verify it before trusting it`);
    }
  }

  if (fm.fields.has('effort')) {
    const effort = unquote(fm.fields.get('effort').value);
    if (!VALID_EFFORT.has(effort)) {
      error(rel, fm.fields.get('effort').line, 'agent-effort',
        `effort \`${effort}\` is not one of ${Array.from(VALID_EFFORT).join(', ')}`);
    }
  }

  if (fm.fields.has('tools')) {
    const tools = unquote(fm.fields.get('tools').value);
    const names = tools.split(',').map((t) => t.trim()).filter((t) => t !== '');
    if (names.length === 0) {
      error(rel, fm.fields.get('tools').line, 'agent-tools-empty',
        'tools: is present but lists nothing. An empty allowlist is not the same as omitting the field; omit it to inherit.');
    }
    // Case-sensitive on purpose: the tool name `Edit` and the prohibition must share one sentence, so
    // prose such as "never ... edit fields" about other systems is not read as a ban on the tool.
    if (names.includes('Edit') && /\bEdit\b[^.\n]*\b(never allowed|NEVER)\b/.test(text)) {
      error(rel, fm.fields.get('tools').line, 'agent-tools-contradiction',
        'tools: grants Edit while the body states Edit is never allowed. The allowlist wins over prose.');
    }
  }

  // AUTOGEN markers must balance: they are the next generation run's idempotency boundary.
  checkMarkers(rel, text);

  return { name: declared ?? fileName, file: rel };
}

function validateSkill(root, dirRel) {
  const skillRel = join(dirRel, 'SKILL.md');
  const abs = join(root, skillRel);
  if (!existsSync(abs)) {
    error(dirRel, 1, 'skill-missing-file', 'skill directory has no SKILL.md, so the skill does not exist as far as the harness is concerned');
    return null;
  }
  const text = readFileSync(abs, 'utf-8');
  const fm = checkFrontmatterCommon(skillRel, text, SKILL_REQUIRED, SKILL_KNOWN, 'skill');
  if (fm === null) {
    return null;
  }

  const dirName = basename(dirRel);
  const declared = fm.fields.has('name') ? unquote(fm.fields.get('name').value) : null;
  if (declared !== null && declared !== dirName) {
    // The DIRECTORY is the invocation name; `name:` does not drive the registry.
    // So this is a self-contradicting file rather than a broken one - warn, not error.
    warn(skillRel, fm.fields.get('name').line, 'skill-name-mismatch',
      `frontmatter name \`${declared}\` != directory \`${dirName}\`. The directory is the invocation name, so the skill still works - but the file contradicts itself.`);
  }

  // Only warn when the value is NOT already an error, so an unquoted `[--staged]` is reported once.
  if (fm.fields.has('argument-hint') && !fm.fields.get('argument-hint').blockScalar
      && !isQuoted(fm.fields.get('argument-hint').value)
      && !looksLikeUnquotedCollection(fm.fields.get('argument-hint').value)) {
    const v = fm.fields.get('argument-hint').value.trim();
    if (v !== '') {
      warn(skillRel, fm.fields.get('argument-hint').line, 'skill-argument-hint-unquoted',
        'argument-hint is unquoted. It is only an ERROR when the value starts with [ or {, but quoting it always is the rule that never bites.');
    }
  }

  checkMarkers(skillRel, text);
  return { name: dirName, file: skillRel };
}

function checkMarkers(rel, text) {
  const starts = new Map();
  const ends = new Map();
  const lines = text.split('\n');
  lines.forEach((line, idx) => {
    const s = line.match(AUTOGEN_START);
    if (s !== null) {
      starts.set(s[1], (starts.get(s[1]) ?? []).concat(idx + 1));
    }
    const e = line.match(AUTOGEN_END);
    if (e !== null) {
      ends.set(e[1], (ends.get(e[1]) ?? []).concat(idx + 1));
    }
  });

  for (const [name, at] of starts) {
    const closing = ends.get(name) ?? [];
    if (closing.length !== at.length) {
      error(rel, at[0], 'marker-unbalanced',
        `AUTOGEN marker \`${name}\` has ${at.length} START and ${closing.length} END. The generator keys idempotency off these; an unbalanced pair means the next run duplicates or clobbers.`);
    }
    if (at.length > 1) {
      error(rel, at[1], 'marker-duplicate', `AUTOGEN section \`${name}\` appears ${at.length} times`);
    }
  }
  for (const [name, at] of ends) {
    if (!starts.has(name)) {
      error(rel, at[0], 'marker-orphan-end', `AUTOGEN END marker \`${name}\` has no matching START`);
    }
  }
}

// Every string value inside a parsed JSON value, depth-first. JSON has no cycles, and the
// depth guard bounds a pathological file rather than any real settings.json.
function collectStrings(value, depth = 0) {
  assert(depth <= 64, 'collectStrings: JSON nesting deeper than 64 levels');
  if (typeof value === 'string') {
    return [value];
  }
  if (value === null || typeof value !== 'object') {
    return [];
  }
  const children = Array.isArray(value) ? value : Object.values(value);
  const strings = children.flatMap((child) => collectStrings(child, depth + 1));
  return strings;
}

// A hook script that settings.json names but that is absent silently disables the guardrail
// for every developer.
function validateHookWiring(root, agentNames) {
  const settingsRel = join('.claude', 'settings.json');
  const settingsAbs = join(root, settingsRel);
  if (!existsSync(settingsAbs)) {
    warn(settingsRel, 1, 'settings-missing', 'no .claude/settings.json - hooks and permissions are not configured');
    return;
  }

  let settings;
  try {
    settings = JSON.parse(readFileSync(settingsAbs, 'utf-8'));
  } catch (err) {
    error(settingsRel, 1, 'settings-parse', `not valid JSON: ${err.message}`);
    return;
  }

  // Matched on the parsed strings, not on re-serialised JSON, so a Windows-style
  // `.claude\hooks\x.mjs` command is recognised exactly like the POSIX form.
  const referenced = new Set();
  for (const command of collectStrings(settings.hooks ?? {})) {
    for (const m of command.matchAll(/\.claude[\\/]+hooks[\\/]+([A-Za-z0-9_.-]+\.mjs)/g)) {
      referenced.add(m[1]);
    }
  }
  for (const name of referenced) {
    if (!existsSync(join(root, '.claude', 'hooks', name))) {
      error(settingsRel, 1, 'hook-script-missing',
        `settings.json invokes .claude/hooks/${name}, which does not exist. On another developer's checkout that hook fails open and the guardrail is gone.`);
    }
  }

  const hookFiles = listFiles(join(root, '.claude', 'hooks'), '.mjs')
    .filter((n) => n !== 'test-hooks.mjs');
  for (const name of hookFiles) {
    if (!referenced.has(name)) {
      warn(join('.claude', 'hooks', name), 1, 'hook-script-unreferenced',
        'hook script exists but settings.json never invokes it - dead file, or a missing wiring entry');
    }
  }

  // A subagent_type naming an agent that does not exist fails only at dispatch time.
  const skillsRoot = join(root, '.claude', 'skills');
  for (const dir of listDirs(skillsRoot)) {
    const skillFile = join(skillsRoot, dir, 'SKILL.md');
    if (!existsSync(skillFile)) {
      continue;
    }
    const body = readFileSync(skillFile, 'utf-8');
    for (const m of body.matchAll(/`?subagent_type`?\s*[:=]\s*`?([A-Za-z][A-Za-z0-9-]*)`?/g)) {
      const target = m[1];
      if (!agentNames.has(target) && !BUILT_IN_AGENT_TYPES.has(target)) {
        warn(join('.claude', 'skills', dir, 'SKILL.md'), 1, 'subagent-type-unresolved',
          `names subagent_type \`${target}\`, which is not an agent in .claude/agents/. If this is a literal dispatch it fails at dispatch time.`);
      }
    }
  }
}

// --- Generated documentation checks ----------------------------------------
//
// The checks below validate the DOCS the generator writes, which nothing else executes.

// Rendered by the generator from the same value root AGENTS.md states in its
// "Anti-degradation governance" section. String.raw keeps the raw template runnable: an
// unrendered or empty value is detected at runtime instead of being a syntax error that the
// script's own error handling can never report.
const ROOT_LINE_BUDGET_SOURCE = String.raw`400`;

const DEFAULT_ROOT_LINE_BUDGET = 400;
const MAX_ROOT_LINE_BUDGET = 500;
const VALIDATOR_REL = join('.claude', 'scripts', 'validate-artifacts.mjs');

// Resolved once by resolveRenderedConstants() before any check runs.
let ROOT_LINE_BUDGET = DEFAULT_ROOT_LINE_BUDGET;

function isUnrendered(source) {
  assert(typeof source === 'string', 'isUnrendered: source must be a string');
  const v = source.trim();
  return v === '' || v.startsWith('{{');
}

function parseRootLineBudget(source) {
  const problems = [];
  const v = source.trim();
  if (!/^\d+$/.test(v) || Number(v) <= 0) {
    problems.push(`ROOT_LINE_BUDGET_SOURCE must be a positive integer, got ${JSON.stringify(v)}`);
    return { value: DEFAULT_ROOT_LINE_BUDGET, problems };
  }
  return { value: Number(v), problems };
}

// Every rendering problem is collected and thrown together, so one fix pass covers them all.
function resolveRenderedConstants() {
  const problems = [];

  if (isUnrendered(ROOT_LINE_BUDGET_SOURCE)) {
    warn(VALIDATOR_REL, 1, 'validator-unrendered',
      `ROOT_LINE_BUDGET_SOURCE was never rendered; using the default budget of ${DEFAULT_ROOT_LINE_BUDGET}. Render it with the value root AGENTS.md states.`);
    ROOT_LINE_BUDGET = DEFAULT_ROOT_LINE_BUDGET;
  } else {
    const parsed = parseRootLineBudget(ROOT_LINE_BUDGET_SOURCE);
    problems.push(...parsed.problems);
    ROOT_LINE_BUDGET = parsed.value;
  }
  if (ROOT_LINE_BUDGET > MAX_ROOT_LINE_BUDGET) {
    error(VALIDATOR_REL, 1, 'root-line-budget-max',
      `ROOT_LINE_BUDGET_SOURCE is ${ROOT_LINE_BUDGET}, above the hard maximum of ${MAX_ROOT_LINE_BUDGET}; the root budget is checked against ${MAX_ROOT_LINE_BUDGET}. Render a value <= ${MAX_ROOT_LINE_BUDGET}.`);
    ROOT_LINE_BUDGET = MAX_ROOT_LINE_BUDGET;
  }

  if (problems.length > 0) {
    throw new Error(`rendered constants are invalid: ${problems.join('; ')}`);
  }
}

// Directories never walked for generated docs, at any depth. Dot-directories are skipped too:
// the only one the generator writes into is .claude/, which is walked on its own.
const SKIP_DIRS = new Set([
  'node_modules', 'build', 'dist', 'out', 'target', 'vendor', 'coverage',
  'venv', '__pycache__', 'bin', 'obj',
]);

// Bounds the walk on a pathological tree (symlink loops, generated output); real workspace
// members sit a few levels down.
const MAX_WALK_DEPTH = 12;

// Walks the repo once and returns what the generator emits outside .claude/: AGENTS.md at any
// depth (root first), the root ONBOARDING.md, every cookbooks/*.md, and the README of an e2e
// suite (a directory holding a playwright.config.*).
function listGeneratedDocs(root) {
  assert(typeof root === 'string' && root.length > 0, 'listGeneratedDocs: root must be a non-empty path');
  const agents = [];
  const extra = [];
  if (existsSync(join(root, 'ONBOARDING.md'))) {
    extra.push('ONBOARDING.md');
  }

  const walk = (relDir, depth) => {
    if (depth > MAX_WALK_DEPTH) {
      return;
    }
    const absDir = relDir === '' ? root : join(root, relDir);
    const names = readdirSync(absDir);
    const inCookbooks = basename(relDir) === 'cookbooks';
    const isE2eSuite = names.some((n) => /^playwright\.config\.[cm]?[jt]s$/.test(n));
    for (const name of names) {
      const rel = relDir === '' ? name : join(relDir, name);
      const abs = join(root, rel);
      if (statSync(abs).isDirectory()) {
        if (!SKIP_DIRS.has(name) && !name.startsWith('.')) {
          walk(rel, depth + 1);
        }
        continue;
      }
      if (name === 'AGENTS.md') {
        agents.push(rel);
      } else if (inCookbooks && name.endsWith('.md')) {
        extra.push(rel);
      } else if (isE2eSuite && name === 'README.md' && relDir !== '') {
        extra.push(rel);
      }
    }
  };
  walk('', 0);

  const docs = agents.sort((a, b) => (a === 'AGENTS.md' ? -1 : b === 'AGENTS.md' ? 1 : a.localeCompare(b)))
    .concat(extra);
  return docs;
}

// The generated section repeats the `## Heading` the surrounding template already
// emits just above the marker (a copy marker placed above its heading in the reference file).
// Compares the heading TEXT either side of the marker, so a section opening with a deeper
// subheading of its own is not flagged.
function checkHeadingAfterMarker(rel, text) {
  const lines = text.split('\n');
  const headingText = (line) => {
    const m = (line ?? '').trim().match(/^#{1,6}\s+(\S.*?)\s*$/);
    return m === null ? null : m[1];
  };

  lines.forEach((line, idx) => {
    if (!AUTOGEN_START.test(line)) {
      return;
    }
    const inside = headingText(lines[idx + 1]);
    if (inside === null) {
      return;
    }
    let before = idx - 1;
    while (before >= 0 && lines[before].trim() === '') {
      before -= 1;
    }
    if (before < 0 || headingText(lines[before]) !== inside) {
      return;
    }
    error(rel, idx + 2, 'autogen-duplicate-heading',
      `the heading \`${inside}\` appears both just above the AUTOGEN marker and as the marker's first line, so the reader gets it twice. Move the copy marker in the reference file to BELOW its heading.`);
  });
}

// Same marker definition as checkMarkers.
function sliceMarker(text, name) {
  const start = text.match(autogenMarkerPattern(name, 'START'));
  const end = text.match(autogenMarkerPattern(name, 'END'));
  if (start === null || end === null || end.index < start.index) {
    return null;
  }
  const from = text.indexOf('\n', start.index);
  const body = text.slice(from + 1, end.index).replace(/\r/g, '').trim();
  return body;
}

// Checks that the generated AGENTS.md still states the AI-generation disclosure rule. It checks
// the TEMPLATE, never anyone's commit.
function checkAiDisclosureDocumented(root) {
  const agentsRel = 'AGENTS.md';
  if (!existsSync(join(root, agentsRel))) {
    return;
  }
  const text = readFileSync(join(root, agentsRel), 'utf8');

  const commit = sliceMarker(text, 'commit-message');
  if (commit !== null && !commit.includes('Co-Authored-By:')) {
    error(agentsRel, 1, 'ai-disclosure-commit',
      'the commit message format no longer states the `Co-Authored-By:` AI-generation disclosure. That trailer is how "which model wrote this" stays answerable later; a regeneration that drops the wording drops the rule.');
  }

  const pr = sliceMarker(text, 'pr-description');
  if (pr !== null && !pr.includes('AI-generation disclosure')) {
    error(agentsRel, 1, 'ai-disclosure-pr',
      'the PR description format no longer carries its `AI-generation disclosure` section, so nothing tells an agent to mark a description it wrote.');
  }
}

// Counts lines the way `wc -l` does (newline characters), so the number in the message is the
// number a developer measures. CRLF files count the same because each `\r\n` holds one `\n`.
function countLines(text) {
  const matches = text.match(/\n/g);
  const count = matches === null ? 0 : matches.length;
  return count;
}

// Only the ROOT AGENTS.md is budgeted: subproject files load only when work touches them.
function checkRootLineBudget(root) {
  assert(Number.isInteger(ROOT_LINE_BUDGET) && ROOT_LINE_BUDGET > 0 && ROOT_LINE_BUDGET <= MAX_ROOT_LINE_BUDGET,
    `ROOT_LINE_BUDGET must be a positive integer <= ${MAX_ROOT_LINE_BUDGET}, got ${String(ROOT_LINE_BUDGET)}`);
  const agentsRel = 'AGENTS.md';
  if (!existsSync(join(root, agentsRel))) {
    return;
  }
  const lineCount = countLines(readFileSync(join(root, agentsRel), 'utf8'));
  const softBudget = Math.floor((ROOT_LINE_BUDGET * 3) / 4);
  if (lineCount <= softBudget) {
    return;
  }
  if (lineCount <= ROOT_LINE_BUDGET) {
    warn(agentsRel, softBudget + 1, 'root-line-soft-budget',
      `root AGENTS.md has ${lineCount} lines, past three quarters of its budget of ${ROOT_LINE_BUDGET} (soft limit ${softBudget}). Root holds project-wide rules and navigation; move directory-specific content to that subproject's index or cookbook, and operational detail to .claude/references/, before the ERROR budget is reached.`);
    return;
  }
  const toleranceLimit = Math.floor((ROOT_LINE_BUDGET * 11) / 10);
  if (lineCount <= toleranceLimit) {
    warn(agentsRel, ROOT_LINE_BUDGET + 1, 'root-line-tolerance',
      `root AGENTS.md has ${lineCount} lines, over its budget of ${ROOT_LINE_BUDGET} (by ${lineCount - ROOT_LINE_BUDGET}) but within the 10% tolerance (${toleranceLimit}). Move operational detail, runbooks or history to .claude/references/ and leave a one-line pointer before the ERROR limit is reached.`);
    return;
  }
  error(agentsRel, toleranceLimit + 1, 'root-line-budget',
    `root AGENTS.md has ${lineCount} lines, over its budget of ${ROOT_LINE_BUDGET} plus the 10% tolerance (${toleranceLimit}, by ${lineCount - toleranceLimit}). Move operational detail, runbooks or history to .claude/references/ and leave a one-line pointer; detail is never deleted to meet the budget, it moves (see "Anti-degradation governance").`);
}

// Every file under .claude/ whose name ends in one of `extensions`, node_modules excluded.
function listClaudeFiles(root, extensions) {
  assert(Array.isArray(extensions) && extensions.length > 0, 'listClaudeFiles: extensions must be non-empty');
  const found = [];
  const walk = (relDir, depth) => {
    if (depth > MAX_WALK_DEPTH) {
      return;
    }
    for (const name of readdirSync(join(root, relDir))) {
      const rel = join(relDir, name);
      if (statSync(join(root, rel)).isDirectory()) {
        if (name !== 'node_modules') {
          walk(rel, depth + 1);
        }
        continue;
      }
      if (extensions.some((ext) => name.endsWith(ext))) {
        found.push(rel);
      }
    }
  };
  if (existsSync(join(root, '.claude'))) {
    walk('.claude', 0);
  }
  return found;
}

// Every Markdown file the generator emits that an agent reads: each .md under .claude/
// (references included) plus the generated docs outside it.
function listEmittedMarkdown(root) {
  const all = listClaudeFiles(root, ['.md']).concat(listGeneratedDocs(root));
  return all;
}

// Emitted scripts and JSON, where a leftover token fails nowhere. A guard such as
// startsWith('{{') is not a token.
function listEmittedCode(root) {
  const found = listClaudeFiles(root, ['.mjs', '.js', '.cjs', '.json']);
  if (existsSync(join(root, '.mcp.json'))) {
    found.push('.mcp.json');
  }
  return found;
}

const LEFTOVER_TOKEN = /\{\{[A-Z][A-Z0-9_]*\}\}/g;

// The optional-block names "Strip on emit" defines. Any other `NAME:START` / `NAME:END` comment
// is reported as well; the list only makes the message name the rule. The second group is the
// stack detection blocks of the cookbooks and their thin indexes.
const OPTIONAL_BLOCK_NAMES = new Set([
  'EXTRA_PROFILE', 'E2E', 'WEB_E2E', 'E2E_HELPERS', 'ISSUES', 'BOARD', 'BOARD_MISSING', 'MERGE_MANUAL', 'MERGE_ON_GREEN', 'RCLONE',
  'RCLONE_MISSING',
  'CI_NODE', 'CI_PYTHON',
  'CONTROLLER_LAYER', 'CJS_EXPORTS', 'ROUTER_AUTH', 'LOGGER', 'LOGGER_MISSING', 'STRUCTURED_LOGGER',
  'ERROR_LOOKUP', 'LIB_WRAPPERS', 'AUDIT_LOG', 'CLASS_VALIDATOR', 'I18N', 'ROUTE_GUARDS',
  'ROUTE_DOCS', 'ERROR_SLUGS', 'BASE_DTO', 'ENV_MODULE', 'LIFESPAN_CONF', 'ORM_INIT_LIST', 'ASYNC_DB',
  'REGISTER_JOB', 'JOB_NAME_LITERAL', 'AUTO_DISCOVERY',
]);

// Comments that are emitted on purpose and must survive: AUTOGEN markers, append-below
// sentinels and TODO fill-in hints.
const KEPT_COMMENT = [
  /^AGENTIC-SETUP:AUTOGEN:[A-Za-z0-9_-]+\s+(?:START|END)$/,
  /^Add (?:entries|parity rows) below this line\b/,
  /^Team additions\b/,
  /^TODO\(/,
];

// Generator meta-instructions that "Strip on emit" removes: the KEEP THE FRONTMATTER FIRST block
// and what it holds, the generator-instructions block of each modular reference, single-line
// generator hints, and every template's header comment. Matched only inside HTML comments, so
// prose that merely uses the same words is never flagged.
const MAINTENANCE_COMMENT_PATTERNS = [
  { label: 'KEEP THE FRONTMATTER FIRST', pattern: /KEEP THE FRONTMATTER FIRST/ },
  { label: 'Generated by', pattern: /Generated by ul-[a-z-]+/ },
  { label: 'Generator instructions', pattern: /Generator instructions/ },
  { label: 'NARROW CONTRACT:', pattern: /NARROW CONTRACT:/ },
  { label: 'The generator ... hint', pattern: /^The generator\b/ },
  { label: 'template header', pattern: /\btemplate\b\s*[.,:(]|\btemplate for\b/ },
  { label: 'substitution list', pattern: /\bSubstitution(?:s\b|\s+rules\b)/ },
  { label: 'Emitted ONCE', pattern: /\bEmitted ONCE\b/ },
  { label: 'Verification stub', pattern: /\bVerification stub for\b/ },
  { label: 'THIN index', pattern: /\bTHIN index\b/ },
];

// Distinctive enough to flag anywhere, including a header that leaked out of its comment
// because a `-->` inside it closed the comment early.
const MAINTENANCE_LINE_MARKERS = ['KEEP THE FRONTMATTER FIRST', 'Generator instructions (stripped on copy'];

// HTML comments outside fenced code blocks, with the 1-based line each starts on. A fence shows
// a marker as an example; it is not one.
function extractHtmlComments(lines) {
  assert(Array.isArray(lines), 'extractHtmlComments: lines must be an array');
  const comments = [];
  const insideLines = new Set();
  let inFence = false;
  let open = null;
  lines.forEach((line, idx) => {
    if (open === null && /^\s*(?:```|~~~)/.test(line)) {
      inFence = !inFence;
      return;
    }
    if (inFence) {
      return;
    }
    let rest = line;
    while (rest.length > 0) {
      if (open === null) {
        const at = rest.indexOf('<!--');
        if (at === -1) {
          break;
        }
        open = { line: idx + 1, text: '' };
        rest = rest.slice(at + 4);
      }
      insideLines.add(idx + 1);
      const close = rest.indexOf('-->');
      if (close === -1) {
        open.text += `${rest}\n`;
        break;
      }
      open.text += rest.slice(0, close);
      comments.push({ line: open.line, text: open.text.replace(/\r/g, '').trim() });
      open = null;
      rest = rest.slice(close + 3);
    }
  });
  return { comments, insideLines };
}

function classifyComment(rel, comment) {
  const text = comment.text;
  if (KEPT_COMMENT.some((pattern) => pattern.test(text))) {
    return;
  }
  const block = text.match(/^([A-Z][A-Z0-9_]*):(START|END)$/);
  if (block !== null) {
    const rule = OPTIONAL_BLOCK_NAMES.has(block[1])
      ? 'an optional-block marker from "Strip on emit"'
      : 'an optional-block-shaped marker no strip rule names';
    warn(rel, comment.line, 'optional-block-marker',
      `\`${block[1]}:${block[2]}\` is ${rule}; resolve the block on emit (condition off: delete the block with both markers; on: delete only the two marker lines)`);
    return;
  }
  const delimiter = text.match(/^(TEST-PATTERNS-BLOCK|OPTIONAL-[A-Z0-9-]+)\b/);
  if (delimiter !== null) {
    warn(rel, comment.line, 'delimiter-comment',
      `\`${delimiter[1]}\` is a generator delimiter comment; strip it on emit`);
    return;
  }
  const hit = MAINTENANCE_COMMENT_PATTERNS.find(({ pattern }) => pattern.test(text));
  if (hit !== undefined) {
    warn(rel, comment.line, 'maintenance-comment',
      `HTML comment carries a generator instruction (${hit.label}); strip the whole comment block on emit`);
  }
}

// A user-level skill exists only on the machine that ran the generator; another developer's
// checkout has no such skill, so an emitted file that names one points at nothing.
const USER_LEVEL_SKILL = /(?<![\w/.-])ul-[a-z]+(?:-[a-z]+)+\b/;

// An unsubstituted template token renders as literal braces: a rule that names a path or a
// command nobody can run. WARN, not ERROR: nothing fails to register because of it.
function checkEmittedLeftovers(root) {
  const markdown = [...new Set(listEmittedMarkdown(root))];
  for (const rel of markdown) {
    const lines = readFileSync(join(root, rel), 'utf8').split('\n');
    const { comments, insideLines } = extractHtmlComments(lines);
    lines.forEach((line, idx) => {
      const tokens = line.match(LEFTOVER_TOKEN);
      if (tokens !== null) {
        warn(rel, idx + 1, 'leftover-token',
          `unsubstituted ${[...new Set(tokens)].join(', ')} - the generator left a template token in an emitted file`);
      }
      if (!insideLines.has(idx + 1)) {
        const marker = MAINTENANCE_LINE_MARKERS.find((m) => line.includes(m));
        if (marker !== undefined) {
          warn(rel, idx + 1, 'maintenance-comment',
            `"${marker}" is a generator instruction outside any comment (a header that leaked); strip it on emit`);
        }
      }
      const skill = line.match(USER_LEVEL_SKILL);
      if (skill !== null) {
        warn(rel, idx + 1, 'user-level-skill-reference',
          `names \`${skill[0]}\`, a user-level skill other developers do not have; describe the behaviour instead of naming the skill`);
      }
    });
    for (const comment of comments) {
      classifyComment(rel, comment);
    }
  }

  for (const rel of listEmittedCode(root)) {
    const lines = readFileSync(join(root, rel), 'utf8').split('\n');
    lines.forEach((line, idx) => {
      const tokens = line.match(LEFTOVER_TOKEN);
      if (tokens !== null) {
        warn(rel, idx + 1, 'leftover-token',
          `unsubstituted ${[...new Set(tokens)].join(', ')} - valid JS/JSON, so nothing fails; the value falls back to whatever the unrendered default is`);
      }
    });
  }
}

// A backticked relative path in a cookbook must resolve from the subproject or the repo root.
function checkCookbookPaths(root, rel) {
  const subprojectDir = dirname(dirname(rel));
  const text = readFileSync(join(root, rel), 'utf8');
  const seen = new Set();
  text.split('\n').forEach((line, idx) => {
    for (const m of line.matchAll(/`([^`\s]+)`/g)) {
      const candidate = m[1].replace(/:\d+$/, '');
      const pathShaped = /^\.{0,2}\/?[A-Za-z0-9_@.-]+(?:\/[A-Za-z0-9_@.-]+)+\/?$/.test(candidate);
      const hasFileOrDirEnd = /\.[A-Za-z0-9]{1,6}$/.test(candidate) || candidate.endsWith('/');
      if (!pathShaped || !hasFileOrDirEnd || candidate.startsWith('@') || seen.has(candidate)) {
        continue;
      }
      seen.add(candidate);
      const fromSubproject = join(root, subprojectDir, candidate);
      const fromRoot = join(root, candidate);
      if (!existsSync(fromSubproject) && !existsSync(fromRoot)) {
        warn(rel, idx + 1, 'cookbook-path-missing',
          `\`${candidate}\` resolves neither from ${subprojectDir === '.' ? 'the repo root' : subprojectDir} nor from the repo root; a cookbook names only files that exist`);
      }
    }
  });
}

// Subproject indexes and cookbook fragments load whenever work touches their directory, so each stays
// short. Root has its own budget (checkRootLineBudget).
const INDEX_LINE_BUDGET = 120;
const FRAGMENT_LINE_BUDGET = 120;

function isCookbookFragment(rel) {
  const fragment = basename(dirname(rel)) === 'cookbooks' && rel.endsWith('.md');
  return fragment;
}

function checkSubprojectBudgets(root, docs) {
  assert(Array.isArray(docs), 'checkSubprojectBudgets: docs must be an array');
  for (const rel of docs) {
    const isIndex = basename(rel) === 'AGENTS.md' && rel !== 'AGENTS.md';
    const isFragment = isCookbookFragment(rel);
    if (!isIndex && !isFragment) {
      continue;
    }
    const budget = isIndex ? INDEX_LINE_BUDGET : FRAGMENT_LINE_BUDGET;
    const lineCount = countLines(readFileSync(join(root, rel), 'utf8'));
    if (lineCount <= budget) {
      continue;
    }
    if (isIndex) {
      warn(rel, budget + 1, 'index-line-budget',
        `subproject index has ${lineCount} lines, over its budget of ${budget}. The index keeps commands, file map, verification, a pitfalls summary and the "Read before" table; move detail into the cookbook fragment of its task.`);
    } else {
      warn(rel, budget + 1, 'cookbook-fragment-budget',
        `cookbook fragment has ${lineCount} lines, over its budget of ${budget}. Split it by task into two fragments, each with its own "Read before" row in the subproject index; never trim it.`);
    }
  }
}

function splitTableRow(line) {
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  const cells = trimmed.split('|').map((cell) => cell.trim());
  return cells;
}

function isTableRow(line) {
  const row = typeof line === 'string' && line.trim().startsWith('|');
  return row;
}

// Finds every `| Task | Fragment |` table (matched on its header cells, not on the heading above it) and
// returns the `cookbooks/<file>.md` paths its Fragment column names, with their 1-based lines. Fenced code
// is skipped: a fence shows a table as an example; it is not one.
function parseRoutingTables(text) {
  assert(typeof text === 'string', 'parseRoutingTables: text must be a string');
  const lines = text.replace(/\r/g, '').split('\n');
  const routes = [];
  let found = false;
  let inFence = false;
  for (let i = 0; i < lines.length; i += 1) {
    if (/^\s*(?:```|~~~)/.test(lines[i])) {
      inFence = !inFence;
      continue;
    }
    if (inFence || !isTableRow(lines[i]) || !isTableRow(lines[i + 1])) {
      continue;
    }
    const header = splitTableRow(lines[i]).map((cell) => cell.toLowerCase());
    const fragmentColumn = header.indexOf('fragment');
    if (!header.includes('task') || fragmentColumn === -1) {
      continue;
    }
    found = true;
    let j = i + 2;
    while (j < lines.length && isTableRow(lines[j])) {
      const cell = splitTableRow(lines[j])[fragmentColumn] ?? '';
      for (const m of cell.matchAll(/cookbooks\/[A-Za-z0-9_.-]+\.md/g)) {
        routes.push({ path: m[0], line: j + 1 });
      }
      j += 1;
    }
    i = j - 1;
  }
  return { found, routes };
}

// Every cookbook fragment is routed from its subproject index, and every routed path exists. Grouped by
// the directory that owns `cookbooks/` (`.` for a repo-root cookbooks/ dir).
function checkCookbookRouting(root, docs) {
  assert(Array.isArray(docs), 'checkCookbookRouting: docs must be an array');
  const fragmentsByOwner = new Map();
  for (const rel of docs.filter(isCookbookFragment)) {
    const owner = dirname(dirname(rel));
    fragmentsByOwner.set(owner, (fragmentsByOwner.get(owner) ?? []).concat(rel));
  }
  const owners = new Set(fragmentsByOwner.keys());
  for (const rel of docs.filter((d) => basename(d) === 'AGENTS.md')) {
    owners.add(dirname(rel));
  }

  for (const owner of owners) {
    const indexRel = owner === '.' ? 'AGENTS.md' : join(owner, 'AGENTS.md');
    const fragments = fragmentsByOwner.get(owner) ?? [];
    const indexText = existsSync(join(root, indexRel)) ? readFileSync(join(root, indexRel), 'utf8') : null;
    const routing = indexText === null ? { found: false, routes: [] } : parseRoutingTables(indexText);

    if (!routing.found) {
      if (fragments.length > 0) {
        const names = fragments.map((f) => `cookbooks/${basename(f)}`).join(', ');
        warn(indexText === null ? fragments[0] : indexRel, 1, 'cookbook-routing-table-missing',
          `${owner === '.' ? 'the repo root' : owner} has cookbook fragments (${names}) but ${indexText === null ? 'no AGENTS.md index' : 'its index has no `| Task | Fragment |` "Read before" table'}; add the table and route each fragment to the task it covers.`);
      }
      continue;
    }

    const routed = new Set(routing.routes.map((r) => r.path));
    for (const rel of fragments) {
      const routePath = `cookbooks/${basename(rel)}`;
      if (!routed.has(routePath)) {
        warn(rel, 1, 'cookbook-unrouted',
          `\`${routePath}\` is not named in the "Read before" table of ${indexRel}; add a \`| <task> | ${routePath} |\` row so agents know when to read it.`);
      }
    }
    for (const route of routing.routes) {
      if (!existsSync(join(root, owner, route.path))) {
        warn(indexRel, route.line, 'cookbook-route-missing',
          `the "Read before" table routes to \`${route.path}\`, which does not exist under ${owner === '.' ? 'the repo root' : owner}; fix the row or restore the fragment.`);
      }
    }
  }
}

const GENERATED_MANIFEST_REL = join('.claude', 'references', '.generated.json');
const GENERATED_EDIT_ADVICE = 'repo-specific facts go to an AGENTS.md; a wrong generic rule is fixed in the skill';

// sha256 over the UTF-8 text with every CR byte removed, so LF and CRLF checkouts hash the same. The
// generator records the same hash in .generated.json when it writes the files.
function normalizedSha256(text) {
  assert(typeof text === 'string', 'normalizedSha256: text must be a string');
  const digest = createHash('sha256').update(text.replace(/\r/g, ''), 'utf8').digest('hex');
  return digest;
}

function isSafeRelativePath(path) {
  const safe = typeof path === 'string' && path.length > 0 && !isAbsolute(path)
    && !path.split(/[\\/]/).includes('..');
  return safe;
}

function checkGeneratedManifest(root) {
  if (!existsSync(join(root, GENERATED_MANIFEST_REL))) {
    return;
  }
  let manifest;
  try {
    manifest = JSON.parse(readFileSync(join(root, GENERATED_MANIFEST_REL), 'utf8'));
  } catch (err) {
    warn(GENERATED_MANIFEST_REL, 1, 'generated-manifest-invalid', `not valid JSON (${err.message}); no generated file was checked. Re-run the scaffolding skill to rewrite it.`);
    return;
  }
  if (manifest === null || typeof manifest !== 'object' || Array.isArray(manifest)) {
    warn(GENERATED_MANIFEST_REL, 1, 'generated-manifest-invalid', 'must be an object mapping repo-relative paths to sha256 hex digests; no generated file was checked.');
    return;
  }

  const invalid = [];
  for (const [path, recorded] of Object.entries(manifest)) {
    const recordedHex = typeof recorded === 'string' ? recorded.replace(/^sha256:/, '') : null;
    if (!isSafeRelativePath(path) || recordedHex === null || !/^[0-9a-f]{64}$/.test(recordedHex)) {
      invalid.push(path);
      continue;
    }
    const abs = join(root, path);
    if (!existsSync(abs)) {
      warn(path, 1, 'generated-reference-edited',
        `listed in ${GENERATED_MANIFEST_REL.replace(/\\/g, '/')} as generated, but missing: ${GENERATED_EDIT_ADVICE}.`);
      continue;
    }
    if (normalizedSha256(readFileSync(abs, 'utf8')) !== recordedHex) {
      warn(path, 1, 'generated-reference-edited',
        `differs from what the scaffolding skill emitted (sha256 over the text with CR removed): ${GENERATED_EDIT_ADVICE}.`);
    }
  }
  if (invalid.length > 0) {
    warn(GENERATED_MANIFEST_REL, 1, 'generated-manifest-invalid',
      `entries skipped (need a safe repo-relative path and a "sha256:" plus 64-char lowercase hex digest): ${invalid.map((p) => JSON.stringify(p)).join(', ')}`);
  }
}

function validateGeneratedDocs(root) {
  const references = listFiles(join(root, '.claude', 'references'), '.md')
    .map((name) => join('.claude', 'references', name));
  for (const rel of listGeneratedDocs(root).concat(references)) {
    const text = readFileSync(join(root, rel), 'utf8');
    checkMarkers(rel, text);
    checkHeadingAfterMarker(rel, text);
    if (basename(dirname(rel)) === 'cookbooks') {
      checkCookbookPaths(root, rel);
    }
  }
  checkAiDisclosureDocumented(root);
  checkRootLineBudget(root);
  const docs = listGeneratedDocs(root);
  checkSubprojectBudgets(root, docs);
  checkCookbookRouting(root, docs);
  checkGeneratedManifest(root);
  checkEmittedLeftovers(root);
}

function main() {
  resolveRenderedConstants();
  const root = readFlag('--root', process.cwd());
  const asJson = process.argv.includes('--json');

  const agentsDir = join(root, '.claude', 'agents');
  const skillsDir = join(root, '.claude', 'skills');

  if (!existsSync(join(root, '.claude'))) {
    process.stderr.write(`validate-artifacts.mjs: no .claude directory under ${root}. Nothing to validate.\n`);
    process.exitCode = 1;
    return;
  }

  const agentNames = new Set();
  const agentFiles = listFiles(agentsDir, '.md');
  for (const name of agentFiles) {
    const result = validateAgent(root, join('.claude', 'agents', name));
    if (result !== null) {
      agentNames.add(result.name);
    }
  }

  const skillDirs = listDirs(skillsDir);
  for (const dir of skillDirs) {
    validateSkill(root, join('.claude', 'skills', dir));
  }

  validateHookWiring(root, agentNames);
  validateGeneratedDocs(root);

  const errors = findings.filter((f) => f.level === 'ERROR');
  const warns = findings.filter((f) => f.level === 'WARN');

  if (asJson) {
    process.stdout.write(JSON.stringify({
      root,
      counts: { agents: agentFiles.length, skills: skillDirs.length, errors: errors.length, warnings: warns.length },
      findings,
    }, null, 2) + '\n');
  } else {
    const lines = [];
    lines.push(`validate-artifacts: ${agentFiles.length} agent(s), ${skillDirs.length} skill(s) under ${root}`);
    lines.push('');
    if (findings.length === 0) {
      lines.push('PASS - no structural problems found.');
    } else {
      for (const f of findings) {
        lines.push(`${f.level.padEnd(5)} ${f.file}:${f.line}  [${f.check}]`);
        lines.push(`      ${f.message}`);
      }
      lines.push('');
      lines.push(`${errors.length} error(s), ${warns.length} warning(s).`);
    }
    process.stdout.write(lines.join('\n') + '\n');
  }

  if (errors.length > 0) {
    process.exitCode = 3;
  }
}

try {
  main();
} catch (err) {
  process.stderr.write(`validate-artifacts.mjs failed: ${err.message}\n`);
  process.exitCode = 1;
}
