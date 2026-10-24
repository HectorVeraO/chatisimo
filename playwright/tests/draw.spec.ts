import { expect, test, DM_BUDGET_MS, GUILD_NAME } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla", "Dario"];

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("the draw is confirmed by the organizer and announced publicly without any pair", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);

  await world.asUser("Olga");
  await world.slash("/ge draw");
  await expect(
    world.messageWithContent(texts.format(texts.confirmDraw, EXCHANGE, PARTICIPANTS.length)),
  ).toBeVisible();
  await expect(world.messages.getByRole("button", { name: texts.buttonConfirm, exact: true })).toBeVisible();
  await expect(world.messages.getByRole("button", { name: texts.buttonBack, exact: true })).toBeVisible();
  await world.clickButton(texts.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.drawn, EXCHANGE))).toBeVisible();

  const announcement = world.messageWithContent(texts.format(texts.drawnPublic, EXCHANGE));
  await expect(announcement).toBeVisible();
  await expect(announcement).toHaveAttribute("data-ephemeral", "false");
  await expect(announcement.getByTestId("embed")).toHaveCount(0);

  await world.asUser("Bruno");
  const seenByMember = world.messageWithContent(texts.format(texts.drawnPublic, EXCHANGE));
  await expect(seenByMember).toBeVisible();
  await expect(seenByMember.getByTestId("mention")).toHaveCount(0);
});

test("each giver gets a DM with exactly one mention and never themselves", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);
  await world.draw("Olga", EXCHANGE, PARTICIPANTS.length);

  const recipients = new Map<string, string>();
  for (const giver of PARTICIPANTS) {
    await world.asUser(giver);
    await world.openDirectMessages();

    const dm = world.messageWithEmbed(texts.titleRecipient);
    await expect(dm).toHaveCount(1, { timeout: DM_BUDGET_MS });
    await expect(dm.getByTestId("message-content")).toHaveText(texts.format(texts.dmDrawn, EXCHANGE, GUILD_NAME));
    await expect(dm.getByTestId("embed-description").getByTestId("mention")).toHaveCount(1);
    await expect(world.fieldValue(dm, texts.fieldReveal)).toHaveText(texts.manual);

    const markers = await world.pairMarkers();
    expect(markers).toHaveLength(1);
    const [marker] = markers;
    expect(marker?.mentionIds).toHaveLength(1);
    const recipient = world.nameOf(marker?.mentionIds[0] ?? "");
    expect(recipient).not.toBe(giver);
    recipients.set(giver, recipient);
  }

  // The recipients are the participants once each: a derangement.
  expect([...recipients.values()].sort()).toEqual([...PARTICIPANTS].sort());

  await world.asUser("Olga");
  await world.openDirectMessages();
  await expect(world.messages).toHaveCount(0);
  await world.expectConfidential([recipients], ["Olga"], PARTICIPANTS);
});
