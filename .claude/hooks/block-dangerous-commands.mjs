/**
 * PreToolUse hook for Bash AND PowerShell.
 *
 * Blocks shell commands that lose work that cannot be recovered, rewrite shared history,
 * bypass a verification gate, stage secrets with git, sweep a formatter/linter across the
 * whole tree, or recursively delete `.git`, a drive root, the home directory or the repo
 * root. Rebuildable local state (containers, volumes, dev databases, node_modules, build
 * output) is not blocked. A lease-protected force push to a developer's own work branch
 * is allowed.
 *
 * Matching is done on shell tokens, never on the raw command string. The command is split
 * into segments at `;`, `&`, `|`, newlines, parentheses and braces; quotes group text into
 * one token, so a quoted commit message or search pattern that mentions `--no-verify` or
 * `git reset --hard` is an argument, not a flag, and passes. The tool of each segment is
 * identified by its lowercased basename without a Windows extension, so `git.exe`, `Git`
 * and `/usr/bin/git` are the same tool, and git global options (`-C <dir>`, `-c k=v`,
 * `--git-dir=...`) are skipped before the subcommand is read. Flags match in any position.
 *
 * The `deny` rules in settings.json cover some plain spellings; this hook covers what prefix
 * globs cannot reach: flags in any order or position, commands further into a pipeline or
 * inside `bash -c` / `pwsh -Command` / `cmd /c`, and the PowerShell `Remove-Item` alias family.
 *
 * **Both shells must be matched.** The settings.json matcher is `Bash|PowerShell`.
 *
 * Claude Code delivers the tool payload as JSON on stdin. It does NOT set
 * CLAUDE_TOOL_INPUT_COMMAND.
 *
 * Exit codes: 0 permits the call, 2 blocks it and surfaces stderr to the model. Any
 * internal error also exits 2: a guardrail that crashed has not cleared the call.
 */

import { Buffer } from 'node:buffer';

// Package runners that may precede the invoked tool, and the subcommands that mean
// "run a binary" rather than "run a script" (`npm run lint` is left alone on purpose).
const RUNNERS = new Set(['npx', 'pnpx', 'bunx', 'npm', 'pnpm', 'yarn', 'bun']);
const RUNNER_SUBCOMMANDS = new Set(['exec', 'dlx', 'x']);

// Prefix commands that run the next word as the real tool.
const WRAPPERS = new Set(['sudo', 'doas', 'env', 'command', 'builtin', 'exec', 'nohup', 'time', 'nice', 'xargs']);

// Path targets that mean "the whole tree", compared after normalizePath. A closed set on
// purpose: `./src/a.ts` normalizes to `src/a.ts` and is still an explicit file.
const WHOLE_TREE_TARGETS = new Set(['.', '..', '*', '**', '**/*']);

// Options that consume the NEXT token as their value, per tool. That value is never a
// path target, so `eslint -c . src/a.ts` must not read the `.` as a sweep. The
// `--flag=value` spelling is already a single flag token and needs no entry here.
const VALUE_FLAGS = {
  eslint: new Set([
    '-c', '--config', '--resolve-plugins-relative-to', '--ignore-path', '--cache-location',
    '--rulesdir', '--parser', '--plugin', '--format', '-f', '--output-file', '-o', '--ext',
    '--max-warnings', '--env', '--global', '--parser-options', '--print-config',
  ]),
  prettier: new Set([
    '--config', '--ignore-path', '--plugin', '--parser', '--log-level', '--cache-location',
    '--config-precedence',
  ]),
};

// An eslint run carrying one of these prints information and lints nothing, so running it
// without path targets is not a sweep.
const ESLINT_INFO_FLAGS = new Set([
  '--version', '-v', '--help', '-h', '--print-config', '--init', '--env-info', '--inspect-config',
]);

// Git global options that consume the next token as their value.
const GIT_GLOBAL_VALUE_OPTIONS = new Set(['-C', '-c', '--git-dir', '--work-tree', '--namespace', '--config-env', '--exec-path']);

// `git commit` short options that take a value: attached (`-mmsg`) or as the next token.
const COMMIT_VALUE_SHORT = new Set(['m', 'F', 'C', 'c', 't']);
// `git commit` short options with an optional attached value only, which end a cluster.
const COMMIT_ATTACHED_SHORT = new Set(['S', 'u']);
const COMMIT_VALUE_LONG = new Set([
  '--message', '--file', '--reuse-message', '--reedit-message', '--template', '--author',
  '--date', '--fixup', '--squash', '--cleanup', '--pathspec-from-file', '--trailer',
]);
const PUSH_VALUE_OPTIONS = new Set(['-o', '--push-option', '--repo', '--receive-pack', '--exec']);

