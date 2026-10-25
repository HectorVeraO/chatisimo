import { expect, test, DM_BUDGET_MS, GUILD_NAME } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const DRAWN = "Winter exchange";
const OPEN = "Spring exchange";
const PARTICIPANTS = ["Alice", "Bruno", "Carla"];
const QUESTION = "Do you like tea?";
const ANSWER = "Yes, green tea.";

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("ask lists only drawn exchanges, fills the only one in, and the answer travels back", async ({ world }) => {
  await world.createExchange("Olga", DRAWN);
  await world.createExchange("Olga", OPEN);
  for (const name of PARTICIPANTS) {
    await world.asUser(name);
    for (const exchange of [DRAWN, OPEN]) {
      await world.slash("/ge join", { exchange: { choose: exchange } });
      await expect(world.messageWithContent(texts.format(texts.joined, exchange))).toBeVisible();
    }
  }
  await world.asUser("Olga");
  await world.slash("/ge draw", { exchange: { choose: DRAWN } });
  await expect(world.messageWithContent(texts.format(texts.confirmDraw, DRAWN, PARTICIPANTS.length))).toBeVisible();
  await world.clickButton(texts.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.drawnPublic, DRAWN))).toBeVisible();
  const round = await world.recipientsFromDms(PARTICIPANTS);

  await world.asUser("Alice");
  expect(await world.autocompleteChoices("/ge ask", "exchange")).toEqual([DRAWN]);

  await world.slash("/ge ask", { question: QUESTION });
  const reply = world.messageWithContent(texts.format(texts.asked, DRAWN));
  await expect(reply).toBeVisible();
  await expect(reply).not.toContainText(round.get("Alice") ?? "");

  const recipient = round.get("Alice") ?? "";
  await world.asUser(recipient);
  await world.openDirectMessages();
  const question = world.messageWithContentContaining(texts.format(texts.dmQuestion, DRAWN, GUILD_NAME));
  await expect(question).toBeVisible({ timeout: DM_BUDGET_MS });
  await expect(question).toContainText(QUESTION);
  await expect(question).not.toContainText("Alice");
  await expect(question.getByTestId("mention")).toHaveCount(0);

  await world.clickButton(texts.buttonAnswer);
  await world.submitModal({ answer: ANSWER });
  await expect(question).toContainText(texts.yourAnswer);
  await expect(question).toContainText(ANSWER);
  await expect(question.getByRole("button", { name: texts.buttonAnswer })).toHaveCount(0);

  await world.asUser("Alice");
  await world.openDirectMessages();
  const answer = world.messageWithContentContaining(texts.format(texts.dmAnswer, DRAWN, GUILD_NAME));
  await expect(answer).toBeVisible({ timeout: DM_BUDGET_MS });
  await expect(answer).toContainText(QUESTION);
  await expect(answer).toContainText(ANSWER);

  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});
