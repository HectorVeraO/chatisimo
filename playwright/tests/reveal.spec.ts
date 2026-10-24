import { expect, test, DM_BUDGET_MS, GUILD_NAME } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla", "Dario"];

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("the reveal tells each participant their giver by DM and by /ge giver", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);
  await world.draw("Olga", EXCHANGE, PARTICIPANTS.length);
  const round = await world.recipientsFromDms(PARTICIPANTS);

  // Before the reveal nobody can ask for a giver.
  await world.asUser("Alice");
  await world.slash("/ge giver");
  await expect(world.messageWithContent(texts.noCandidates)).toBeVisible();

  await world.reveal("Olga", EXCHANGE);

  const givers = new Map<string, string>();
  for (const [giver, recipient] of round) {
    givers.set(recipient, giver);
  }

  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.openDirectMessages();
    const dm = world.messageWithEmbed(texts.titleGiver);
    await expect(dm).toHaveCount(1, { timeout: DM_BUDGET_MS });
    await expect(dm.getByTestId("message-content")).toHaveText(
      texts.format(texts.dmRevealed, EXCHANGE, GUILD_NAME),
    );
    await expect(dm.getByTestId("embed-description").getByTestId("mention")).toHaveCount(1);
    await expect(dm.getByTestId("embed-description")).toHaveText(`@${givers.get(participant)}`);
  }

  await world.asUser("Alice");
  await world.slash("/ge giver");
  const reply = world.messageWithEmbed(texts.titleGiver).filter({ has: world.page.getByTestId("ephemeral-note") });
  await expect(reply).toBeVisible();
  await expect(reply.getByTestId("embed-description")).toHaveText(`@${givers.get("Alice")}`);

  await world.asUser("Olga");
  await world.openDirectMessages();
  await expect(world.messageWithEmbed(texts.titleGiver)).toHaveCount(0);
  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});
