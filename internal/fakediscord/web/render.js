// Turns server data into DOM nodes. Everything the server sends reaches the page through
// textContent, createElement or setAttribute on non-URL attributes; no markup is ever built
// from a string, and no link is created from data.

const FLAG_EPHEMERAL = 64;
const FLAG_LOADING = 128;
const MAX_MARKDOWN_LENGTH = 6000;
const MAX_MARKDOWN_DEPTH = 6;
const MAX_COMPONENT_DEPTH = 5;
const ASCII_PUNCTUATION = /[!-/:-@[-`{-~]/;
const MENTION_PATTERN = /<@!?(\d{1,20})>/y;
const TIMESTAMP_PATTERN = /<t:(-?\d{1,13})(?::([tTdDfFR]))?>/y;
const WORD_CHARACTER = /[\p{L}\p{N}]/u;
const WHITESPACE = /\s/;

export function make(tag, className, testId) {
  const node = document.createElement(tag);
  if (className) {
    node.className = className;
  }
  if (testId) {
    node.setAttribute('data-testid', testId);
  }
  return node;
}

const syncKeys = new WeakMap();
const syncSignatures = new WeakMap();

// Makes the children of container match items, in order. An item is
// { key, signature, build }; a child is rebuilt only when its signature changes, so a form
// control inside a stable child keeps its state and focus.
export function syncChildren(container, items) {
  const existing = new Map();
  for (const child of Array.from(container.children)) {
    const key = syncKeys.get(child);
    if (key === undefined) {
      child.remove();
    } else {
      existing.set(key, child);
    }
  }

  const wanted = new Set(items.map((item) => item.key));
  for (const [key, child] of existing) {
    if (!wanted.has(key)) {
      child.remove();
    }
  }

  let previous = null;
  for (const item of items) {
    let node = existing.get(item.key);
    if (!node || syncSignatures.get(node) !== item.signature) {
      const fresh = item.build();
      syncKeys.set(fresh, item.key);
      syncSignatures.set(fresh, item.signature);
      if (node) {
        node.replaceWith(fresh);
      }
      node = fresh;
    }
    const reference = previous ? previous.nextSibling : container.firstChild;
    if (node !== reference) {
      container.insertBefore(node, reference);
    }
    previous = node;
  }
}

// ---- markdown subset: bold, italic, quote, inline code, backslash escapes ----

export function renderMarkdown(text, ctx) {
  const fragment = document.createDocumentFragment();
  const source = typeof text === 'string' ? text : '';
  if (source.length > MAX_MARKDOWN_LENGTH) {
    fragment.append(document.createTextNode(source));
    return fragment;
  }

  let plain = [];
  let quoted = [];
  const flushPlain = () => {
    if (plain.length > 0) {
      appendInline(fragment, plain.join('\n'), ctx, 0);
      plain = [];
    }
  };
  const flushQuote = () => {
    if (quoted.length > 0) {
      const block = make('blockquote', 'quote');
      appendInline(block, quoted.join('\n'), ctx, 0);
      fragment.append(block);
      quoted = [];
    }
  };

  for (const line of source.split('\n')) {
    if (line.startsWith('> ')) {
      flushPlain();
      quoted.push(line.slice(2));
    } else {
      flushQuote();
      plain.push(line);
    }
  }
  flushPlain();
  flushQuote();
  return fragment;
}

function appendInline(parent, text, ctx, depth) {
  let literal = '';
  const flushLiteral = () => {
    if (literal !== '') {
      parent.append(document.createTextNode(literal));
      literal = '';
    }
  };

  let index = 0;
  while (index < text.length) {
    const char = text[index];
    const isEscape = char === '\\' && index + 1 < text.length && ASCII_PUNCTUATION.test(text[index + 1]);
    if (isEscape) {
      literal += text[index + 1];
      index += 2;
      continue;
    }
    const token = readToken(text, index, ctx, depth);
    if (token === null) {
      literal += char;
      index += 1;
      continue;
    }
    flushLiteral();
    parent.append(token.node);
    index = token.end;
  }
  flushLiteral();
}

function readToken(text, index, ctx, depth) {
  switch (text[index]) {
    case '`':
      return readCode(text, index);
    case '<':
      return readMention(text, index, ctx) || readTimestamp(text, index);
    case '*':
      return readEmphasis(text, index, ctx, depth);
    case '_':
      return readUnderscore(text, index, ctx, depth);
    default:
      return null;
  }
}

function backtickRun(text, index) {
  let end = index;
  while (text[end] === '`') {
    end += 1;
  }
  return end - index;
}