// Every name under which a recursive delete is reachable: POSIX rm, cmd rmdir/rd/del/erase,
// and the PowerShell Remove-Item cmdlet with its aliases (`rm`, `rmdir`, `del` are aliases
// of Remove-Item in PowerShell).
const REMOVE_TOOLS = new Set(['rm', 'rmdir', 'rd', 'del', 'erase', 'ri', 'remove-item']);
// The cmd.exe spellings, whose `/s` and `/q` switches are not paths.
const CMD_REMOVE_TOOLS = new Set(['rmdir', 'rd', 'del', 'erase']);

// Shells whose argument is itself a command line, re-analyzed with a depth cap.
const POSIX_SHELLS = new Set(['bash', 'sh', 'zsh', 'dash', 'ksh']);
const POWERSHELLS = new Set(['pwsh', 'powershell']);
const MAX_NESTING_DEPTH = 3;

// Flags that skip a verification gate, blocked for every tool when given as a flag token.
const GATE_SKIP_FLAGS = new Set(['--no-verify', '--skip-pre-commit']);

// Placeholder argument for a `(...)`, `$(...)` or backtick substitution whose output the
// hook cannot know.
const SUBSTITUTION = '<substitution>';

// Same boundaries as block-protected-paths: `.env` and `.env.local` are caught, `.envrc`
// is not; any path under a `secrets` directory is caught. An env template (`.example.env`,
// `.prod.example.env`, legacy `.env.example`) is not an env file.
const ENV_FILE_PATTERN = /(^|[\\/])\.env($|\.)/i;
const ENV_TEMPLATE_PATTERN = /(^|[\\/])[^\\/]*\.example(\.[^\\/]*)?$/i;
const SECRETS_DIR_PATTERN = /(^|[\\/])secrets[\\/]/i;

// Branch prefixes owned by one developer, where a lease-protected force push is allowed.
const WORK_BRANCH_PREFIXES = ['feature/', 'bugfix/'];

// Recursive-delete targets naming the home directory, compared lowercased.
const HOME_TARGETS = new Set([
  '~', '$home', '${home}', '$env:userprofile', '$env:home', '${env:userprofile}', '${env:home}', '%userprofile%',
]);

const BLOCKED_HANDOFF = 'If it is really needed, hand it to the developer as root AGENTS.md -> "Blocked actions" describes.';

/**
 * Splits a command line into segments of unquoted tokens.
 *
 * Each segment is `{ tokens, writes, pipedFromPrevious, groupStart }`. Segments end at
 * unquoted `;`, `&`, `|`, newlines, `(`, `)`, `{` and `}`. A group is a run of segments
 * joined by a single `|` or by parentheses and braces; `;`, `&&`, `||`, `&` and newlines
 * start a new group. Output redirects (`>`, `>>`, `N>`, `*>`, `&>`) record their target
 * in `writes` and drop the pending fd token (`2`, `*`); `>&N` is an fd duplication with no
 * target, and an input redirect target (`<`) is dropped. A bash heredoc body is skipped up
 * to its delimiter line, so prose inside it is never read as commands.
 *
 * With `backtickEscapes` true, a backtick escapes the next character (PowerShell); with
 * false, it separates segments (bash command substitution). The caller analyzes both.
 *
 * @param {string} command the raw shell command.
 * @param {boolean} backtickEscapes how to read an unquoted backtick.
 * @returns {{tokens: string[], writes: string[], groupStart: boolean}[]} the segments.
 */
