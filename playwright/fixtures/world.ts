import { expect, test as base, type Locator, type Page } from "@playwright/test";

import { Control, type FakeGuild, type FakeUser, type OutboundRecord } from "./control";
import * as texts from "./texts";

export { expect } from "@playwright/test";

// One value of a slash command option. A string fills a text field; a boolean sets a checkbox;
// { user } picks a member of the guild by name; { choose } types into an autocomplete field and
// clicks the choice with that name.
export type SlashValue = string | boolean | { user: string } | { choose: string };

const DEFAULT_CAST = ["Alice", "Bruno", "Carla"];
export const GUILD_NAME = "Test server";
const SYNC_BUDGET_MS = 10_000;

// A direct message goes through a queued job: River picks it up within a few seconds.
export const DM_BUDGET_MS = 25_000;

// How long a spec waits before it asserts that a direct message never arrived.
const ABSENCE_WINDOW_MS = 3_000;

export const PAIR_MARKER_TITLES = [texts.titleRecipient, texts.titleNewRecipient, texts.titleGiver];

// One embed that names a counterpart, as the page shows it.
export interface PairMarker {
  title: string;
  mentionIds: string[];
  isEphemeral: boolean;
}

// One outbound record with such an embed.
export interface PairRecord {
  record: OutboundRecord;
  title: string;
  namedId: string;
}

// Who gives to whom in one round: giver name to recipient name.
export type Round = ReadonlyMap<string, string>;

type View = "channel" | "dm";

function exactly(text: string): RegExp {
  const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`^\\s*${escaped}\\s*$`);
}

// The cast of one test: a fresh guild with named users, created through the control API, and
// the page of the fake's web client that plays them one at a time. Commands, buttons, selects
// and modals go through the page so the video shows them.
export class World {
  private viewer: FakeUser;
  private view: View = "channel";
  // The newest outbound record before the test began; records at or below it are not the test's.
  private outboundBaseline = 0;

  constructor(
    readonly page: Page,
    readonly control: Control,
    readonly guild: FakeGuild,
    private readonly cast: Readonly<Record<string, FakeUser>>,
  ) {
    const first = Object.values(cast)[0];
    if (first === undefined) {
      throw new Error("the cast needs at least one user");
    }
    this.viewer = first;
  }

  // Loads the page as the first user of the cast.
  async start(): Promise<void> {
    this.outboundBaseline = await this.control.latestOutboundSeq();
    await this.page.goto("/");
    await this.asUser(this.viewer.name);
  }

  user(name: string): FakeUser {
    const found = this.cast[name];
    if (found === undefined) {
      throw new Error(`no user named ${name} in the cast: ${Object.keys(this.cast).join(", ")}`);
    }
    return found;
  }

  // Switches the page to this user and opens the channel of the guild. Returns once the page
  // shows every message the user can see, so a check that something is absent is not vacuous.
  async asUser(name: string): Promise<void> {
    const user = this.user(name);
    await this.page.getByTestId("user-switcher").selectOption({ value: user.id });
    this.viewer = user;
    await this.openChannel();
  }

  async openChannel(): Promise<void> {
    await this.page.locator(`[data-testid="channel-link"][data-channel-id="${this.guild.channel_id}"]`).click();
    this.view = "channel";
    await this.settle();
  }

  async openDirectMessages(): Promise<void> {
    await this.page.getByTestId("dm-link").click();
    this.view = "dm";
    await this.settle();
  }

  get messages(): Locator {
    return this.page.getByTestId("message");
  }

  get ephemeralMessages(): Locator {
    return this.page.locator('[data-testid="message"][data-ephemeral="true"]');
  }

  // The message whose text content is exactly `text`.
  messageWithContent(text: string): Locator {
    return this.messages.filter({ has: this.page.getByTestId("message-content").filter({ hasText: exactly(text) }) });
  }

  // The message holding an embed with exactly this title.
  messageWithEmbed(title: string): Locator {
    return this.messages.filter({ has: this.page.getByTestId("embed-title").filter({ hasText: exactly(title) }) });
  }

  // The value of the embed field with this name inside a message. A name shared by two fields
  // matches both; pass `position` to pick one.
  fieldValue(message: Locator, name: string, position = 0): Locator {
    const field = message.getByTestId("embed-field").filter({
      has: this.page.getByTestId("embed-field-name").filter({ hasText: exactly(name) }),
    });
    return field.nth(position).getByTestId("embed-field-value");
  }