// Returns the index of the closing run of exactly `length` backticks, or -1.
function findCodeClose(text, from, length) {
  let cursor = from;
  while (cursor < text.length) {
    const found = text.indexOf('`', cursor);
    if (found === -1) {
      return -1;
    }
    const run = backtickRun(text, found);
    if (run === length) {
      return found;
    }
    cursor = found + run;
  }
  return -1;
}

function readCode(text, index) {
  const length = backtickRun(text, index);
  const close = findCodeClose(text, index + length, length);
  const hasBody = close > index + length;
  if (!hasBody) {
    return null;
  }
  const node = make('code', 'inline-code');
  node.textContent = text.slice(index + length, close);
  return { node, end: close + length };
}

function readMention(text, index, ctx) {
  MENTION_PATTERN.lastIndex = index;
  const match = MENTION_PATTERN.exec(text);
  if (match === null) {
    return null;
  }
  const userId = match[1];
  const name = ctx && ctx.userName ? ctx.userName(userId) : undefined;
  const node = make('span', 'mention', 'mention');
  node.setAttribute('data-user-id', userId);
  node.textContent = '@' + (name || userId);
  return { node, end: MENTION_PATTERN.lastIndex };
}

function readTimestamp(text, index) {
  TIMESTAMP_PATTERN.lastIndex = index;
  const match = TIMESTAMP_PATTERN.exec(text);
  if (match === null) {
    return null;
  }
  const unix = Number(match[1]);
  const date = new Date(unix * 1000);
  if (Number.isNaN(date.getTime())) {
    return null;
  }
  const node = make('time', 'timestamp', 'timestamp');
  node.setAttribute('data-unix', String(unix));
  node.setAttribute('datetime', date.toISOString());
  node.textContent = formatTimestamp(date, match[2] || 'f');
  return { node, end: TIMESTAMP_PATTERN.lastIndex };
}

const TIMESTAMP_FORMATS = {
  t: { timeStyle: 'short' },
  T: { timeStyle: 'medium' },
  d: { dateStyle: 'short' },
  D: { dateStyle: 'long' },
  f: { dateStyle: 'long', timeStyle: 'short' },
  F: { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', hour: 'numeric', minute: '2-digit' },
};

export function formatTimestamp(date, style) {
  if (style === 'R') {
    return formatRelative(date, new Date());
  }
  return new Intl.DateTimeFormat(undefined, TIMESTAMP_FORMATS[style]).format(date);
}

function formatRelative(date, now) {
  const seconds = Math.round((date.getTime() - now.getTime()) / 1000);
  const units = [
    ['year', 31536000],
    ['month', 2592000],
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ];
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return formatter.format(Math.round(seconds / size), unit);
    }
  }
  return formatter.format(seconds, 'second');
}

// Returns the index of the next unescaped `marker` outside inline code, or -1. A single `*`
// does not close on half of a `**`.
function findEmphasisClose(text, from, marker) {
  let cursor = from;
  while (cursor < text.length) {
    const char = text[cursor];
    if (char === '\\') {
      cursor += 2;
      continue;
    }
    if (char === '`') {
      const length = backtickRun(text, cursor);
      const close = findCodeClose(text, cursor + length, length);
      cursor = close === -1 ? cursor + length : close + length;
      continue;
    }
    if (marker === '*' && text.startsWith('**', cursor)) {
      cursor += 2;
      continue;
    }
    if (text.startsWith(marker, cursor)) {
      return cursor;
    }
    cursor += 1;
  }
  return -1;
}

function emphasisNode(tag, text, start, close, markerLength, ctx, depth) {
  const inner = text.slice(start, close);
  if (inner === '' || depth + 1 >= MAX_MARKDOWN_DEPTH) {
    return null;
  }
  const hasEdgeSpace = WHITESPACE.test(inner[0]) || WHITESPACE.test(inner[inner.length - 1]);
  if (hasEdgeSpace) {
    return null;
  }
  const node = make(tag, tag === 'strong' ? 'bold' : 'italic');
  appendInline(node, inner, ctx, depth + 1);
  return { node, end: close + markerLength };
}

function readEmphasis(text, index, ctx, depth) {
  const marker = text.startsWith('**', index) ? '**' : '*';
  const start = index + marker.length;
  const close = findEmphasisClose(text, start, marker);
  if (close === -1) {
    return null;
  }
  return emphasisNode(marker === '**' ? 'strong' : 'em', text, start, close, marker.length, ctx, depth);
}

