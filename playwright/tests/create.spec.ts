import { expect, test } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";

test("create posts a public card with the organizer and the join controls", async ({ world }) => {
  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });

  const card = world.messageWithEmbed(EXCHANGE);
  await expect(card).toBeVisible();
  await expect(card.getByTestId("ephemeral-note")).toHaveCount(0);
  await expect(card.getByTestId("embed-description")).toHaveText(texts.format(texts.cardBody, "@Alice"));
  await expect(world.fieldValue(card, texts.fieldDraw)).toHaveText(texts.manual);
  await expect(world.fieldValue(card, texts.fieldReveal)).toHaveText(texts.manual);
  for (const label of [texts.buttonJoin, texts.buttonLeave, texts.buttonRules]) {
    await expect(card.getByRole("button", { name: label, exact: true })).toBeVisible();
  }
});

test("create shows the organizer an ephemeral hint nobody else sees", async ({ world, page }) => {
  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });

  const hint = world.messageWithContent(texts.format(texts.created, EXCHANGE));
  await expect(hint).toBeVisible();
  await expect(hint.getByTestId("ephemeral-note")).toBeVisible();

  await world.asUser("Bruno");
  await expect(world.messageWithEmbed(EXCHANGE)).toBeVisible();
  await expect(world.messageWithContent(texts.format(texts.created, EXCHANGE))).toHaveCount(0);
  await expect(world.ephemeralMessages).toHaveCount(0);
  await expect(page.getByTestId("ping-badge")).toHaveCount(0);
});

test("create sends the hint to the organizer alone and the card to every member", async ({ world }) => {
  const sinceSeq = await world.control.latestOutboundSeq();
  const alice = world.user("Alice");

  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });
  await expect(world.messageWithEmbed(EXCHANGE)).toBeVisible();

  const records = await world.control.outbound(sinceSeq);
  const hintRecord = records.find((record) => record.content === texts.format(texts.created, `**${EXCHANGE}**`));
  expect(hintRecord?.ephemeral).toBe(true);
  expect(hintRecord?.visible_to).toEqual([alice.id]);
  const cardRecord = records.find((record) => record.embeds.some((embed) => embed.title === EXCHANGE));
  expect(cardRecord?.ephemeral).toBe(false);
  expect([...(cardRecord?.visible_to ?? [])].sort()).toEqual(world.guild.members.slice().sort());
});