  // Types a slash command into the composer, fills its options and sends it. Returns once the
  // bot has acknowledged the interaction and the composer is empty again.
  async slash(path: string, options: Record<string, SlashValue> = {}): Promise<void> {
    const input = this.page.getByTestId("slash-input");
    await input.fill(path);
    for (const [name, value] of Object.entries(options)) {
      await this.fillOption(name, value);
    }
    await this.page.getByTestId("slash-submit").click();
    await expect(input).toHaveValue("");
  }

  // Clicks the last button with this label on the page.
  async clickButton(label: string): Promise<void> {
    await this.messages.getByRole("button", { name: label, exact: true }).last().click();
  }

  // Picks an option in the last select with this accessible name.
  async choose(selectName: string, optionLabel: string): Promise<void> {
    const select = this.messages.getByRole("combobox", { name: selectName, exact: true }).last();
    await select.selectOption({ label: optionLabel });
  }

  // Fills the open modal by the custom ID of its fields, sends it and waits for it to close.
  async submitModal(fields: Record<string, string>): Promise<void> {
    const modal = this.page.getByTestId("modal");
    await expect(modal).toBeVisible();
    for (const [customId, value] of Object.entries(fields)) {
      await modal.locator(`[data-testid="modal-field"][data-custom-id="${customId}"]`).fill(value);
    }
    await this.page.getByTestId("modal-submit").click();
    await expect(modal).toBeHidden();
  }

  // The ids of every user of the cast.
  private get castIds(): Set<string> {
    return new Set(Object.values(this.cast).map((user) => user.id));
  }

  // The name of a cast member, from the id a mention or a record carries.
  nameOf(userId: string): string {
    const found = Object.values(this.cast).find((user) => user.id === userId);
    if (found === undefined) {
      throw new Error("a mention or record names a user outside the cast");
    }
    return found.name;
  }

  // The message whose content contains this text.
  messageWithContentContaining(text: string): Locator {
    return this.messages.filter({ has: this.page.getByTestId("message-content").filter({ hasText: text }) });
  }

  // The public card of an exchange; open the channel first.
  card(exchangeName: string): Locator {
    return this.messageWithEmbed(exchangeName);
  }

  // The id of an exchange, read from the custom ID of the Join button of its card.
  async exchangeId(exchangeName: string): Promise<string> {
    await this.openChannel();
    const join = this.card(exchangeName).locator('[data-testid="component-button"][data-custom-id^="/gx/join/"]');
    const customId = await join.first().getAttribute("data-custom-id");
    if (customId === null) {
      throw new Error("the card has no Join button");
    }
    return customId.slice("/gx/join/".length);
  }

  // The id of the message that holds the card of an exchange.
  async cardMessageId(exchangeName: string): Promise<string> {
    await this.openChannel();
    const id = await this.card(exchangeName).first().getAttribute("data-message-id");
    if (id === null) {
      throw new Error("the card has no message id");
    }
    return id;
  }

  // The organizer creates an exchange and each joiner joins it by the card button.
  async createExchange(organizer: string, exchangeName: string, joiners: string[] = []): Promise<void> {
    await this.asUser(organizer);
    await this.slash("/ge create", { name: exchangeName });
    await expect(this.card(exchangeName)).toBeVisible();
    for (const joiner of joiners) {
      await this.join(joiner, exchangeName);
    }
  }

  async join(user: string, exchangeName: string): Promise<void> {
    await this.asUser(user);
    await this.card(exchangeName).getByRole("button", { name: texts.buttonJoin, exact: true }).click();
    await expect(this.messageWithContent(texts.format(texts.joined, exchangeName))).toBeVisible();
  }

  // Runs an organizer command that asks for a confirmation, confirms it and waits for every
  // text in `results`.
  async runConfirmed(
    organizer: string,
    command: string,
    exchangeName: string,
    prompt: string,
    results: string[],
  ): Promise<void> {
    await this.asUser(organizer);
    await this.slash(command, { exchange: { choose: exchangeName } });
    await expect(this.messageWithContent(prompt)).toBeVisible();
    await this.clickButton(texts.buttonConfirm);
    for (const result of results) {
      await expect(this.messageWithContent(result)).toBeVisible();
    }
  }

  async draw(organizer: string, exchangeName: string, participants: number): Promise<void> {
    const prompt = texts.format(texts.confirmDraw, exchangeName, participants);
    await this.runConfirmed(organizer, "/ge draw", exchangeName, prompt, [
      texts.format(texts.drawn, exchangeName),
      texts.format(texts.drawnPublic, exchangeName),
    ]);
  }

