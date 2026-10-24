// Page state and wiring. The page plays several synthetic users against /_control: it reads
// the world, opens one event stream for the selected user, and sends interactions.

import * as api from './api.js';
import { make, renderMessage, renderNotice, renderThinking, syncChildren } from './render.js';
import { createComposer, buildNodes } from './slash.js';
import { createModal } from './modal.js';

const DM_LABEL = 'Chatisimo';
const NO_RESPONSE_TEXT = 'The application did not respond';
const CALLBACK_DEFER_MESSAGE = 5;
const SCROLL_SLACK_PX = 80;

const state = {
  users: [],
  guilds: [],
  bot: 'absent',
  nodes: [],
  userId: null,
  // Either { kind: 'guild', guildId, channelId } or { kind: 'dm' }.
  view: { kind: 'dm' },
  selectedGuildId: null,
  messages: [],
  placeholders: [],
  notices: [],
  unread: 0,
  botName: DM_LABEL,
  stream: null,
  isLoading: false,
  loadAgain: false,
  noticeCounter: 0,
};

const $ = (id) => document.getElementById(id);
const dom = {
  userSwitcher: $('user-switcher'),
  newUserForm: $('new-user-form'),
  newUserName: $('new-user-name'),
  botStatus: $('bot-status'),
  serverList: $('server-list'),
  newServerForm: $('new-server-form'),
  newServerName: $('new-server-name'),
  memberForm: $('member-form'),
  memberServer: $('member-server'),
  memberList: $('member-list'),
  memberSelect: $('member-select'),
  dmLink: $('dm-link'),
  dmBadge: $('dm-badge'),
  channelHeader: $('channel-header'),
  appError: $('app-error'),
  messageList: $('message-list'),
  composer: $('composer'),
  slashInput: $('slash-input'),
  slashSuggestions: $('slash-suggestions'),
  slashOptions: $('slash-options'),
  slashError: $('slash-error'),
  modalRoot: $('modal-root'),
};

// ---- small helpers ----

function showError(message) {
  dom.appError.textContent = message;
  dom.appError.hidden = false;
}

function clearError() {
  dom.appError.textContent = '';
  dom.appError.hidden = true;
}

function userById(id) {
  return state.users.find((user) => user.id === id);
}

function userName(id) {
  const user = userById(id);
  return user ? user.name : undefined;
}

function guildById(id) {
  return state.guilds.find((guild) => guild.id === id);
}

function memberGuilds() {
  return state.guilds.filter((guild) => guild.members.includes(state.userId));
}

function describeError(error) {
  return error instanceof Error ? error.message : String(error);
}

// Rebuilds the options of a select only when they changed, then selects the value.
function fillSelect(select, options, selected) {
  const signature = JSON.stringify(options);
  if (select.getAttribute('data-signature') !== signature) {
    select.replaceChildren();
    for (const { value, label, disabled } of options) {
      const option = document.createElement('option');
      option.value = value;
      option.textContent = label;
      option.disabled = Boolean(disabled);
      select.append(option);
    }
    select.setAttribute('data-signature', signature);
  }
  select.value = selected;
}

// ---- rendering of the chrome ----

function renderTopbar() {
  const options = state.users.map((user) => ({ value: user.id, label: user.name }));
  fillSelect(dom.userSwitcher, options, state.userId || '');
  dom.botStatus.textContent = state.bot;
}

function isActiveGuildChannel(guild) {
  return state.view.kind === 'guild' && state.view.guildId === guild.id;
}

function renderServers() {
  const items = memberGuilds().map((guild) => ({
    key: guild.id,
    signature: JSON.stringify([guild.name, guild.channel_id, guild.channel, isActiveGuildChannel(guild), guild.id === state.selectedGuildId]),
    build: () => {
      const item = make('li', 'server-item', 'server-item');
      item.setAttribute('data-guild-id', guild.id);
      const name = make('div', 'server-name');
      name.textContent = guild.name;
      const link = make('button', 'channel-link', 'channel-link');
      link.setAttribute('type', 'button');
      link.setAttribute('data-channel-id', guild.channel_id);
      link.textContent = '#' + guild.channel;
      if (isActiveGuildChannel(guild)) {
        link.setAttribute('aria-current', 'page');
      }
      link.addEventListener('click', () => {
        openGuild(guild);
      });
      item.append(name, link);
      return item;
    },
  }));
  syncChildren(dom.serverList, items);
}

function selectedGuild() {
  const candidates = memberGuilds();
  const chosen = candidates.find((guild) => guild.id === state.selectedGuildId);
  return chosen || candidates[0] || null;
}