function tokenizeSegments(command, backtickEscapes) {
  console.assert(typeof command === 'string', 'command must be a string');
  const segments = [];
  let tokens = [];
  let writes = [];
  let current = '';
  let hasToken = false;
  let quote = '';
  let target = '';
  let heredocs = [];
  let nextGroupStart = true;

  const flushToken = () => {
    if (hasToken) {
      if (target === 'write') {
        writes.push(current);
      } else if (target === '') {
        tokens.push(current);
      }
      target = '';
    }
    current = '';
    hasToken = false;
  };
  const flushSegment = (startsNewGroup) => {
    flushToken();
    target = '';
    segments.push({ tokens, writes, groupStart: nextGroupStart });
    tokens = [];
    writes = [];
    nextGroupStart = startsNewGroup;
  };

  let index = 0;
  while (index < command.length) {
    const ch = command[index];
    const next = command[index + 1];
    if (quote) {
      if (ch === quote) {
        quote = '';
      } else {
        current += ch;
      }
      index += 1;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      hasToken = true;
      index += 1;
    } else if (ch === '`' && backtickEscapes) {
      if (next !== undefined && next !== '\n') {
        current += next;
        hasToken = true;
      }
      index += 2;
    } else if (ch === '\n') {
      flushSegment(true);
      index = skipHeredocBodies(command, index + 1, heredocs);
      heredocs = [];
    } else if (ch === ';') {
      flushSegment(true);
      index += 1;
    } else if (ch === '&' && next === '>') {
      flushToken();
      index = consumeOutputRedirect(command, index + 1);
      target = 'write';
    } else if (ch === '&' || ch === '|') {
      const doubled = next === ch;
      flushSegment(ch === '&' || doubled);
      index += doubled ? 2 : 1;
    } else if (ch === '{' && current.endsWith('$')) {
      // `${HOME}` and `${env:HOME}` are one variable reference, not a brace group.
      const close = command.indexOf('}', index);
      const end = close === -1 ? command.length : close + 1;
      current += command.slice(index, end);
      hasToken = true;
      index = end;
    } else if (ch === '(' || ch === ')' || ch === '{' || ch === '}' || ch === '`') {
      // An opening substitution stands in for arguments the hook cannot see, so the
      // segment it ends keeps a placeholder argument instead of reading as argument-less.
      if (ch === '(' || ch === '`') {
        flushToken();
        tokens.push(SUBSTITUTION);
      }
      flushSegment(false);
      index += 1;
    } else if (ch === '>') {
      if (/^(\d+|\*)$/.test(current)) {
        current = '';
        hasToken = false;
      }
      flushToken();
      const afterRedirect = consumeOutputRedirect(command, index);
      if (command[afterRedirect] === '&') {
        let skip = afterRedirect + 1;
        while (skip < command.length && /[\d-]/.test(command[skip])) {
          skip += 1;
        }
        index = skip;
      } else {
        index = afterRedirect;
        target = 'write';
      }
    } else if (ch === '<') {
      flushToken();
      if (next === '<' && command[index + 2] === '<') {
        index += 3;
        target = 'drop';
      } else if (next === '<') {
        const heredoc = readHeredocDelimiter(command, index + 2);
        heredocs.push(heredoc.delimiter);
        index = heredoc.end;
      } else {
        index += 1;
        target = 'drop';
      }
    } else if (/\s/.test(ch)) {
      flushToken();
      index += 1;
    } else {
      current += ch;
      hasToken = true;
      index += 1;
    }
  }
  flushSegment(true);
  return segments.filter((segment) => segment.tokens.length > 0 || segment.writes.length > 0);
}

/**
 * Consumes a `>` or `>>` (and a trailing `|`) starting at `index`.
 *
 * @param {string} command the raw shell command.
 * @param {number} index the position of the first `>`.
 * @returns {number} the position after the redirect operator.
 */
function consumeOutputRedirect(command, index) {
  let position = index;
  while (command[position] === '>') {
    position += 1;
  }
  if (command[position] === '|') {
    position += 1;
  }
  return position;
}

/**
 * Reads a heredoc delimiter after `<<` (optionally `<<-`), unquoting it.
 *
 * @param {string} command the raw shell command.
 * @param {number} index the position right after `<<`.
 * @returns {{delimiter: string, end: number}} the delimiter and the position after it.
 */
function readHeredocDelimiter(command, index) {
  let position = index;
  if (command[position] === '-') {
    position += 1;
  }
  while (position < command.length && (command[position] === ' ' || command[position] === '\t')) {
    position += 1;
  }
  let delimiter = '';
  while (position < command.length && !/[\s;&|<>()]/.test(command[position])) {
    if (command[position] !== '"' && command[position] !== "'" && command[position] !== '\\') {
      delimiter += command[position];
    }
    position += 1;
  }
  return { delimiter, end: position };
}