  async reveal(organizer: string, exchangeName: string): Promise<void> {
    const prompt = texts.format(texts.confirmReveal, exchangeName);
    await this.runConfirmed(organizer, "/ge reveal", exchangeName, prompt, [
      texts.format(texts.revealed, exchangeName),
      texts.format(texts.revealedPublic, exchangeName),
    ]);
  }

  // The names offered by the autocomplete of one option of a command.
  async autocompleteChoices(path: string, option: string, typed = ""): Promise<string[]> {
    const input = this.page.getByTestId("slash-input");
    await input.fill(path);
    const control = this.page.locator(`[data-testid="slash-option"][data-option="${option}"]`);
    if (typed === "") {
      await control.focus();
    } else {
      await control.fill(typed);
    }
    const choices = this.page.getByTestId("autocomplete-choice");
    await expect(choices.first()).toBeVisible();
    const names = (await choices.allTextContents()).map((name) => name.trim());
    await input.fill("");
    return names;
  }

  // The pair markers the page shows in the current view, in page order.
  async pairMarkers(): Promise<PairMarker[]> {
    return this.messages.evaluateAll((nodes, titles) => {
      const found: { title: string; mentionIds: string[]; isEphemeral: boolean }[] = [];
      for (const node of nodes) {
        for (const embed of Array.from(node.querySelectorAll('[data-testid="embed"]'))) {
          const title = (embed.querySelector('[data-testid="embed-title"]')?.textContent ?? "").trim();
          if (!titles.includes(title)) {
            continue;
          }
          const mentions = Array.from(
            embed.querySelectorAll('[data-testid="embed-description"] [data-testid="mention"]'),
          );
          found.push({
            title,
            mentionIds: mentions.map((mention) => mention.getAttribute("data-user-id") ?? ""),
            isEphemeral: node.getAttribute("data-ephemeral") === "true",
          });
        }
      }
      return found;
    }, PAIR_MARKER_TITLES);
  }

  // Gives a direct message that must not exist time to arrive, so an absence check that follows
  // can fail on a late one.
  async letLateDmsArrive(): Promise<void> {
    await this.page.waitForTimeout(ABSENCE_WINDOW_MS);
  }

  // The pair markers in a user's bot DM, once at least `atLeast` of them are there.
  async dmMarkers(user: string, atLeast = 0): Promise<PairMarker[]> {
    await this.asUser(user);
    await this.openDirectMessages();
    await expect
      .poll(async () => (await this.pairMarkers()).length, {
        message: `the DM of ${user} never held ${atLeast} pair markers`,
        timeout: DM_BUDGET_MS,
      })
      .toBeGreaterThanOrEqual(atLeast);
    return this.pairMarkers();
  }

  // Reads each giver's current recipient from the latest "Your recipient" or "Your new
  // recipient" DM.
  async recipientsFromDms(givers: string[]): Promise<Map<string, string>> {
    const round = new Map<string, string>();
    for (const giver of givers) {
      const markers = (await this.dmMarkers(giver, 1)).filter((marker) => marker.title !== texts.titleGiver);
      const latest = markers[markers.length - 1];
      if (latest === undefined || latest.mentionIds.length !== 1) {
        throw new Error(`${giver} has no single recipient in the DM`);
      }
      round.set(giver, this.nameOf(latest.mentionIds[0] ?? ""));
    }
    return round;
  }

  // Every outbound record of this test, from the cast's point of view, with a pair marker.
  async pairRecords(): Promise<PairRecord[]> {
    const records = await this.control.outbound(this.outboundBaseline);
    const ids = this.castIds;
    const found: PairRecord[] = [];
    for (const record of records) {
      if (!record.visible_to.some((id) => ids.has(id))) {
        continue;
      }
      for (const embed of record.embeds) {
        const title = embed.title ?? "";
        if (PAIR_MARKER_TITLES.includes(title)) {
          const named = /<@!?(\d+)>/.exec(embed.description ?? "");
          found.push({ record, title, namedId: named?.[1] ?? "" });
        }
      }
    }
    return found;
  }

  // Every outbound record of this test that is visible to a cast member.
  async castRecords(): Promise<OutboundRecord[]> {
    const records = await this.control.outbound(this.outboundBaseline);
    const ids = this.castIds;
    return records.filter((record) => record.visible_to.some((id) => ids.has(id)));
  }

