// The only module that talks to the server. Every request goes to /_control; the bot token
// endpoint is never requested.

const CONTROL = '/_control';

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function request(method, path, body) {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body);
  }
  const response = await fetch(CONTROL + path, options);
  const text = await response.text();
  let data = null;
  if (text !== '') {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!response.ok) {
    const message = data && typeof data.message === 'string' ? data.message : response.statusText;
    throw new ApiError(response.status, message || 'Request failed (' + response.status + ')');
  }
  return data;
}

export function getState() {
  return request('GET', '/state');
}

export function getCommands() {
  return request('GET', '/commands');
}

export function addUser(name) {
  return request('POST', '/users', { name });
}

export function addGuild(name, memberIds) {
  return request('POST', '/guilds', { name, members: memberIds });
}

export function addMember(guildId, userId) {
  return request('POST', '/guilds/' + encodeURIComponent(guildId) + '/members', { user_id: userId });
}

export function getChannelMessages(channelId, userId) {
  return request('GET', '/channels/' + encodeURIComponent(channelId) + '/messages?as=' + encodeURIComponent(userId));
}

export function getDirectMessages(userId) {
  return request('GET', '/dms/' + encodeURIComponent(userId));
}

// Resolves to the user's pending modal, or null when none is open.
export async function getPendingModal(userId) {
  try {
    return await request('GET', '/modals/' + encodeURIComponent(userId));
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return null;
    }
    throw error;
  }
}

export function sendInteraction(body) {
  return request('POST', '/interactions', body);
}

export const EVENT_NAMES = [
  'message_created',
  'message_updated',
  'message_deleted',
  'modal_opened',
  'world_changed',
  'bot_status',
];

// Opens the event stream of one user, or of the whole world when userId is empty. The caller
// closes the returned stream before opening another one.
export function openEvents(userId, handlers) {
  const source = new EventSource(CONTROL + '/events?as=' + encodeURIComponent(userId));
  for (const name of EVENT_NAMES) {
    source.addEventListener(name, (event) => {
      let payload = null;
      try {
        payload = JSON.parse(event.data);
      } catch {
        payload = null;
      }
      if (handlers.onEvent) {
        handlers.onEvent(name, payload);
      }
    });
  }
  if (handlers.onOpen) {
    source.addEventListener('open', handlers.onOpen);
  }
  return source;
}