/**
 * Skips the bodies of pending heredocs, each ending at a line equal to its delimiter.
 *
 * @param {string} command the raw shell command.
 * @param {number} index the start of the line after the heredoc operator.
 * @param {string[]} delimiters the pending delimiters, in order.
 * @returns {number} the position after the last skipped body.
 */
function skipHeredocBodies(command, index, delimiters) {
  let position = index;
  for (const delimiter of delimiters) {
    if (delimiter === '') {
      continue;
    }
    for (;;) {
      if (position >= command.length) {
        return command.length;
      }
      const lineEnd = command.indexOf('\n', position);
      const end = lineEnd === -1 ? command.length : lineEnd;
      const line = command.slice(position, end);
      position = end + 1;
      if (line.trim() === delimiter) {
        break;
      }
    }
  }
  return position;
}

/**
 * Returns the lowercased basename of a command token without a Windows shim extension,
 * so `node_modules/.bin/eslint.cmd`, `git.exe` and `C:\Git\bin\Git.EXE` are recognized.
 *
 * @param {string} token a command token.
 * @returns {string} the normalized tool name.
 */
function toolName(token) {
  const basename = token.replace(/\\/g, '/').split('/').pop();
  const name = basename.toLowerCase().replace(/\.(cmd|exe|js|bat|ps1)$/, '');
  return name;
}

/**
 * Returns the index of the invoked tool within a segment's tokens: leading `NAME=value`
 * assignments, the PowerShell call operator, prefix wrappers (`sudo`, `env`), a package
 * runner, their flags and the runner's exec-style subcommand are skipped.
 *
 * @param {string[]} tokens one segment's tokens.
 * @returns {number} the tool index, or tokens.length when the segment has no tool.
 */
function toolIndex(tokens) {
  let index = 0;
  for (;;) {
    while (index < tokens.length && (/^[A-Za-z_][A-Za-z0-9_]*=/.test(tokens[index]) || tokens[index] === '.')) {
      index += 1;
    }
    if (index >= tokens.length || !WRAPPERS.has(toolName(tokens[index]))) {
      break;
    }
    index += 1;
    while (index < tokens.length && tokens[index].startsWith('-')) {
      index += 1;
    }
  }
  if (index < tokens.length && RUNNERS.has(toolName(tokens[index]))) {
    index += 1;
    while (index < tokens.length
      && (tokens[index].startsWith('-') || RUNNER_SUBCOMMANDS.has(tokens[index].toLowerCase()))) {
      index += 1;
    }
  }
  return index;
}

/**
 * Normalizes a path argument: `\` becomes `/`, empty and `.` segments are dropped, and an
 * argument made only of those normalizes to `.`. So `./`, `./.` and `.\` all become `.`.
 *
 * @param {string} arg a path argument.
 * @returns {string} the normalized path.
 */
function normalizePath(arg) {
  const parts = arg.replace(/\\/g, '/').split('/').filter((part) => part !== '' && part !== '.');
  const normalized = parts.length === 0 ? '.' : parts.join('/');
  return normalized;
}

/**
 * True when a path names a `.git` directory or anything inside one: a segment equal to
 * `.git`, or starting with `.git` followed by a non-word character (`.git-backup`),
 * case-insensitively. `.gitignore` and `.github` are not `.git`.
 *
 * @param {string} arg a path argument.
 * @returns {boolean} whether the path reaches a `.git` directory.
 */
function reachesGitDir(arg) {
  const parts = arg.replace(/\\/g, '/').split('/');
  const reaches = parts.some((part) => /^\.git(?![\w])/i.test(part));
  return reaches;
}

/**
 * True when a delete target names a filesystem or drive root (`/`, `C:\`, Git Bash `/c`),
 * the home directory (`~`, `$HOME`, `$env:USERPROFILE`) or the current directory (`.`),
 * with or without trailing `/`, `/.` or `/*`.
 *
 * @param {string} arg a path argument.
 * @returns {boolean} whether the delete would wipe one of those.
 */
function isRootTarget(arg) {
  const slashed = arg.replace(/\\/g, '/');
  let trimmed = slashed;
  while (/(\/|\/\.|\/\*)$/.test(trimmed)) {
    trimmed = trimmed.replace(/(\/|\/\.|\/\*)$/, '');
  }
  if (trimmed === '' && slashed.startsWith('/')) {
    return true;
  }
  const lower = trimmed.toLowerCase();
  const isRoot = lower === '.' || HOME_TARGETS.has(lower) || /^[a-z]:$/.test(lower) || /^\/[a-z]$/.test(lower);
  return isRoot;
}