function renderMembers() {
  const guild = selectedGuild();
  dom.memberServer.textContent = guild ? guild.name : 'no server';
  const members = guild ? guild.members.map((id) => ({ id, name: userName(id) || id })) : [];
  syncChildren(
    dom.memberList,
    members.map((member) => ({
      key: member.id,
      signature: member.name,
      build: () => {
        const item = make('li', 'member', 'member');
        item.setAttribute('data-user-id', member.id);
        item.textContent = member.name;
        return item;
      },
    })),
  );

  const candidates = state.users
    .filter((user) => !guild || !guild.members.includes(user.id))
    .map((user) => ({ value: user.id, label: user.name }));
  const options = [{ value: '', label: 'Select a user' }, ...candidates];
  const previous = dom.memberSelect.value;
  const keep = candidates.some((option) => option.value === previous) ? previous : '';
  fillSelect(dom.memberSelect, options, keep);
}

function renderDirectMessages() {
  if (state.view.kind === 'dm') {
    dom.dmLink.setAttribute('aria-current', 'page');
  } else {
    dom.dmLink.removeAttribute('aria-current');
  }
  dom.dmBadge.replaceChildren();
  if (state.unread > 0) {
    const badge = make('span', 'unread', 'dm-unread');
    badge.textContent = String(state.unread);
    dom.dmBadge.append(badge);
  }
}

function renderHeader() {
  if (state.view.kind === 'dm') {
    dom.channelHeader.textContent = DM_LABEL;
    return;
  }
  const guild = guildById(state.view.guildId);
  dom.channelHeader.textContent = '#' + (guild ? guild.channel : 'general');
}

function renderChrome() {
  renderTopbar();
  renderServers();
  renderMembers();
  renderDirectMessages();
  renderHeader();
  composer.refresh();
}

// ---- messages ----

function messageContext() {
  return { viewerId: state.userId, userName, actions: { onButton, onSelect } };
}

function messageSignature(message) {
  const names = state.users.map((user) => user.id + ':' + user.name).join(',');
  return JSON.stringify(message) + '|' + state.userId + '|' + names;
}

function renderMessages() {
  const answered = new Set(state.messages.map((message) => message.interaction_id).filter(Boolean));
  state.placeholders = state.placeholders.filter((placeholder) => !answered.has(placeholder.interactionId));

  const ctx = messageContext();
  const items = state.messages.map((message) => ({
    key: 'm:' + message.id,
    signature: messageSignature(message),
    build: () => renderMessage(message, ctx),
  }));
  for (const placeholder of state.placeholders) {
    items.push({
      key: 'p:' + placeholder.interactionId,
      signature: state.botName,
      build: () => renderThinking(state.botName, placeholder.interactionId),
    });
  }
  for (const notice of state.notices) {
    items.push({
      key: 'n:' + notice.id,
      signature: NO_RESPONSE_TEXT,
      build: () => renderNotice('interaction-failed', NO_RESPONSE_TEXT),
    });
  }

  const list = dom.messageList;
  const wasNearBottom = list.scrollHeight - list.scrollTop - list.clientHeight < SCROLL_SLACK_PX;
  const countBefore = list.children.length;
  syncChildren(list, items);
  if (wasNearBottom || list.children.length > countBefore) {
    list.scrollTop = list.scrollHeight;
  }
}

async function reloadMessages() {
  if (state.userId === null) {
    return;
  }
  if (state.isLoading) {
    state.loadAgain = true;
    return;
  }
  state.isLoading = true;
  try {
    do {
      state.loadAgain = false;
      const requestedUser = state.userId;
      const requestedView = state.view;
      if (requestedUser === null) {
        break;
      }
      const list =
        requestedView.kind === 'dm'
          ? await api.getDirectMessages(requestedUser)
          : await api.getChannelMessages(requestedView.channelId, requestedUser);
      const isCurrent = state.userId === requestedUser && state.view === requestedView;
      if (isCurrent) {
        state.messages = list;
        const bot = list.find((message) => message.author && message.author.bot);
        if (bot) {
          state.botName = bot.author.username;
        }
        renderMessages();
      }
    } while (state.loadAgain);
  } catch (error) {
    showError(describeError(error));
  } finally {
    state.isLoading = false;
  }
}

// ---- views and users ----

function resetViewState() {
  state.messages = [];
  state.placeholders = [];
  state.notices = [];
  renderMessages();
}

