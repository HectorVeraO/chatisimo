import { expect, test, DM_BUDGET_MS, GUILD_NAME } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla"];

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("cancelling announces it publicly, tells each participant by DM and closes the card", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, PARTICIPANTS);
  await world.draw("Olga", EXCHANGE, PARTICIPANTS.length);

  await world.runConfirmed("Olga", "/ge cancel", `${EXCHANGE} (drawn)`, texts.format(texts.confirmCancel, EXCHANGE), [
    texts.format(texts.cancelled, EXCHANGE),
    texts.format(texts.cancelledPublic, EXCHANGE),
  ]);
  const announcement = world.messageWithContent(texts.format(texts.cancelledPublic, EXCHANGE));
  await expect(announcement).toHaveAttribute("data-ephemeral", "false");

  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.openDirectMessages();
    await expect(world.messageWithContentContaining(texts.format(texts.dmCancelled, EXCHANGE, GUILD_NAME))).toBeVisible({
      timeout: DM_BUDGET_MS,
    });
  }

  const cancelled = texts.format(texts.invalidState, "cancelled");
  await world.asUser("Bruno");
  await world.card(EXCHANGE).getByRole("button", { name: texts.buttonJoin, exact: true }).click();
  await expect(world.messageWithContent(cancelled)).toHaveCount(1);
  await world.card(EXCHANGE).getByRole("button", { name: texts.buttonLeave, exact: true }).click();
  await expect(world.messageWithContent(cancelled)).toHaveCount(2);

});
