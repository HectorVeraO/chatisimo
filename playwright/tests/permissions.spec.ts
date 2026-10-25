import { expect, test } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";

test.use({ cast: ["Olga", "Alice", "Bruno", "Carla"] });

test("a participant cannot draw, cancel or change rules, and nothing changes or is sent", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, ["Alice", "Bruno"]);
  const exchangeId = await world.exchangeId(EXCHANGE);
  const cardId = await world.cardMessageId(EXCHANGE);
  const bruno = world.user("Bruno");
  const refused = world.messageWithContent(texts.forbidden);

  // The command: Bruno names the exchange by its id, as nothing offers it to him.
  await world.asUser("Bruno");
  await world.slash("/ge draw", { exchange: exchangeId });
  await expect(refused).toHaveCount(1);
  await expect(world.messages.getByRole("button", { name: texts.buttonConfirm, exact: true })).toHaveCount(0);

  // Forged clicks on the confirm buttons of the draw and the cancel.
  for (const [position, action] of ["draw-confirm", "cancel-confirm"].entries()) {
    const result = await world.control.interact({
      as: bruno.id,
      kind: "button",
      message_id: cardId,
      custom_id: `/gx/${action}/${exchangeId}`,
      forged: true,
    });
    expect(result.status).not.toBe("rejected");
    await expect(refused).toHaveCount(position + 2);
  }

  // A forged choice of a rule value, then a forged pick of the time rule, which would open
  // the modal for the draw time.
  const choice = await world.control.interact({
    as: bruno.id,
    kind: "select",
    message_id: cardId,
    custom_id: `/gx/rule-option/${exchangeId}/dropout_policy`,
    values: ["redraw"],
    forged: true,
  });
  expect(choice.status).not.toBe("rejected");
  await expect(refused).toHaveCount(4);

  const pick = await world.control.interact({
    as: bruno.id,
    kind: "select",
    message_id: cardId,
    custom_id: `/gx/rule-pick/${exchangeId}`,
    values: ["draw_at"],
    forged: true,
  });
  expect(pick.status).not.toBe("rejected");
  expect(pick.modal).toBeUndefined();
  await expect(refused).toHaveCount(5);
  await expect(world.page.getByTestId("modal")).toBeHidden();

  // The exchange is as it was: open, with two participants, the default policy, no draw time.
  await world.asUser("Olga");
  await world.slash("/ge list");
  await expect(world.messageWithEmbed(texts.listTitle).getByTestId("embed-description")).toHaveText(
    texts.format(texts.listLine, EXCHANGE, "open", 2, texts.roleOrganizer),
  );
  await world.clickButton(texts.buttonRules);
  const rules = world.messageWithEmbed(texts.format(texts.rulesTitle, EXCHANGE));
  await expect(world.fieldValue(rules, texts.ruleDropoutPolicy)).toHaveText("patch" + texts.suffixDefault);
  await expect(world.fieldValue(rules, texts.ruleDrawAt)).toHaveText(texts.manual + texts.suffixDefault);

  // The bot sent no DM and no public message after the card: every record the attempts caused
  // is an ephemeral reply to Bruno.
  const records = await world.castRecords();
  expect(records.filter((record) => record.source === "dm")).toHaveLength(0);
  const refusals = records.filter((record) => record.content === texts.forbidden);
  expect(refusals.length).toBeGreaterThanOrEqual(5);
  for (const record of refusals) {
    expect(record.ephemeral).toBe(true);
    expect(record.visible_to).toEqual([bruno.id]);
  }
});

test("a participant cannot reveal an exchange or remove a participant, and nothing changes", async ({ world }) => {
  await world.createExchange("Olga", EXCHANGE, ["Alice", "Bruno", "Carla"]);
  await world.draw("Olga", EXCHANGE, 3);
  const exchangeId = await world.exchangeId(EXCHANGE);
  const bruno = world.user("Bruno");
  const refused = world.messageWithContent(texts.forbidden);

  await world.asUser("Bruno");
  await world.slash("/ge reveal", { exchange: exchangeId });
  await expect(refused).toHaveCount(1);
  await world.slash("/ge remove", { exchange: exchangeId, participant: { user: "Alice" } });
  await expect(refused).toHaveCount(2);
  await expect(world.messages.getByRole("button", { name: texts.buttonConfirm, exact: true })).toHaveCount(0);

  // The exchange is still drawn, with both participants.
  await world.asUser("Olga");
  await world.slash("/ge list");
  await expect(world.messageWithEmbed(texts.listTitle).getByTestId("embed-description")).toHaveText(
    texts.format(texts.listLine, EXCHANGE, "drawn", 3, texts.roleOrganizer),
  );

  const records = await world.castRecords();
  const refusals = records.filter((record) => record.content === texts.forbidden);
  expect(refusals).toHaveLength(2);
  for (const record of refusals) {
    expect(record.ephemeral).toBe(true);
    expect(record.visible_to).toEqual([bruno.id]);
  }
  expect(records.some((record) => record.content.includes(texts.format(texts.revealed, EXCHANGE)))).toBe(false);
});
