import { expect, test, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";

async function createExchange(world: World): Promise<void> {
  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });
  await expect(world.messageWithEmbed(EXCHANGE)).toBeVisible();
}

function listLine(participants: number): string {
  return texts.format(texts.listLine, EXCHANGE, "open", participants, texts.roleOrganizer);
}

test("a participant joins by the card button and by the slash command", async ({ world }) => {
  await createExchange(world);

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();

  await world.asUser("Carla");
  await world.slash("/ge join");
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();

  await world.asUser("Alice");
  await world.slash("/ge list");
  const list = world.messageWithEmbed(texts.listTitle);
  await expect(list.getByTestId("embed-description")).toHaveText(listLine(2));
});

test("joining twice answers that the person is already in", async ({ world }) => {
  await createExchange(world);

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();

  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.alreadyParticipant)).toBeVisible();

  await world.slash("/ge join");
  await expect(world.messageWithContent(texts.noCandidates)).toBeVisible();
});

test("a participant leaves while the exchange is open and the count drops", async ({ world }) => {
  await createExchange(world);

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();
  await world.asUser("Carla");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();

  await world.slash("/ge leave");
  await expect(world.messageWithContent(texts.format(texts.left, EXCHANGE))).toBeVisible();

  await world.asUser("Alice");
  await world.slash("/ge list");
  const list = world.messageWithEmbed(texts.listTitle);
  await expect(list.getByTestId("embed-description")).toHaveText(listLine(1));

  // Bruno is still in: joining again is refused.
  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.alreadyParticipant)).toBeVisible();

  // Carla is out: joining again succeeds and adds one more confirmation to her view.
  await world.asUser("Carla");
  const confirmations = world.messageWithContent(texts.format(texts.joined, EXCHANGE));
  const before = await confirmations.count();
  await world.clickButton(texts.buttonJoin);
  await expect(confirmations).toHaveCount(before + 1);
});