  // Whether `named` is the counterpart a marker with this title may show to `viewer` in one of
  // the rounds: the recipient of the viewer, or for "Your giver" the giver of the viewer.
  private namesOwnCounterpart(viewer: string, title: string, named: string, rounds: Round[]): boolean {
    return rounds.some((round) =>
      title === texts.titleGiver ? round.get(named) === viewer : round.get(viewer) === named,
    );
  }

  // Walks a user's channel and DM and checks every pair marker there names the user's own
  // counterpart; an outsider (in no round) must see none.
  private async expectOwnMarkersOnly(user: string, rounds: Round[]): Promise<void> {
    await this.asUser(user);
    const inChannel = await this.pairMarkers();
    await this.openDirectMessages();
    const inDm = await this.pairMarkers();

    for (const marker of inChannel) {
      expect(marker.isEphemeral, `${user} sees a public ${marker.title}`).toBe(true);
    }
    for (const marker of [...inChannel, ...inDm]) {
      expect(marker.mentionIds, `${user}: ${marker.title} must name one user`).toHaveLength(1);
      const named = this.nameOf(marker.mentionIds[0] ?? "");
      const isOwn = this.namesOwnCounterpart(user, marker.title, named, rounds);
      expect(isOwn, `${user} sees ${marker.title} naming ${named}, who is not their counterpart`).toBe(true);
    }
  }

  // The confidentiality check: the UI walk of every user, then the outbound records.
  async expectConfidential(rounds: Round[], outsiders: string[], participants: string[]): Promise<void> {
    for (const outsider of outsiders) {
      await this.expectOwnMarkersOnly(outsider, []);
    }
    for (const participant of participants) {
      await this.expectOwnMarkersOnly(participant, rounds);
    }

    const outsiderIds = new Set(outsiders.map((name) => this.user(name).id));
    for (const { record, title, namedId } of await this.pairRecords()) {
      expect(record.visible_to, `a ${title} record must have one reader`).toHaveLength(1);
      const readerId = record.visible_to[0] ?? "";
      const reader = this.nameOf(readerId);
      expect(outsiderIds.has(readerId), `${reader} is an outsider and got a ${title}`).toBe(false);
      const isPrivate = record.ephemeral || record.source === "dm";
      expect(isPrivate, `a ${title} record is public`).toBe(true);
      const named = this.nameOf(namedId);
      const isOwn = this.namesOwnCounterpart(reader, title, named, rounds);
      expect(isOwn, `${reader} got ${title} naming ${named}, who is not their counterpart`).toBe(true);
    }
  }

  private async fillOption(name: string, value: SlashValue): Promise<void> {
    const control = this.page.locator(`[data-testid="slash-option"][data-option="${name}"]`);
    if (typeof value === "string") {
      await control.fill(value);
    } else if (typeof value === "boolean") {
      await control.setChecked(value);
    } else if ("user" in value) {
      await control.selectOption({ value: this.user(value.user).id });
    } else {
      await control.fill(value.choose);
      await this.page.getByTestId("autocomplete-choice").filter({ hasText: exactly(value.choose) }).click();
    }
  }

  // Waits until the page lists exactly the messages, by id and in order, that the fake holds for the current user and view.
  private async settle(): Promise<void> {
    const viewer = this.viewer;
    const view = this.view;
    await expect
      .poll(
        async () => {
          const stored =
            view === "dm"
              ? await this.control.directMessages(viewer.id)
              : await this.control.channelMessages(this.guild.channel_id, viewer.id);
          const shown = await this.messages.evaluateAll((nodes) =>
            nodes.map((node) => node.getAttribute("data-message-id")),
          );
          const expected = stored.map((message) => message.id);
          return JSON.stringify(shown) === JSON.stringify(expected);
        },
        { message: `the page never listed the messages of ${viewer.name}`, timeout: SYNC_BUDGET_MS },
      )
      .toBe(true);
  }
}

interface Fixtures {
  cast: string[];
  world: World;
}

export const test = base.extend<Fixtures>({
  cast: [DEFAULT_CAST, { option: true }],

  world: async ({ page, request, cast }, use) => {
    const control = new Control(request);
    const users: Record<string, FakeUser> = {};
    for (const name of cast) {
      users[name] = await control.createUser(name);
    }
    const guild = await control.createGuild(
      GUILD_NAME,
      Object.values(users).map((user) => user.id),
    );

    const world = new World(page, control, guild, users);
    await world.start();
    await use(world);
  },
});
