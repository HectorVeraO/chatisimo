import type { APIRequestContext, APIResponse } from "@playwright/test";

export interface FakeUser {
  id: string;
  name: string;
  dms_open: boolean;
  locale: string;
}

export interface FakeGuild {
  id: string;
  name: string;
  channel_id: string;
  channel: string;
  locale: string;
  members: string[];
}

export type InteractionKind = "slash" | "autocomplete" | "button" | "select" | "modal";

export interface InteractionRequest {
  as: string;
  kind: InteractionKind;
  guild_id?: string;
  path?: string;
  options?: Record<string, string | number | boolean>;
  focused?: string;
  message_id?: string;
  custom_id?: string;
  values?: string[];
  fields?: Record<string, string>;
  bypass_validation?: boolean;
  forged?: boolean;
}

export interface InteractionResult {
  interaction_id: string;
  status: "responded" | "deferred" | "timeout" | "rejected";
  callback_type: number;
  message_id?: string;
  choices?: { name: string; value: string }[];
  modal?: unknown;
  error?: string;
}

export interface FakeEmbed {
  title?: string;
  description?: string;
  fields?: { name: string; value: string; inline?: boolean }[];
}

export interface FakeMessage {
  id: string;
  channel_id: string;
  author: { id: string; username: string; bot?: boolean };
  content: string;
  flags: number;
  embeds: FakeEmbed[];
  components: unknown[];
  interaction_id?: string;
  pings?: string[];
}

export interface OutboundRecord {
  seq: number;
  route: string;
  source: "dm" | "callback" | "edit" | "followup" | "channel";
  status: number;
  interaction_id?: string;
  message_id?: string;
  channel_id: string;
  guild_id: string | null;
  ephemeral: boolean;
  visible_to: string[];
  content: string;
  embeds: FakeEmbed[];
  allowed_mentions_effective: { parse: string[] };
  mentions_rendered: string[];
  pings: string[];
}

export interface Health {
  server: string;
  bot: "ready" | "connecting" | "absent";
}

const CONTROL = "/_control";

// Typed wrappers over the control API of the fake Discord. The API has no authentication and
// the fake publishes it on loopback only.
export class Control {
  constructor(private readonly api: APIRequestContext) {}

  async healthz(waitForBot = false): Promise<Health> {
    const response = await this.api.get(`${CONTROL}/healthz${waitForBot ? "?bot=1" : ""}`);
    return (await this.json(response, "GET healthz")) as Health;
  }

  async createUser(name: string): Promise<FakeUser> {
    const response = await this.api.post(`${CONTROL}/users`, { data: { name } });
    return (await this.json(response, "POST users")) as FakeUser;
  }

  async setDirectMessagesOpen(userId: string, isOpen: boolean): Promise<FakeUser> {
    const response = await this.api.patch(`${CONTROL}/users/${encodeURIComponent(userId)}`, {
      data: { dms_open: isOpen },
    });
    return (await this.json(response, "PATCH users")) as FakeUser;
  }

  async setUserLocale(userId: string, locale: string): Promise<FakeUser> {
    const response = await this.api.patch(`${CONTROL}/users/${encodeURIComponent(userId)}`, {
      data: { locale },
    });
    return (await this.json(response, "PATCH users")) as FakeUser;
  }

  async createGuild(name: string, memberIds: string[]): Promise<FakeGuild> {
    const response = await this.api.post(`${CONTROL}/guilds`, { data: { name, members: memberIds } });
    return (await this.json(response, "POST guilds")) as FakeGuild;
  }

  async setGuildLocale(guildId: string, locale: string): Promise<FakeGuild> {
    const response = await this.api.patch(`${CONTROL}/guilds/${encodeURIComponent(guildId)}`, {
      data: { locale },
    });
    return (await this.json(response, "PATCH guilds")) as FakeGuild;
  }

  async addMember(guildId: string, userId: string): Promise<void> {
    const response = await this.api.post(`${CONTROL}/guilds/${encodeURIComponent(guildId)}/members`, {
      data: { user_id: userId },
    });
    await this.expectStatus(response, 204, "POST guild members");
  }

  async setMemberPermissions(guildId: string, userId: string, permissions: string): Promise<void> {
    const url = `${CONTROL}/guilds/${encodeURIComponent(guildId)}/members/${encodeURIComponent(userId)}`;
    const response = await this.api.patch(url, { data: { permissions } });
    await this.expectStatus(response, 204, "PATCH guild member");
  }

  async interact(request: InteractionRequest): Promise<InteractionResult> {
    const response = await this.api.post(`${CONTROL}/interactions`, { data: request });
    return (await this.json(response, "POST interactions")) as InteractionResult;
  }

  async channelMessages(channelId: string, viewerId: string): Promise<FakeMessage[]> {
    const url = `${CONTROL}/channels/${encodeURIComponent(channelId)}/messages?as=${encodeURIComponent(viewerId)}`;
    const response = await this.api.get(url);
    return (await this.json(response, "GET channel messages")) as FakeMessage[];
  }

  async directMessages(userId: string): Promise<FakeMessage[]> {
    const response = await this.api.get(`${CONTROL}/dms/${encodeURIComponent(userId)}`);
    return (await this.json(response, "GET dms")) as FakeMessage[];
  }

  // The outbound records with a sequence number above `since`.
  async outbound(since = 0): Promise<OutboundRecord[]> {
    const response = await this.api.get(`${CONTROL}/outbound?since=${since}`);
    return (await this.json(response, "GET outbound")) as OutboundRecord[];
  }

  // The sequence number of the newest outbound record, 0 when there is none.
  async latestOutboundSeq(): Promise<number> {
    const records = await this.outbound(0);
    return records.reduce((newest, record) => Math.max(newest, record.seq), 0);
  }

  async commands(): Promise<unknown[]> {
    const response = await this.api.get(`${CONTROL}/commands`);
    return (await this.json(response, "GET commands")) as unknown[];
  }

  private async json(response: APIResponse, what: string): Promise<unknown> {
    await this.expectStatus(response, 200, what);
    return response.json();
  }

  private async expectStatus(response: APIResponse, wanted: number, what: string): Promise<void> {
    if (response.status() !== wanted) {
      throw new Error(`${what}: status ${response.status()}, wanted ${wanted}: ${await response.text()}`);
    }
  }
}