/**
 * Returns the path targets of an eslint or prettier invocation: every non-flag argument,
 * except the token that follows a value-taking option of that tool (see VALUE_FLAGS).
 *
 * @param {string[]} args the tokens after the tool name.
 * @param {string} tool 'eslint' or 'prettier'.
 * @returns {string[]} the arguments that name paths.
 */
function pathTargets(args, tool) {
  const valueFlags = VALUE_FLAGS[tool];
  console.assert(valueFlags !== undefined, `no value-flag table for ${tool}`);
  const targets = [];
  let skipNext = false;
  for (const arg of args) {
    if (skipNext) {
      skipNext = false;
    } else if (arg.startsWith('-')) {
      skipNext = valueFlags.has(arg);
    } else {
      targets.push(arg);
    }
  }
  return targets;
}

/**
 * Detects a prettier `--write` / `-w` or an eslint run (with or without `--fix`) whose
 * path targets cover the whole tree. An eslint run with no path target lints the working
 * directory and counts as a sweep, unless it only prints information (`--version`).
 * An option missing from VALUE_FLAGS whose value is literally `.` is still read as a
 * target -- a false positive, never a missed sweep.
 *
 * @param {string} tool the normalized tool name.
 * @param {string[]} args the tokens after the tool name.
 * @returns {string|null} the reason, or null when there is no sweep.
 */
function sweepReason(tool, args) {
  if (tool !== 'prettier' && tool !== 'eslint') {
    return null;
  }
  if (tool === 'prettier' && !args.some((arg) => arg === '--write' || arg === '-w')) {
    return null;
  }
  const targets = pathTargets(args, tool).map(normalizePath);
  if (tool === 'eslint' && targets.length === 0 && !args.some((arg) => ESLINT_INFO_FLAGS.has(arg))) {
    return 'eslint with no path target lints the whole working directory. Pass the changed files (`git diff --name-only`).';
  }
  const sweeps = targets.some((target) => WHOLE_TREE_TARGETS.has(target) || target.startsWith('**/'));
  if (sweeps) {
    return `${tool} runs across the whole tree. Pass the changed files (\`git diff --name-only\`) instead of \`.\` or \`*\`.`;
  }
  return null;
}

/**
 * Splits git arguments into the subcommand and its arguments, skipping global options
 * such as `-C <dir>`, `-c <key=value>`, `--git-dir=<dir>` and `--no-pager`.
 *
 * @param {string[]} args the tokens after `git`.
 * @returns {{subcommand: string, rest: string[]}} the subcommand ('' when absent) and its arguments.
 */
function gitSubcommand(args) {
  let index = 0;
  const configs = [];
  while (index < args.length && args[index].startsWith('-')) {
    const option = args[index];
    const consumesValue = GIT_GLOBAL_VALUE_OPTIONS.has(option);
    if (option === '-c' && args[index + 1] !== undefined) {
      configs.push(args[index + 1]);
    }
    index += consumesValue ? 2 : 1;
  }
  if (index >= args.length) {
    return { subcommand: '', rest: [], configs };
  }
  return { subcommand: args[index].toLowerCase(), rest: args.slice(index + 1), configs };
}

/**
 * Splits subcommand arguments into flags and positionals. Tokens after `--` are
 * positionals, and the value of an option listed in `valueOptions` is neither.
 *
 * @param {string[]} rest the arguments after the subcommand.
 * @param {Set<string>} valueOptions options that consume the next token.
 * @returns {{flags: string[], positionals: string[]}} the split.
 */
function splitArgs(rest, valueOptions) {
  const flags = [];
  const positionals = [];
  let afterDoubleDash = false;
  let skipNext = false;
  for (const arg of rest) {
    if (skipNext) {
      skipNext = false;
    } else if (afterDoubleDash) {
      positionals.push(arg);
    } else if (arg === '--') {
      afterDoubleDash = true;
    } else if (arg.startsWith('-')) {
      flags.push(arg);
      skipNext = valueOptions.has(arg);
    } else {
      positionals.push(arg);
    }
  }
  return { flags, positionals };
}

/**
 * True when a flag list carries the short letter `letter`, alone or inside a cluster
 * such as `-fd`. Long flags never match.
 *
 * @param {string[]} flags the flags of one invocation.
 * @param {string} letter a single option letter (case-sensitive).
 * @returns {boolean} whether the letter is set.
 */