function enterView(view) {
  state.view = view;
  if (view.kind === 'dm') {
    state.unread = 0;
  } else {
    state.selectedGuildId = view.guildId;
  }
  resetViewState();
  renderChrome();
  reloadMessages();
}

function guildView(guild) {
  return { kind: 'guild', guildId: guild.id, channelId: guild.channel_id };
}

function openGuild(guild) {
  clearError();
  enterView(guildView(guild));
}

function openDirectMessages() {
  clearError();
  enterView({ kind: 'dm' });
}

function defaultView() {
  const first = memberGuilds()[0];
  return first ? guildView(first) : { kind: 'dm' };
}

async function switchUser(userId) {
  clearError();
  closeStream();
  modal.close();
  state.userId = userId;
  state.unread = 0;
  state.selectedGuildId = null;
  state.view = defaultView();
  if (state.view.kind === 'guild') {
    state.selectedGuildId = state.view.guildId;
  }
  resetViewState();
  renderChrome();
  openStream();
  await reloadMessages();
}

// Keeps the selection valid after the world changed.
function reconcile() {
  const stillThere = state.userId !== null && userById(state.userId) !== undefined;
  const hasNobody = state.userId === null && state.users.length === 0;
  if (hasNobody) {
    renderChrome();
    return;
  }
  if (!stillThere) {
    const first = state.users[0];
    switchUser(first ? first.id : null);
    return;
  }
  const view = state.view;
  const isViewGone = view.kind === 'guild' && !memberGuilds().some((guild) => guild.id === view.guildId);
  if (isViewGone) {
    enterView(defaultView());
    return;
  }
  renderChrome();
  renderMessages();
}

async function loadWorld() {
  const world = await api.getState();
  state.users = world.users;
  state.guilds = world.guilds;
  state.bot = world.bot;
}

async function loadCommands() {
  try {
    state.nodes = buildNodes(await api.getCommands());
  } catch (error) {
    showError(describeError(error));
  }
  composer.refresh();
}

async function refreshWorld() {
  try {
    await loadWorld();
    reconcile();
  } catch (error) {
    showError(describeError(error));
  }
}

// ---- event stream ----

function closeStream() {
  if (state.stream !== null) {
    state.stream.close();
    state.stream = null;
  }
}

function openStream() {
  closeStream();
  state.stream = api.openEvents(state.userId || '', {
    onOpen: () => {
      refreshWorld();
      loadCommands();
      reloadMessages();
      checkPendingModal();
    },
    onEvent: handleEvent,
  });
}

async function checkPendingModal() {
  const userId = state.userId;
  if (userId === null) {
    return;
  }
  try {
    const pending = await api.getPendingModal(userId);
    if (pending !== null && state.userId === userId) {
      modal.open(pending);
    }
  } catch (error) {
    showError(describeError(error));
  }
}

function handleEvent(name, payload) {
  switch (name) {
    case 'message_created':
      if (payload && payload.message && !payload.guild_id && state.view.kind !== 'dm') {
        state.unread += 1;
        renderDirectMessages();
      }
      reloadMessages();
      break;
    case 'message_updated':
    case 'message_deleted':
      reloadMessages();
      break;
    case 'modal_opened':
      if (payload && payload.user_id === state.userId) {
        modal.open(payload);
      }
      break;
    case 'bot_status':
      if (payload) {
        state.bot = payload.bot;
        renderTopbar();
        loadCommands();
      }
      break;
    case 'world_changed':
      refreshWorld();
      loadCommands();
      break;
    default:
      break;
  }
}

// ---- interactions ----

// Sends one interaction as the selected user. It resolves to { result, error }: error is a
// message when the request itself failed.
async function runInteraction(body) {
  if (state.userId === null) {
    return { result: null, error: 'Select a user first.' };
  }
  const startUser = state.userId;
  const startView = state.view;
  try {
    const result = await api.sendInteraction({ as: startUser, ...body });
    const isSameView = state.userId === startUser && state.view === startView;
    if (isSameView) {
      applyResult(result);
    }
    reloadMessages();
    return { result, error: null };
  } catch (error) {
    return { result: null, error: describeError(error) };
  }
}

function applyResult(result) {
  if (result.status === 'timeout') {
    state.noticeCounter += 1;
    state.notices.push({ id: state.noticeCounter });
  }
  const isThinking = result.status === 'deferred' && result.callback_type === CALLBACK_DEFER_MESSAGE;
  const isAnswered = state.messages.some((message) => message.interaction_id === result.interaction_id);
  if (isThinking && !isAnswered) {
    state.placeholders.push({ interactionId: result.interaction_id });
  }
  if (result.modal && result.modal.user_id === state.userId) {
    modal.open(result.modal);
  }
  renderMessages();
}