function readUnderscore(text, index, ctx, depth) {
  if (text.startsWith('__', index)) {
    return { node: document.createTextNode('__'), end: index + 2 };
  }
  const before = index > 0 ? text[index - 1] : '';
  if (before !== '' && WORD_CHARACTER.test(before)) {
    return null;
  }
  const start = index + 1;
  const close = findEmphasisClose(text, start, '_');
  if (close === -1) {
    return null;
  }
  const after = text[close + 1];
  if (after !== undefined && WORD_CHARACTER.test(after)) {
    return null;
  }
  return emphasisNode('em', text, start, close, 1, ctx, depth);
}

// ---- embeds ----

function textBlock(className, testId, value, ctx) {
  const node = make('div', className, testId);
  node.append(renderMarkdown(value, ctx));
  return node;
}

function isNonEmptyString(value) {
  return typeof value === 'string' && value !== '';
}

export function renderEmbed(embed, ctx) {
  const node = make('div', 'embed', 'embed');
  const color = embed.color;
  const isColor = Number.isInteger(color) && color >= 0 && color <= 0xffffff;
  if (isColor) {
    node.style.setProperty('--embed-color', '#' + color.toString(16).padStart(6, '0'));
  }

  if (embed.author && isNonEmptyString(embed.author.name)) {
    node.append(textBlock('embed-author', null, embed.author.name, ctx));
  }
  if (isNonEmptyString(embed.title)) {
    node.append(textBlock('embed-title', 'embed-title', embed.title, ctx));
  }
  if (isNonEmptyString(embed.description)) {
    node.append(textBlock('embed-description', 'embed-description', embed.description, ctx));
  }

  const fields = Array.isArray(embed.fields) ? embed.fields : [];
  if (fields.length > 0) {
    const grid = make('div', 'embed-fields');
    for (const field of fields) {
      const cell = make('div', field.inline ? 'embed-field inline' : 'embed-field', 'embed-field');
      cell.append(textBlock('embed-field-name', 'embed-field-name', field.name, ctx));
      cell.append(textBlock('embed-field-value', 'embed-field-value', field.value, ctx));
      grid.append(cell);
    }
    node.append(grid);
  }
  if (embed.footer && isNonEmptyString(embed.footer.text)) {
    node.append(textBlock('embed-footer', null, embed.footer.text, ctx));
  }
  return node;
}

// ---- components ----

function buttonLabel(component) {
  if (isNonEmptyString(component.label)) {
    return component.label;
  }
  if (component.emoji && isNonEmptyString(component.emoji.name)) {
    return component.emoji.name;
  }
  return '';
}

function renderButton(component, message, actions) {
  const node = make('button', 'btn style-' + Number(component.style || 1), 'component-button');
  node.setAttribute('type', 'button');
  if (isNonEmptyString(component.custom_id)) {
    node.setAttribute('data-custom-id', component.custom_id);
  }
  const label = buttonLabel(component);
  node.textContent = label;
  if (label === '') {
    node.setAttribute('aria-label', 'button');
  }
  // A link button opens a URL on Discord. The page never navigates to data it was given.
  const isLink = Number(component.style) === 5;
  if (component.disabled || isLink || !isNonEmptyString(component.custom_id)) {
    node.disabled = true;
    return node;
  }
  node.addEventListener('click', () => {
    actions.onButton(message.id, component.custom_id);
  });
  return node;
}

function selectedValues(select) {
  return Array.from(select.selectedOptions)
    .map((option) => option.value)
    .filter((value) => value !== '');
}

function renderSelect(component, message, actions) {
  const maxValues = Number(component.max_values || 1);
  const isMultiple = maxValues > 1;
  const node = make('select', 'menu', 'component-select');
  node.setAttribute('data-custom-id', String(component.custom_id || ''));
  const hasPlaceholder = isNonEmptyString(component.placeholder);
  node.setAttribute('aria-label', hasPlaceholder ? component.placeholder : String(component.custom_id || 'menu'));
  const options = Array.isArray(component.options) ? component.options : [];
  if (isMultiple) {
    node.multiple = true;
    node.size = Math.min(Math.max(options.length, 2), 6);
  } else {
    const placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.disabled = true;
    placeholder.textContent = hasPlaceholder ? component.placeholder : 'Select an option';
    node.append(placeholder);
  }
  for (const option of options) {
    const item = document.createElement('option');
    item.value = String(option.value);
    item.textContent = String(option.label);
    item.selected = Boolean(option.default);
    node.append(item);
  }
  if (!isMultiple && selectedValues(node).length === 0) {
    node.selectedIndex = 0;
  }
  const defaults = Array.from(node.options).map((item) => item.selected);

  const canAct = !component.disabled && isNonEmptyString(component.custom_id);
  node.disabled = !canAct;
  node.addEventListener('change', () => {
    const values = selectedValues(node);
    // The menu returns to its defaults so the same choice can be made again.
    Array.from(node.options).forEach((item, position) => {
      item.selected = defaults[position];
    });
    if (values.length > 0) {
      actions.onSelect(message.id, component.custom_id, values);
    }
  });
  return node;
}