function hasShortFlag(flags, letter) {
  const found = flags.some((flag) => /^-[A-Za-z]+$/.test(flag) && flag.slice(1).includes(letter));
  return found;
}

/**
 * True when a `git commit` argument list skips the hooks with `-n`, alone or in a
 * cluster (`-an`), without reading a message value (`-m -n`, `-mn`) as the flag.
 *
 * @param {string[]} rest the arguments after `commit`.
 * @returns {boolean} whether `-n` is set.
 */
function commitSkipsHooks(rest) {
  let skipNext = false;
  for (const arg of rest) {
    if (skipNext) {
      skipNext = false;
      continue;
    }
    if (arg === '--') {
      return false;
    }
    if (COMMIT_VALUE_LONG.has(arg)) {
      skipNext = true;
      continue;
    }
    if (!/^-[A-Za-z]/.test(arg) || arg.startsWith('--')) {
      continue;
    }
    for (let position = 1; position < arg.length; position += 1) {
      const letter = arg[position];
      if (letter === 'n') {
        return true;
      }
      if (COMMIT_VALUE_SHORT.has(letter)) {
        skipNext = position === arg.length - 1;
        break;
      }
      if (COMMIT_ATTACHED_SHORT.has(letter)) {
        break;
      }
    }
  }
  return false;
}

/**
 * True when a path is an environment file or lies under a `secrets` directory. An env
 * template carrying an `.example` segment is neither.
 *
 * @param {string} arg a path argument.
 * @returns {boolean} whether staging the path would stage a secret.
 */
function isSecretPath(arg) {
  if (SECRETS_DIR_PATTERN.test(arg)) {
    return true;
  }
  const secret = ENV_FILE_PATTERN.test(arg) && !ENV_TEMPLATE_PATTERN.test(arg);
  return secret;
}

/**
 * Returns the branch a push refspec writes to: the part after `:`, else the whole refspec,
 * without a `refs/heads/` prefix.
 *
 * @param {string} refspec one push refspec.
 * @returns {string} the destination branch.
 */
