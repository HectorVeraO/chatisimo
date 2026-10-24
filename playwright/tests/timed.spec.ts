import { expect, test, DM_BUDGET_MS, GUILD_NAME, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla"];
const EVERYONE = ["Olga", ...PARTICIPANTS];

test.use({ cast: EVERYONE });

// The organizer changes one rule of the rules view in its modal. The time is read by the bot
// when the modal is submitted, so each value is typed right before it is sent.
async function setRule(world: World, label: string, value: string): Promise<void> {
  await world.choose(texts.selectPlaceholder, label);
  await world.submitModal({ value });
}

// Waits until the DM of the user shows a message whose content contains the text.
async function expectDirectMessage(world: World, user: string, text: string): Promise<void> {
  await world.asUser(user);
  await world.openDirectMessages();
  await expect(world.messageWithContentContaining(text)).toBeVisible({ timeout: DM_BUDGET_MS });
}

test("the reminder, the deadline draw, the reveal reminder and the reveal arrive on time", async ({ world }) => {
  test.setTimeout(180_000);

  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);

  await world.asUser("Olga");
  await world.clickButton(texts.buttonRules);
  const rules = world.messageWithEmbed(texts.format(texts.rulesTitle, EXCHANGE));
  await expect(rules).toBeVisible();
  await setRule(world, texts.ruleReminderLead, "5s");
  await expect(world.fieldValue(rules, texts.ruleReminderLead)).toContainText("5s");
  await setRule(world, texts.ruleRevealAt, "in 40s");
  await setRule(world, texts.ruleDrawAt, "in 15s");
  await expect(world.fieldValue(rules, texts.ruleDrawAt).getByTestId("timestamp").first()).toBeVisible();

  const drawSoon = texts.formatPrefix(texts.dmDrawSoon, EXCHANGE, GUILD_NAME);
  for (const user of EVERYONE) {
    await expectDirectMessage(world, user, drawSoon);
  }

  const drawn = texts.format(texts.dmDrawn, EXCHANGE, GUILD_NAME);
  for (const user of PARTICIPANTS) {
    await expectDirectMessage(world, user, drawn);
  }
  const round = await world.recipientsFromDms(PARTICIPANTS);

  const revealSoon = texts.formatPrefix(texts.dmRevealSoon, EXCHANGE, GUILD_NAME);
  for (const user of PARTICIPANTS) {
    await expectDirectMessage(world, user, revealSoon);
    await expect(world.messageWithContentContaining(revealSoon).getByTestId("embed-description")).toHaveText(
      `@${round.get(user)}`,
    );
  }

  const revealed = texts.format(texts.dmRevealed, EXCHANGE, GUILD_NAME);
  for (const user of PARTICIPANTS) {
    await expectDirectMessage(world, user, revealed);
  }

  // One DM per event, in the order the clock produced them.
  await world.asUser("Alice");
  await world.openDirectMessages();
  const contents = await world.messages.evaluateAll((nodes) =>
    nodes.map((node) => (node.querySelector('[data-testid="message-content"]')?.textContent ?? "").trim()),
  );
  const expected = [drawSoon, drawn, revealSoon, revealed];
  expect(contents).toHaveLength(expected.length);
  expected.forEach((text, position) => {
    expect(contents[position]?.startsWith(text)).toBe(true);
  });

  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});
