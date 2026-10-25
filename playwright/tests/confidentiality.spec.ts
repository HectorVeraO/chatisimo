import { expect, test, DM_BUDGET_MS, type Round, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const ORGANIZER = "Olga";
const OUTSIDER = "Xavi";
const PARTICIPANTS = ["Alice", "Bruno", "Carla", "Dario", "Erik"];
const OUTSIDERS = [ORGANIZER, OUTSIDER];

test.use({ cast: [ORGANIZER, ...PARTICIPANTS, OUTSIDER] });

// The organizer never joins; the five participants do; the organizer draws. Returns the round
// read from each participant's DM, after checking it is a derangement of the participants.
async function drawnRound(world: World): Promise<Round> {
  await world.createExchange(ORGANIZER, EXCHANGE, PARTICIPANTS);
  await world.draw(ORGANIZER, EXCHANGE, PARTICIPANTS.length);

  const round = await world.recipientsFromDms(PARTICIPANTS);
  expect([...round.values()].sort()).toEqual([...PARTICIPANTS].sort());
  for (const [giver, recipient] of round) {
    expect(recipient).not.toBe(giver);
  }
  return round;
}

test("after the draw only each giver sees their own recipient", async ({ world }) => {
  const round = await drawnRound(world);

  // Every participant also asks for their recipient in the channel: an ephemeral reply.
  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.slash("/ge recipient");
    await expect(world.messageWithEmbed(texts.titleRecipient).filter({ has: world.page.getByTestId("ephemeral-note") })).toBeVisible();
  }

  await world.expectConfidential([round], OUTSIDERS, PARTICIPANTS);
});

test("after a dropout only the affected givers are told, and only their own recipient", async ({ world }) => {
  const round = await drawnRound(world);
  const leaver = "Bruno";
  const giverOfLeaver = [...round].find(([, recipient]) => recipient === leaver)?.[0] ?? "";
  const inherited = round.get(leaver) ?? "";
  const remaining = PARTICIPANTS.filter((name) => name !== leaver);

  await world.asUser(leaver);
  await world.slash("/ge leave");
  await expect(world.messageWithContent(texts.format(texts.confirmLeave, EXCHANGE))).toBeVisible();
  await world.clickButton(texts.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.left, EXCHANGE))).toBeVisible();

  // The giver of the leaver is told in both cases.
  await world.asUser(giverOfLeaver);
  await world.openDirectMessages();
  await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(1, { timeout: DM_BUDGET_MS });

  // A leaver whose giver and recipient are the same person forces a redraw, which tells every
  // remaining giver; any other leaver is patched and only that giver is told.
  const isRedrawn = inherited === giverOfLeaver;
  const told = isRedrawn ? remaining : [giverOfLeaver];
  for (const name of told) {
    await world.asUser(name);
    await world.openDirectMessages();
    await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(1, { timeout: DM_BUDGET_MS });
  }
  await world.letLateDmsArrive();
  for (const name of remaining.filter((candidate) => !told.includes(candidate))) {
    await world.asUser(name);
    await world.openDirectMessages();
    await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(0);
  }
  await world.asUser(leaver);
  await world.openDirectMessages();
  await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(0);

  const newRound = await world.recipientsFromDms(remaining);
  if (!isRedrawn) {
    expect(newRound.get(giverOfLeaver)).toBe(inherited);
  }

  await world.expectConfidential([round, newRound], OUTSIDERS, PARTICIPANTS);
});

test("the organizer's own commands show no pair and the reveal tells each participant one giver", async ({
  world,
}) => {
  const round = await drawnRound(world);
  const exchangeId = await world.exchangeId(EXCHANGE);

  await world.asUser(ORGANIZER);
  await world.slash("/ge recipient");
  await expect(world.messageWithContent(texts.noCandidates)).toBeVisible();
  await world.slash("/ge recipient", { exchange: exchangeId });
  await expect(world.messageWithContent(texts.notParticipant)).toBeVisible();

  await world.slash("/ge rules");
  const rules = world.messageWithEmbed(texts.format(texts.rulesTitle, EXCHANGE));
  await expect(rules).toBeVisible();
  await expect(world.fieldValue(rules, texts.exclusionsField, 1)).toHaveText("0");
  expect(await world.pairMarkers()).toEqual([]);

  await world.reveal(ORGANIZER, EXCHANGE);
  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.openDirectMessages();
    await expect(world.messageWithEmbed(texts.titleGiver)).toHaveCount(1, { timeout: DM_BUDGET_MS });
  }
  for (const outsider of OUTSIDERS) {
    await world.asUser(outsider);
    await world.openDirectMessages();
    await expect(world.messageWithEmbed(texts.titleGiver)).toHaveCount(0);
  }

  await world.expectConfidential([round], OUTSIDERS, PARTICIPANTS);
});