function pushDestination(refspec) {
  const colon = refspec.lastIndexOf(':');
  const destination = colon === -1 ? refspec : refspec.slice(colon + 1);
  const branch = destination.replace(/^refs\/heads\//, '');
  return branch;
}

/**
 * Returns why a `git push` is blocked, or null. `--force`, `-f` and a `+refspec` are
 * always blocked. `--force-with-lease` is allowed only when every refspec is named and
 * writes to a developer's own work branch (WORK_BRANCH_PREFIXES).
 *
 * @param {string[]} rest the arguments after `push`.
 * @returns {string|null} the reason.
 */
function pushReason(rest) {
  const { flags, positionals } = splitArgs(rest, PUSH_VALUE_OPTIONS);
  const plainForce = flags.includes('--force') || hasShortFlag(flags, 'f');
  if (plainForce || positionals.some((arg) => arg.startsWith('+'))) {
    return 'git push with --force, -f or a +refspec rewrites shared history. On your own work branch use `git push --force-with-lease origin <branch>`.';
  }
  const leased = flags.some((flag) => flag === '--force-with-lease' || flag.startsWith('--force-with-lease='));
  if (!leased) {
    return null;
  }
  const allowedPrefixes = WORK_BRANCH_PREFIXES.join(', ');
  const refspecs = positionals.slice(1);
  const pushesMany = flags.some((flag) => ['--all', '--mirror', '--tags', '--branches'].includes(flag));
  if (refspecs.length === 0 || pushesMany) {
    return `git push --force-with-lease needs the branch named: \`git push --force-with-lease origin <branch>\` (${allowedPrefixes} only).`;
  }
  const shared = refspecs.map(pushDestination).find((branch) => !WORK_BRANCH_PREFIXES.some((prefix) => branch.startsWith(prefix) && branch.length > prefix.length));
  if (shared !== undefined) {
    return `git push --force-with-lease to ${shared} rewrites a shared branch; it is allowed only on ${allowedPrefixes} branches.`;
  }
  return null;
}

/**
 * Returns why a git invocation is blocked, or null when it is allowed.
 *
 * @param {string[]} args the tokens after `git`.
 * @returns {string|null} the reason.
 */
function gitReason(args) {
  const { subcommand, rest, configs } = gitSubcommand(args);
  if (subcommand === '') {
    return null;
  }
  if (configs.some((config) => /^core\.hookspath=/i.test(config))) {
    return 'git -c core.hooksPath=... redirects the verification hooks away.';
  }
  if (subcommand === 'commit' && commitSkipsHooks(rest)) {
    return 'git commit -n skips the verification hooks.';
  }
  if (subcommand === 'push') {
    const reason = pushReason(rest);
    if (reason) {
      return reason;
    }
  }
  if (subcommand === 'reset' && rest.includes('--hard')) {
    return 'git reset --hard discards uncommitted work. Use `git stash -u`, which can be undone.';
  }
  if (subcommand === 'branch') {
    const flags = splitArgs(rest, new Set()).flags;
    const deletes = flags.includes('--delete') || hasShortFlag(flags, 'd');
    const forces = flags.includes('--force') || hasShortFlag(flags, 'f');
    if (hasShortFlag(flags, 'D') || (deletes && forces)) {
      return 'git branch -D / --delete --force deletes a branch whether or not it is merged. Use `git branch -d` for a merged branch.';
    }
  }
  if (subcommand === 'add' || subcommand === 'stage') {
    const { positionals } = splitArgs(rest, new Set(['--pathspec-from-file', '--chmod']));
    const secret = positionals.find(isSecretPath);
    if (secret) {
      return `git ${subcommand} ${secret} stages an environment file or a secret.`;
    }
  }
  return null;
}

/**
 * True when a remove-family invocation carries a recursive signal: `--recursive`, cmd
 * `/s`, a PowerShell prefix of `-Recurse` (`-r`, `-Rec`, `-Recurse:$true`), or a POSIX
 * short cluster made only of rm letters that contains `r`/`R` (`-rf`, `-fr`, `-Rv`).
 * `-Force` is not a cluster: its letters are not all rm letters.
 *
 * @param {string[]} args the tokens after the tool name.
 * @returns {boolean} whether the delete is recursive.
 */
function removesRecursively(args) {
  const recursive = args.some((arg) => {
    const lower = arg.toLowerCase().split(':')[0];
    if (lower === '--recursive' || lower === '/s') {
      return true;
    }
    if (/^-[rRfivdI]+$/.test(arg) && /[rR]/.test(arg)) {
      return true;
    }
    return lower.length >= 2 && !lower.startsWith('--') && '-recurse'.startsWith(lower);
  });
  return recursive;
}

/**
 * Returns why a segment is blocked, or null. `groupReachesGitDir` says whether any
 * segment of the same pipeline group names a `.git` path, which is how
 * `gci .git | Remove-Item -Recurse` is caught: the delete has no path of its own.
 *
 * @param {{tokens: string[], writes: string[]}} segment one segment.
 * @param {boolean} groupReachesGitDir whether the segment's group names a `.git` path.
 * @param {number} depth the nesting depth of the command being analyzed.
 * @returns {string|null} the reason.
 */
function segmentReason(segment, groupReachesGitDir, depth) {
  const index = toolIndex(segment.tokens);
  if (index >= segment.tokens.length) {
    return null;
  }
  const tool = toolName(segment.tokens[index]);
  const args = segment.tokens.slice(index + 1);

  if (args.includes('--break-system-packages')) {
    return '--break-system-packages installs into the system interpreter. Use a virtualenv.';
  }
  const gateSkip = args.find((arg) => GATE_SKIP_FLAGS.has(arg));
  if (gateSkip) {
    return `${gateSkip} skips the verification hooks.`;
  }
  if (tool === 'git') {
    return gitReason(args);
  }
  if (REMOVE_TOOLS.has(tool) && removesRecursively(args)) {
    const cmdSwitches = CMD_REMOVE_TOOLS.has(tool);
    const positionals = args.filter((arg) => !arg.startsWith('-') && !(cmdSwitches && /^\/[a-z]$/i.test(arg)));
    const onlyPiped = positionals.every((arg) => arg === SUBSTITUTION || arg === '$' || arg === '$_');
    if (positionals.some(reachesGitDir) || (onlyPiped && groupReachesGitDir)) {
      return 'a recursive delete of .git destroys every unpushed commit and stash.';
    }
    const root = positionals.find(isRootTarget);
    if (root !== undefined) {
      return `a recursive delete of ${root} wipes a drive root, the home directory or the whole repo. Delete the specific subdirectory instead.`;
    }
  }
  const nested = nestedCommand(tool, args);
  if (nested !== null) {
    return commandReason(nested, depth + 1);
  }
  return sweepReason(tool, args);
}

/**
 * Returns the command line a shell invocation runs (`bash -c "<cmd>"`,
 * `pwsh -Command <cmd>`, `pwsh -EncodedCommand <b64>`, `cmd /c <cmd>`), or null.
 *
 * @param {string} tool the normalized tool name.
 * @param {string[]} args the tokens after the tool name.
 * @returns {string|null} the nested command line.
 */
function nestedCommand(tool, args) {
  if (POSIX_SHELLS.has(tool)) {
    const flagIndex = args.findIndex((arg) => /^-[a-z]*c[a-z]*$/.test(arg));
    const script = flagIndex >= 0 ? args[flagIndex + 1] : undefined;
    return script === undefined ? null : script;
  }
  if (POWERSHELLS.has(tool)) {
    const encodedIndex = args.findIndex((arg) => /^-(e|ec|enc\w*)$/i.test(arg));
    if (encodedIndex >= 0 && args[encodedIndex + 1] !== undefined) {
      const decoded = Buffer.from(args[encodedIndex + 1], 'base64').toString('utf16le');
      return decoded;
    }
    const commandIndex = args.findIndex((arg) => arg.length >= 2 && '-command'.startsWith(arg.toLowerCase()));
    return commandIndex >= 0 ? args.slice(commandIndex + 1).join(' ') : null;
  }
  if (tool === 'cmd') {
    const commandIndex = args.findIndex((arg) => /^\/[ck]$/i.test(arg));
    return commandIndex >= 0 ? args.slice(commandIndex + 1).join(' ') : null;
  }
  return null;
}

/**
 * Returns why a command line is blocked, or null when it is allowed. The command is read
 * twice, with a backtick as PowerShell escape and as bash command substitution, and is
 * blocked when either reading is.
 *
 * @param {string} command the raw shell command.
 * @param {number} depth the nesting depth, capped at MAX_NESTING_DEPTH.
 * @returns {string|null} the reason.
 */
function commandReason(command, depth) {
  if (depth > MAX_NESTING_DEPTH) {
    return `the command nests shells more than ${MAX_NESTING_DEPTH} levels deep, too deep to inspect.`;
  }
  for (const backtickEscapes of [true, false]) {
    const segments = tokenizeSegments(command, backtickEscapes);
    const groups = [];
    for (const segment of segments) {
      if (segment.groupStart || groups.length === 0) {
        groups.push([]);
      }
      groups[groups.length - 1].push(segment);
    }
    for (const group of groups) {
      const groupReachesGitDir = group.some((segment) => segment.tokens.some(reachesGitDir));
      for (const segment of group) {
        const reason = segmentReason(segment, groupReachesGitDir, depth);
        if (reason) {
          return reason;
        }
      }
    }
  }
  return null;
}

/**
 * Reads the entire stdin stream. Returns an empty string when stdin is closed,
 * which keeps the hook fail-open on a harness that supplies no payload.
 */
async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) {
    chunks.push(chunk);
  }
  const joined = Buffer.concat(chunks).toString('utf8');
  return joined;
}

async function main() {
  const raw = await readStdin();
  if (!raw.trim()) {
    process.exit(0);
  }

  let payload;
  try {
    payload = JSON.parse(raw);
  } catch {
    console.error('block-dangerous-commands: could not parse the hook payload as JSON; refusing to guess.');
    process.exit(2);
  }
  if (payload === null || typeof payload !== 'object' || Array.isArray(payload)) {
    console.error('block-dangerous-commands: the hook payload is not a JSON object; refusing to guess.');
    process.exit(2);
  }

  const toolInput = payload.tool_input;
  const command = toolInput && typeof toolInput === 'object' && typeof toolInput.command === 'string'
    ? toolInput.command
    : '';
  if (!command) {
    process.exit(0);
  }

  const reason = commandReason(command, 0);
  if (reason) {
    console.error(`Blocked: "${command}": ${reason} ${BLOCKED_HANDOFF}`);
    process.exit(2);
  }

  process.exit(0);
}

main().catch((error) => {
  console.error(`block-dangerous-commands: internal error, blocking the call: ${error && error.stack ? error.stack : error}`);
  process.exit(2);
});
