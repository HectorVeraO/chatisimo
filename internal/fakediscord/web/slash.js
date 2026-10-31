// The slash command composer. The command tree comes from GET /_control/commands, so the
// page never hard-codes a command.

import { make } from './render.js';

const OPTION_STRING = 3;
const OPTION_INTEGER = 4;
const OPTION_BOOLEAN = 5;
const OPTION_USER = 6;
const OPTION_CHANNEL = 7;
const OPTION_NUMBER = 10;
const SUBCOMMAND = 1;
const SUBCOMMAND_GROUP = 2;
const CONTEXT_GUILD = 0;
const CONTEXT_BOT_DM = 1;
const MAX_TREE_DEPTH = 3;
const MAX_CHOICES = 25;
const AUTOCOMPLETE_DELAY_MS = 250;

// ---- the command tree ----

// Flattens command definitions into one node per command, group and subcommand. A node is
// { path: ['ge', 'join'], description, contexts, isLeaf, options }.
export function buildNodes(definitions) {
  const nodes = [];
  for (const definition of Array.isArray(definitions) ? definitions : []) {
    const isChatInput = definition && typeof definition.name === 'string' && (definition.type === undefined || definition.type === 1);
    if (isChatInput) {
      const contexts = Array.isArray(definition.contexts) ? definition.contexts : null;
      collectNodes(definition, [definition.name], contexts, nodes);
    }
  }
  return nodes;
}

function collectNodes(definition, path, contexts, nodes) {
  const options = Array.isArray(definition.options) ? definition.options : [];
  const children = options.filter((option) => option.type === SUBCOMMAND || option.type === SUBCOMMAND_GROUP);
  const isLeaf = children.length === 0;
  nodes.push({
    path,
    description: typeof definition.description === 'string' ? definition.description : '',
    contexts,
    isLeaf,
    options: isLeaf ? options : [],
  });
  if (path.length >= MAX_TREE_DEPTH) {
    return;
  }
  for (const child of children) {
    collectNodes(child, [...path, child.name], contexts, nodes);
  }
}

