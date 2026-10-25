import { expect, test } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla"];

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("/ge recipient answers the giver alone, in the channel", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);
  await world.draw("Olga", EXCHANGE, PARTICIPANTS.length);
  const round = await world.recipientsFromDms(PARTICIPANTS);

  await world.asUser("Alice");
  await world.slash("/ge recipient");
  const reply = world.messageWithEmbed(texts.titleRecipient);
  await expect(reply).toBeVisible();
  await expect(reply).toHaveAttribute("data-ephemeral", "true");
  await expect(reply.getByTestId("ephemeral-note")).toBeVisible();
  await expect(reply.getByTestId("embed-description").getByTestId("mention")).toHaveCount(1);
  await expect(reply.getByTestId("embed-description")).toHaveText(`@${round.get("Alice")}`);

  for (const other of ["Bruno", "Olga"]) {
    await world.asUser(other);
    await expect(world.messageWithEmbed(texts.titleRecipient)).toHaveCount(0);
  }

  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});

test("/ge recipient also works from the bot DM", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);
  await world.draw("Olga", EXCHANGE, PARTICIPANTS.length);
  const round = await world.recipientsFromDms(PARTICIPANTS);

  await world.asUser("Bruno");
  await world.openDirectMessages();
  const before = await world.messageWithEmbed(texts.titleRecipient).count();
  await world.slash("/ge recipient");
  await expect(world.messageWithEmbed(texts.titleRecipient)).toHaveCount(before + 1);

  const markers = await world.pairMarkers();
  const reply = markers[markers.length - 1];
  expect(reply?.isEphemeral).toBe(true);
  expect(world.nameOf(reply?.mentionIds[0] ?? "")).toBe(round.get("Bruno"));

  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});
