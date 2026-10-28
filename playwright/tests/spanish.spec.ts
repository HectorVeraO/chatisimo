import { expect, test, DM_BUDGET_MS, GUILD_NAME } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Intercambio de invierno";
const PARTICIPANTS = ["Alice", "Bruno", "Carla"];
const QUESTION = "¿Te gusta el té?";
const ANSWER = "Sí, el té verde.";

test.use({ cast: ["Olga", ...PARTICIPANTS] });

test("an es-419 server runs a full round in Spanish", async ({ world, page }) => {
  await world.asUser("Olga");
  await world.setUserLocale("es-419");
  await world.setServerLocale("es-419");

  await world.slash("/ge join");
  await expect(world.messageWithContent(texts.es.noCandidates)).toBeVisible();

  await world.slash("/ge create", { name: EXCHANGE });
  await expect(world.messageWithContent(texts.format(texts.es.created, EXCHANGE))).toBeVisible();

  const card = world.card(EXCHANGE);
  await expect(card).toBeVisible();
  await expect(card.getByTestId("embed-description")).toHaveText(texts.format(texts.es.cardBody, "@Olga"));
  await expect(world.fieldValue(card, texts.es.fieldDraw)).toHaveText(texts.es.manual);
  await expect(world.fieldValue(card, texts.es.fieldReveal)).toHaveText(texts.es.manual);
  for (const label of [texts.es.buttonJoin, texts.es.buttonLeave, texts.es.buttonRules]) {
    await expect(card.getByRole("button", { name: label, exact: true })).toBeVisible();
  }

  await world.clickButton(texts.es.buttonRules);
  const rulesView = world.messageWithEmbed(texts.format(texts.es.rulesTitle, EXCHANGE));
  await expect(rulesView).toBeVisible();
  await expect(world.fieldValue(rulesView, texts.es.ruleLanguage)).toHaveText("es-419");

  // Participants keep en-US user locale; the exchange language governs all exchange messages.
  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.card(EXCHANGE).getByRole("button", { name: texts.es.buttonJoin, exact: true }).click();
    await expect(world.messageWithContent(texts.format(texts.es.joined, EXCHANGE))).toBeVisible();
  }

  const drawPrompt = texts.format(texts.es.confirmDraw, EXCHANGE, PARTICIPANTS.length);
  await world.asUser("Olga");
  await world.slash("/ge draw", { exchange: { choose: EXCHANGE } });
  await expect(world.messageWithContent(drawPrompt)).toBeVisible();
  await world.clickButton(texts.es.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.es.drawn, EXCHANGE))).toBeVisible();
  await expect(world.messageWithContent(texts.format(texts.es.drawnPublic, EXCHANGE))).toBeVisible();

  const round = await world.recipientsFromDms(PARTICIPANTS);
  for (const giver of PARTICIPANTS) {
    await world.asUser(giver);
    await world.openDirectMessages();
    const dm = world.messageWithEmbed(texts.es.titleRecipient);
    await expect(dm).toHaveCount(1, { timeout: DM_BUDGET_MS });
    await expect(dm.getByTestId("message-content")).toHaveText(texts.format(texts.es.dmDrawn, EXCHANGE, GUILD_NAME));
    await expect(world.fieldValue(dm, texts.es.fieldReveal)).toHaveText(texts.es.manual);
  }

  await world.asUser("Alice");
  await world.slash("/ge recipient");
  const recipientReply = world.messageWithEmbed(texts.es.titleRecipient).filter({
    has: page.getByTestId("ephemeral-note"),
  });
  await expect(recipientReply).toBeVisible();
  await expect(recipientReply.getByTestId("embed-description")).toHaveText(`@${round.get("Alice")}`);

  await world.slash("/ge ask", { question: QUESTION });
  await expect(world.messageWithContent(texts.format(texts.es.asked, EXCHANGE))).toBeVisible();

  const aliceRecipient = round.get("Alice") ?? "";
  await world.asUser(aliceRecipient);
  await world.openDirectMessages();
  const questionDm = world.messageWithContentContaining(texts.format(texts.es.dmQuestion, EXCHANGE, GUILD_NAME));
  await expect(questionDm).toBeVisible({ timeout: DM_BUDGET_MS });
  await expect(questionDm).toContainText(QUESTION);

  await world.clickButton(texts.es.buttonAnswer);
  await expect(page.getByTestId("modal-title")).toHaveText(texts.es.questionModalTitle);
  await world.submitModal({ answer: ANSWER });
  await expect(questionDm).toContainText(texts.es.yourAnswer);
  await expect(questionDm).toContainText(ANSWER);

  await world.asUser("Alice");
  await world.openDirectMessages();
  const answerDm = world.messageWithContentContaining(texts.format(texts.es.dmAnswer, EXCHANGE, GUILD_NAME));
  await expect(answerDm).toBeVisible({ timeout: DM_BUDGET_MS });
  await expect(answerDm).toContainText(QUESTION);
  await expect(answerDm).toContainText(ANSWER);

  const revealPrompt = texts.format(texts.es.confirmReveal, EXCHANGE);
  await world.asUser("Olga");
  await world.slash("/ge reveal", { exchange: { choose: EXCHANGE } });
  await expect(world.messageWithContent(revealPrompt)).toBeVisible();
  await world.clickButton(texts.es.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.es.revealed, EXCHANGE))).toBeVisible();
  await expect(world.messageWithContent(texts.format(texts.es.revealedPublic, EXCHANGE))).toBeVisible();

  const givers = new Map<string, string>();
  for (const [giver, recipient] of round) {
    givers.set(recipient, giver);
  }

  for (const participant of PARTICIPANTS) {
    await world.asUser(participant);
    await world.openDirectMessages();
    const dm = world.messageWithEmbed(texts.es.titleGiver);
    await expect(dm).toHaveCount(1, { timeout: DM_BUDGET_MS });
    await expect(dm.getByTestId("message-content")).toHaveText(
      texts.format(texts.es.dmRevealed, EXCHANGE, GUILD_NAME),
    );
    await expect(dm.getByTestId("embed-description")).toHaveText(`@${givers.get(participant)}`);
  }

  await world.asUser("Alice");
  await world.slash("/ge giver");
  const giverReply = world.messageWithEmbed(texts.es.titleGiver).filter({
    has: page.getByTestId("ephemeral-note"),
  });
  await expect(giverReply).toBeVisible();
  await expect(giverReply.getByTestId("embed-description")).toHaveText(`@${givers.get("Alice")}`);

  await world.expectConfidential([round], ["Olga"], PARTICIPANTS);
});