async function onButton(messageId, customId) {
  clearError();
  const { error } = await runInteraction({ kind: 'button', message_id: messageId, custom_id: customId });
  if (error !== null) {
    showError(error);
  }
}

async function onSelect(messageId, customId, values) {
  clearError();
  const { error } = await runInteraction({ kind: 'select', message_id: messageId, custom_id: customId, values });
  if (error !== null) {
    showError(error);
  }
}

function guildScope() {
  return state.view.kind === 'guild' ? { guild_id: state.view.guildId } : {};
}

async function submitSlash(path, options) {
  clearError();
  const { result, error } = await runInteraction({ kind: 'slash', path, options, ...guildScope() });
  if (error !== null) {
    composer.showErrors([error]);
    return false;
  }
  if (result.status === 'rejected') {
    composer.showErrors([result.error || 'The interaction was rejected.']);
    return false;
  }
  return true;
}

async function autocomplete(path, options, focused) {
  if (state.userId === null) {
    return [];
  }
  const result = await api.sendInteraction({ as: state.userId, kind: 'autocomplete', path, options, focused, ...guildScope() });
  return result.status === 'responded' ? result.choices || [] : [];
}

async function submitModal(customId, fields) {
  clearError();
  const { result, error } = await runInteraction({ kind: 'modal', custom_id: customId, fields });
  if (error !== null) {
    return { ok: false, error };
  }
  if (result.status === 'rejected') {
    return { ok: false, error: result.error };
  }
  return { ok: true, error: null };
}

// ---- composer and modal ----

function composerUsers() {
  if (state.view.kind === 'guild') {
    const guild = guildById(state.view.guildId);
    const ids = guild ? guild.members : [];
    return ids.map((id) => ({ id, name: userName(id) || id }));
  }
  return state.users.map((user) => ({ id: user.id, name: user.name }));
}

const composer = createComposer(
  {
    form: dom.composer,
    input: dom.slashInput,
    suggestions: dom.slashSuggestions,
    options: dom.slashOptions,
    error: dom.slashError,
  },
  {
    getNodes: () => state.nodes,
    isDirectMessage: () => state.view.kind === 'dm',
    getUsers: composerUsers,
    autocomplete,
    submit: submitSlash,
  },
);

const modal = createModal(dom.modalRoot, { submit: submitModal });

// ---- forms ----

dom.userSwitcher.addEventListener('change', () => {
  switchUser(dom.userSwitcher.value || null);
});

dom.newUserForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  clearError();
  const name = dom.newUserName.value.trim();
  if (name === '') {
    showError('Enter a name for the new user.');
    return;
  }
  try {
    const created = await api.addUser(name);
    dom.newUserName.value = '';
    await loadWorld();
    await switchUser(created.id);
  } catch (error) {
    showError(describeError(error));
  }
});

dom.newServerForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  clearError();
  const name = dom.newServerName.value.trim();
  const problems = [];
  if (name === '') {
    problems.push('Enter a name for the new server.');
  }
  if (state.userId === null) {
    problems.push('Select a user first: the user becomes a member of the server.');
  }
  if (problems.length > 0) {
    showError(problems.join(' '));
    return;
  }
  try {
    const created = await api.addGuild(name, [state.userId]);
    dom.newServerName.value = '';
    await loadWorld();
    const guild = guildById(created.id);
    if (guild) {
      enterView(guildView(guild));
    }
  } catch (error) {
    showError(describeError(error));
  }
});

dom.memberForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  clearError();
  const guild = selectedGuild();
  const userId = dom.memberSelect.value;
  const problems = [];
  if (guild === null) {
    problems.push('There is no server to add a member to.');
  }
  if (userId === '') {
    problems.push('Select the user to add.');
  }
  if (problems.length > 0) {
    showError(problems.join(' '));
    return;
  }
  try {
    await api.addMember(guild.id, userId);
    await loadWorld();
    reconcile();
  } catch (error) {
    showError(describeError(error));
  }
});

dom.dmLink.addEventListener('click', openDirectMessages);

// ---- start ----

async function start() {
  try {
    await loadWorld();
  } catch (error) {
    showError(describeError(error));
  }
  await loadCommands();
  const first = state.users[0];
  if (first) {
    await switchUser(first.id);
  } else {
    renderChrome();
    renderMessages();
    openStream();
  }
}

start();
