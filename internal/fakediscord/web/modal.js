// The modal overlay. A modal is a title and text inputs; the page validates them the way the
// Discord client does and submits them as one interaction.

import { make } from './render.js';

const COMPONENT_TEXT_INPUT = 4;
const COMPONENT_LABEL = 18;
const STYLE_PARAGRAPH = 2;
const MAX_DEPTH = 5;
const DEFAULT_MAX_LENGTH = 4000;

// Lists the text inputs of a modal in order, each with the label shown above it.
export function modalInputs(components, label = '', depth = 0) {
  const found = [];
  if (!Array.isArray(components) || depth > MAX_DEPTH) {
    return found;
  }
  for (const component of components) {
    if (component === null || typeof component !== 'object') {
      continue;
    }
    if (component.type === COMPONENT_TEXT_INPUT) {
      const text = typeof component.label === 'string' && component.label !== '' ? component.label : label;
      found.push({ ...component, shownLabel: text });
    }
    const innerLabel = component.type === COMPONENT_LABEL && typeof component.label === 'string' ? component.label : label;
    found.push(...modalInputs(component.components, innerLabel, depth + 1));
    if (component.component) {
      found.push(...modalInputs([component.component], innerLabel, depth + 1));
    }
  }
  return found;
}

function codePointLength(text) {
  return Array.from(text).length;
}

function buildInput(input, index) {
  const row = make('div', 'modal-row');
  const id = 'modal-field-' + index;
  const label = make('label', 'option-label');
  label.setAttribute('for', id);
  const isRequired = input.required !== false;
  label.textContent = String(input.shownLabel || input.custom_id) + (isRequired ? ' *' : '');
  const isParagraph = Number(input.style) === STYLE_PARAGRAPH;
  const control = make(isParagraph ? 'textarea' : 'input', 'option-control', 'modal-field');
  if (!isParagraph) {
    control.setAttribute('type', 'text');
  }
  control.id = id;
  control.setAttribute('data-custom-id', String(input.custom_id));
  if (typeof input.placeholder === 'string') {
    control.setAttribute('placeholder', input.placeholder);
  }
  if (typeof input.value === 'string') {
    control.value = input.value;
  }
  row.append(label, control);
  return { row, control, input, isRequired };
}

function problemsOf(entries) {
  const problems = [];
  for (const { control, input, isRequired } of entries) {
    const name = String(input.shownLabel || input.custom_id);
    const length = codePointLength(control.value);
    const maxLength = Number.isInteger(input.max_length) ? input.max_length : DEFAULT_MAX_LENGTH;
    if (isRequired && length === 0) {
      problems.push(name + ': this field is required.');
    }
    if (length > maxLength) {
      problems.push(name + ': must be ' + maxLength + ' characters or fewer.');
    }
    if (Number.isInteger(input.min_length) && length > 0 && length < input.min_length) {
      problems.push(name + ': must be at least ' + input.min_length + ' characters.');
    }
  }
  return problems;
}

// Creates the modal host inside root.
//
// deps.submit(customId, fields) resolves to { ok, error }.
export function createModal(root, deps) {
  let openCustomId = null;

  function close() {
    openCustomId = null;
    root.replaceChildren();
  }

  function open(modal) {
    if (openCustomId === modal.custom_id) {
      return;
    }
    close();
    openCustomId = modal.custom_id;

    const overlay = make('div', 'overlay');
    const dialog = make('div', 'modal', 'modal');
    dialog.setAttribute('role', 'dialog');
    dialog.setAttribute('aria-modal', 'true');
    dialog.setAttribute('aria-labelledby', 'modal-title');
    dialog.setAttribute('data-custom-id', String(modal.custom_id));

    const title = make('h2', 'modal-title', 'modal-title');
    title.id = 'modal-title';
    title.textContent = String(modal.title || '');
    dialog.append(title);

    const form = document.createElement('form');
    form.noValidate = true;
    const entries = modalInputs(modal.components).map(buildInput);
    for (const entry of entries) {
      form.append(entry.row);
    }

    const error = make('div', 'form-error', 'modal-error');
    error.hidden = true;
    const actions = make('div', 'modal-actions');
    const cancel = make('button', 'btn style-2', 'modal-cancel');
    cancel.setAttribute('type', 'button');
    cancel.textContent = 'Cancel';
    const submit = make('button', 'btn style-1', 'modal-submit');
    submit.setAttribute('type', 'submit');
    submit.textContent = 'Submit';
    actions.append(cancel, submit);
    form.append(error, actions);
    dialog.append(form);
    overlay.append(dialog);
    root.append(overlay);

    const showError = (messages) => {
      error.replaceChildren();
      for (const message of messages) {
        const line = document.createElement('div');
        line.textContent = message;
        error.append(line);
      }
      error.hidden = messages.length === 0;
    };

    cancel.addEventListener('click', close);
    dialog.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        close();
      }
    });
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      const problems = problemsOf(entries);
      showError(problems);
      if (problems.length > 0) {
        return;
      }
      const fields = {};
      for (const { control, input } of entries) {
        fields[input.custom_id] = control.value;
      }
      const customId = modal.custom_id;
      const result = await deps.submit(customId, fields);
      if (openCustomId !== customId) {
        return;
      }
      if (result.ok) {
        close();
      } else {
        showError([result.error || 'The modal could not be submitted.']);
      }
    });

    const first = entries.length > 0 ? entries[0].control : submit;
    first.focus();
  }

  return {
    open,
    close,
    isOpen: () => openCustomId !== null,
  };
}
