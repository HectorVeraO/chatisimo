import { expect, test as base, type Locator, type Page } from "@playwright/test";

import { Control, type FakeGuild, type FakeUser } from "./control";

export { expect } from "@playwright/test";

// One value of a slash command option. A string fills a text field; a boolean sets a checkbox;
// { user } picks a member of the guild by name; { choose } types into an autocomplete field and
// clicks the choice with that name.
export type SlashValue = string | boolean | { user: string } | { choose: string };

const DEFAULT_CAST = ["Alice", "Bruno", "Carla"];
const GUILD_NAME = "Test server";
const SYNC_BUDGET_MS = 10_000;

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

  // Waits until the page lists as many messages as the fake holds for the current user and view.
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
          const shown = await this.messages.count();
          return shown === stored.length;
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