// "/ge/join" and "/ge  join" both become "ge join"; text that is not a command becomes null.
export function normalizeInput(value) {
  if (typeof value !== 'string' || !value.startsWith('/')) {
    return null;
  }
  return value.slice(1).replace(/\//g, ' ').replace(/\s+/g, ' ').replace(/^ /, '').toLowerCase();
}

function isAvailable(node, inDirectMessage) {
  if (node.contexts === null) {
    return true;
  }
  return node.contexts.includes(inDirectMessage ? CONTEXT_BOT_DM : CONTEXT_GUILD);
}

// The nodes one level below what has been typed whose path starts with the typed text.
export function suggest(nodes, value, inDirectMessage) {
  const typed = normalizeInput(value);
  if (typed === null) {
    return [];
  }
  const depth = typed.split(' ').length;
  return nodes.filter((node) => {
    const isAtDepth = node.path.length === depth;
    return isAtDepth && isAvailable(node, inDirectMessage) && node.path.join(' ').startsWith(typed);
  });
}

// The subcommand the typed text names exactly, if any.
export function resolveLeaf(nodes, value, inDirectMessage) {
  const typed = normalizeInput(value);
  if (typed === null) {
    return null;
  }
  const wanted = typed.trimEnd();
  const found = nodes.find((node) => node.isLeaf && isAvailable(node, inDirectMessage) && node.path.join(' ') === wanted);
  return found || null;
}

function codePointLength(text) {
  return Array.from(text).length;
}

// ---- option fields ----

function isNumeric(definition) {
  return definition.type === OPTION_INTEGER || definition.type === OPTION_NUMBER;
}

function isSupported(definition) {
  return (
    definition.type === OPTION_STRING ||
    definition.type === OPTION_BOOLEAN ||
    definition.type === OPTION_USER ||
    definition.type === OPTION_CHANNEL ||
    isNumeric(definition)
  );
}

// Required options come first, each group in its registered order.
function orderedOptions(options) {
  const supported = options.filter(isSupported);
  return [...supported.filter((o) => o.required), ...supported.filter((o) => !o.required)];
}

function labelFor(definition, controlId) {
  const label = make('label', 'option-label');
  label.setAttribute('for', controlId);
  label.textContent = String(definition.name) + (definition.required ? ' *' : '');
  if (typeof definition.description === 'string') {
    label.title = definition.description;
  }
  return label;
}

function populateUsers(select, users, keep) {
  const previous = keep === undefined ? select.value : keep;
  const blank = document.createElement('option');
  blank.value = '';
  blank.textContent = 'Select a user';
  select.replaceChildren(blank);
  for (const user of users) {
    const option = document.createElement('option');
    option.value = user.id;
    option.textContent = user.name;
    select.append(option);
  }
  const stillThere = users.some((user) => user.id === previous);
  select.value = stillThere ? previous : '';
}

function populateChannels(select, channels, keep) {
  const previous = keep === undefined ? select.value : keep;
  const blank = document.createElement('option');
  blank.value = '';
  blank.textContent = 'Select a channel';
  select.replaceChildren(blank);
  for (const channel of channels) {
    const option = document.createElement('option');
    option.value = channel.id;
    option.textContent = channel.name;
    select.append(option);
  }
  const stillThere = channels.some((channel) => channel.id === previous);
  select.value = stillThere ? previous : '';
}

// Creates the composer behind the static elements of the page.
//
// elements: { form, input, suggestions, options, error }
// deps: {
//   getNodes(), isDirectMessage(), getUsers(),
//   autocomplete(path, options, focused) -> Promise<choices>,
//   submit(path, options) -> Promise<boolean>   // true clears the composer
// }
export function createComposer(elements, deps) {
  let activeLeaf = null;
  let fields = [];
  let optionCounter = 0;

  function showErrors(messages) {
    elements.error.replaceChildren();
    for (const message of messages) {
      const line = document.createElement('div');
      line.textContent = message;
      elements.error.append(line);
    }
    elements.error.hidden = messages.length === 0;
  }

  function currentPath() {
    return '/' + activeLeaf.path.join('/');
  }

  function readValue(field) {
    const { definition, control } = field;
    if (definition.type === OPTION_BOOLEAN) {
      return control.checked || definition.required ? control.checked : undefined;
    }
    const raw = control.value;
    if (raw === '') {
      return undefined;
    }
    if (field.choiceName !== null && raw === field.choiceName) {
      return field.choiceValue;
    }
    if (isNumeric(definition)) {
      return raw.trim() === '' ? Number.NaN : Number(raw);
    }
    return raw;
  }

  function collectValues(exceptName) {
    const values = {};
    for (const field of fields) {
      const value = readValue(field);
      const isUsable = value !== undefined && !(typeof value === 'number' && Number.isNaN(value));
      if (isUsable && field.definition.name !== exceptName) {
        values[field.definition.name] = value;
      }
    }
    return values;
  }

  // Every problem is reported together, as the Discord client does.
  function validate() {
    const problems = [];
    if (activeLeaf === null) {
      problems.push('Choose a command from the list.');
      return problems;
    }
    for (const field of fields) {
      const { definition } = field;
      const name = definition.name;
      const value = readValue(field);
      if (value === undefined) {
        if (definition.required) {
          problems.push(name + ': this option is required.');
        }
        continue;
      }
      if (typeof value === 'number' && Number.isNaN(value)) {
        problems.push(name + ': must be a number.');
      }
      if (typeof value === 'string') {
        const length = codePointLength(value);
        if (Number.isInteger(definition.max_length) && length > definition.max_length) {
          problems.push(name + ': must be ' + definition.max_length + ' characters or fewer.');
        }
        if (Number.isInteger(definition.min_length) && length < definition.min_length) {
          problems.push(name + ': must be at least ' + definition.min_length + ' characters.');
        }
      }
    }
    return problems;
  }

  function hideChoices(field) {
    field.list.hidden = true;
    field.list.replaceChildren();
  }

  function showChoices(field, choices) {
    field.list.replaceChildren();
    const shown = (Array.isArray(choices) ? choices : []).slice(0, MAX_CHOICES);
    for (const choice of shown) {
      const button = make('button', 'choice', 'autocomplete-choice');
      button.setAttribute('type', 'button');
      button.setAttribute('aria-label', String(choice.name));
      button.textContent = String(choice.name);
      button.addEventListener('click', () => {
        field.control.value = String(choice.name);
        field.choiceName = String(choice.name);
        field.choiceValue = choice.value;
        hideChoices(field);
        field.skipNextFocus = true;
        field.control.focus();
        field.skipNextFocus = false;
      });
      field.list.append(button);
    }
    field.list.hidden = shown.length === 0;
  }

  async function runAutocomplete(field) {
    field.sequence += 1;
    const sequence = field.sequence;
    const options = collectValues(field.definition.name);
    options[field.definition.name] = field.control.value;
    try {
      const choices = await deps.autocomplete(currentPath(), options, field.definition.name);
      if (sequence === field.sequence) {
        showChoices(field, choices);
      }
    } catch {
      if (sequence === field.sequence) {
        hideChoices(field);
      }
    }
  }

  function scheduleAutocomplete(field) {
    clearTimeout(field.timer);
    field.timer = setTimeout(() => {
      runAutocomplete(field);
    }, AUTOCOMPLETE_DELAY_MS);
  }

  function buildField(definition) {
    optionCounter += 1;
    const controlId = 'slash-option-' + optionCounter;
    const row = make('div', 'option-row');
    row.append(labelFor(definition, controlId));

    let control;
    if (definition.type === OPTION_BOOLEAN) {
      control = make('input', 'option-control', 'slash-option');
      control.setAttribute('type', 'checkbox');
    } else if (definition.type === OPTION_USER) {
      control = make('select', 'option-control', 'slash-option');
      populateUsers(control, deps.getUsers());
    } else if (definition.type === OPTION_CHANNEL) {
      control = make('select', 'option-control', 'slash-option');
      populateChannels(control, deps.getChannels ? deps.getChannels() : []);
    } else {
      control = make('input', 'option-control', 'slash-option');
      control.setAttribute('type', 'text');
      control.setAttribute('autocomplete', 'off');
    }
    control.id = controlId;
    control.setAttribute('data-option', definition.name);
    row.append(control);

    const field = { definition, control, row, choiceName: null, choiceValue: null, list: null, timer: null, sequence: 0, skipNextFocus: false };
    const hasAutocomplete = definition.autocomplete === true && definition.type === OPTION_STRING;
    if (hasAutocomplete) {
      field.list = make('div', 'autocomplete-list', 'autocomplete-list');
      field.list.setAttribute('role', 'listbox');
      field.list.hidden = true;
      row.append(field.list);
      control.addEventListener('input', () => {
        if (control.value !== field.choiceName) {
          field.choiceName = null;
          field.choiceValue = null;
        }
        scheduleAutocomplete(field);
      });
      control.addEventListener('focus', () => {
        if (!field.skipNextFocus) {
          scheduleAutocomplete(field);
        }
      });
    }
    return field;
  }

  function disposeFields() {
    for (const field of fields) {
      clearTimeout(field.timer);
      field.sequence += 1;
    }
    fields = [];
    elements.options.replaceChildren();
  }

  function showForm(leaf) {
    disposeFields();
    activeLeaf = leaf;
    if (leaf === null) {
      return;
    }
    fields = orderedOptions(leaf.options).map(buildField);
    for (const field of fields) {
      elements.options.append(field.row);
    }
  }

  function renderSuggestions() {
    const matches = suggest(deps.getNodes(), elements.input.value, deps.isDirectMessage());
    elements.suggestions.replaceChildren();
    for (const node of matches) {
      const spaced = '/' + node.path.join(' ');
      const button = make('button', 'suggestion', 'slash-suggestion');
      button.setAttribute('type', 'button');
      button.setAttribute('aria-label', spaced);
      button.setAttribute('data-path', '/' + node.path.join('/'));
      const name = document.createElement('span');
      name.className = 'suggestion-name';
      name.textContent = spaced;
      const description = document.createElement('span');
      description.className = 'suggestion-description';
      description.textContent = node.description;
      button.append(name, description);
      button.addEventListener('click', () => {
        pick(node);
      });
      elements.suggestions.append(button);
    }
    elements.suggestions.hidden = matches.length === 0;
    return matches;
  }

  function pick(node) {
    const suffix = node.isLeaf ? '' : ' ';
    elements.input.value = '/' + node.path.join(' ') + suffix;
    elements.input.focus();
    update();
  }

  function update() {
    const leaf = resolveLeaf(deps.getNodes(), elements.input.value, deps.isDirectMessage());
    const changed = (leaf === null ? '' : leaf.path.join('/')) !== (activeLeaf === null ? '' : activeLeaf.path.join('/'));
    if (changed) {
      showForm(leaf);
    }
    renderSuggestions();
  }

  function clear() {
    elements.input.value = '';
    showForm(null);
    showErrors([]);
    renderSuggestions();
  }

  elements.input.addEventListener('input', () => {
    showErrors([]);
    update();
  });
  elements.input.addEventListener('focus', () => {
    renderSuggestions();
  });
  elements.input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && activeLeaf === null) {
      const matches = renderSuggestions();
      event.preventDefault();
      if (matches.length > 0) {
        pick(matches[0]);
      }
    }
    if (event.key === 'Escape') {
      elements.suggestions.hidden = true;
    }
  });

  elements.form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const problems = validate();
    showErrors(problems);
    if (problems.length > 0) {
      return;
    }
    for (const field of fields) {
      if (field.list) {
        hideChoices(field);
      }
    }
    const shouldClear = await deps.submit(currentPath(), collectValues(null));
    if (shouldClear) {
      clear();
    }
  });

  return {
    // Shows an error from outside, such as the fake refusing an interaction.
    showErrors,
    // Re-reads the tree, the channel kind and the users after any of them changed.
    refresh() {
      const leaf = resolveLeaf(deps.getNodes(), elements.input.value, deps.isDirectMessage());
      const stillValid = leaf !== null && activeLeaf !== null && leaf.path.join('/') === activeLeaf.path.join('/');
      if (!stillValid) {
        showForm(leaf);
      }
      for (const field of fields) {
        if (field.definition.type === OPTION_USER) {
          populateUsers(field.control, deps.getUsers());
        } else if (field.definition.type === OPTION_CHANNEL) {
          populateChannels(field.control, deps.getChannels ? deps.getChannels() : []);
        }
      }
      renderSuggestions();
    },
    clear,
  };
}