function renderComponent(component, message, ctx, depth) {
  if (component === null || typeof component !== 'object' || depth > MAX_COMPONENT_DEPTH) {
    return null;
  }
  switch (component.type) {
    case 2:
      return renderButton(component, message, ctx.actions);
    case 3:
      return renderSelect(component, message, ctx.actions);
    case 10:
      return textBlock('text-display', null, component.content, ctx);
    default:
      break;
  }
  if (Array.isArray(component.components)) {
    const group = make('div', component.type === 1 ? 'action-row' : 'component-group');
    for (const child of component.components) {
      const rendered = renderComponent(child, message, ctx, depth + 1);
      if (rendered !== null) {
        group.append(rendered);
      }
    }
    return group;
  }
  const unsupported = make('div', 'unsupported');
  unsupported.textContent = 'Unsupported component';
  return unsupported;
}

function renderComponents(components, message, ctx) {
  const holder = make('div', 'components');
  for (const component of components) {
    const rendered = renderComponent(component, message, ctx, 0);
    if (rendered !== null) {
      holder.append(rendered);
    }
  }
  return holder;
}

// ---- messages ----

function authorLine(message) {
  const line = make('div', 'message-header');
  const author = message.author || {};
  const name = make('span', 'author');
  name.textContent = String(author.username || 'unknown');
  line.append(name);
  if (author.bot) {
    const tag = make('span', 'app-tag');
    tag.textContent = 'APP';
    line.append(tag);
  }
  const when = new Date(message.timestamp);
  if (!Number.isNaN(when.getTime())) {
    const stamp = make('span', 'sent-at');
    stamp.textContent = when.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    line.append(stamp);
  }
  return line;
}

function messageShell(authorName, isEphemeral) {
  const node = make('article', 'message', 'message');
  node.setAttribute('data-ephemeral', isEphemeral ? 'true' : 'false');
  node.setAttribute('data-author', authorName);
  return node;
}

function thinkingBody() {
  const body = make('div', 'thinking', 'deferred');
  body.textContent = 'Chatisimo is thinking...';
  return body;
}

// A message that has no content yet and carries the loading flag is Discord's "thinking"
// placeholder.
function isLoadingPlaceholder(message) {
  const hasBody = message.content || (message.embeds || []).length > 0 || (message.components || []).length > 0;
  return (message.flags & FLAG_LOADING) !== 0 && !hasBody;
}

// The page's own placeholder for an interaction the bot deferred and has not yet answered.
export function renderThinking(botName, interactionId) {
  const node = messageShell(botName, false);
  node.setAttribute('data-interaction-id', interactionId);
  const line = make('div', 'message-header');
  const name = make('span', 'author');
  name.textContent = botName;
  line.append(name);
  node.append(line, thinkingBody());
  return node;
}

// ctx is { viewerId, userName(id), actions: { onButton, onSelect } }.
export function renderMessage(message, ctx) {
  const isEphemeral = (message.flags & FLAG_EPHEMERAL) !== 0;
  const author = message.author || {};
  const node = messageShell(String(author.username || ''), isEphemeral);
  node.setAttribute('data-message-id', String(message.id));
  node.append(authorLine(message));

  if (isLoadingPlaceholder(message)) {
    node.append(thinkingBody());
    return node;
  }

  if (isNonEmptyString(message.content)) {
    node.append(textBlock('message-content', 'message-content', message.content, ctx));
  }
  for (const embed of message.embeds || []) {
    node.append(renderEmbed(embed, ctx));
  }
  if ((message.components || []).length > 0) {
    node.append(renderComponents(message.components, message, ctx));
  }

  const pings = Array.isArray(message.pings) ? message.pings : [];
  if (ctx.viewerId && pings.includes(ctx.viewerId)) {
    const badge = make('span', 'ping-badge', 'ping-badge');
    badge.textContent = '@ You were pinged';
    node.append(badge);
  }
  if (isEphemeral) {
    const note = make('div', 'ephemeral-note', 'ephemeral-note');
    note.textContent = 'Only you can see this';
    node.append(note);
  }
  return node;
}

export function renderNotice(testId, text) {
  const node = make('div', 'notice', testId);
  node.setAttribute('role', 'status');
  node.textContent = text;
  return node;
}
