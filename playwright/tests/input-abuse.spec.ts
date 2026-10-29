import { expect, test, DM_BUDGET_MS, GUILD_NAME, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const CAST = ["Alice", "Bruno", "Carla"];

test.use({ cast: CAST });

// Every user of the cast sees the channel without a ping badge.
async function expectNobodyPinged(world: World): Promise<void> {
  for (const name of CAST) {
    await world.asUser(name);
    await expect(world.page.getByTestId("ping-badge")).toHaveCount(0);
  }
}

// The records of the cast that mention the text in their content or embeds never ping.
async function expectNoPingsFor(world: World, text: string): Promise<void> {
  const records = (await world.castRecords()).filter((record) => JSON.stringify(record).includes(text));
  expect(records.length).toBeGreaterThan(0);
  for (const record of records) {
    expect(record.pings).toEqual([]);
    expect(record.allowed_mentions_effective.parse).toEqual([]);
  }
}

test("an exchange named @everyone party is shown as text and pings nobody", async ({ world }) => {
  const name = "@everyone party";
  await world.asUser("Alice");
  await world.slash("/ge create", { name });

  const card = world.card(name);
  await expect(card).toBeVisible();
  await expect(card.getByTestId("embed-title")).toHaveText(name);
  await expect(world.messageWithContent(texts.format(texts.created, name))).toBeVisible();

  await expectNoPingsFor(world, "everyone party");
  await expectNobodyPinged(world);
});

test("an exchange named with a mention of a real user shows it as text and pings nobody", async ({ world }) => {
  const bruno = world.user("Bruno");
  const name = `<@${bruno.id}> trip`;
  await world.asUser("Alice");
  await world.slash("/ge create", { name });

  const created = world.messageWithContent(texts.format(texts.created, name));
  await expect(created).toBeVisible();
  await expect(created.getByTestId("mention")).toHaveCount(0);
  await expect(world.card("@Bruno trip")).toBeVisible();

  await world.asUser("Bruno");
  const chipInContent = world.page.locator(
    `[data-testid="message-content"] [data-testid="mention"][data-user-id="${bruno.id}"]`,
  );
  await expect(chipInContent).toHaveCount(0);
  await expectNoPingsFor(world, "trip");
  await expectNobodyPinged(world);
});

test("a name of 100 characters is accepted and rendered", async ({ world }) => {
  const name = "x".repeat(100);
  await world.asUser("Alice");
  await world.slash("/ge create", { name });

  await expect(world.card(name)).toBeVisible();
});

test("a name of 101 characters is refused by the composer and by the module", async ({ world }) => {
  const name = "x".repeat(101);
  await world.asUser("Alice");
  const input = world.page.getByTestId("slash-input");
  await input.fill("/ge create");
  await world.page.locator('[data-testid="slash-option"][data-option="name"]').fill(name);
  await world.page.getByTestId("slash-submit").click();
  await expect(world.page.getByTestId("slash-error")).toContainText(texts.format(texts.composerTooLong, "name", 100));
  await expect(input).toHaveValue("/ge create");
  await expect(world.messages).toHaveCount(0);

  const result = await world.control.interact({
    as: world.user("Alice").id,
    kind: "slash",
    guild_id: world.guild.id,
    path: "/ge/create",
    options: { name },
    bypass_validation: true,
  });
  expect(result.status).not.toBe("rejected");

  const reply = world.messageWithContentContaining(texts.notSaved);
  await expect(reply).toHaveAttribute("data-ephemeral", "true");
  await expect(reply.getByTestId("message-content")).toHaveText(
    `${texts.notSaved} ${texts.format(texts.notSavedLine, `name ${texts.reasonTooLong}`)}`,
  );
  await expect(world.messageWithEmbed(name)).toHaveCount(0);
});

const ACCEPTED_NAMES: [string, string][] = [
  ["right-to-left", "تبادل الهدايا"],
  ["combining marks", "Café de amigos"],
  ["emoji", "Fiesta \u{1F389}\u{1F381}"],
];

for (const [label, name] of ACCEPTED_NAMES) {
  test(`a ${label} name is accepted and rendered`, async ({ world }) => {
    await world.asUser("Alice");
    await world.slash("/ge create", { name });

    const card = world.card(name);
    await expect(card).toBeVisible();
    await expect(card.getByTestId("embed-title")).toHaveText(name);
  });
}

const REFUSED_NAMES: [string, string, string[]][] = [
  ["a bidirectional override", "Party ‮odnum", [texts.reasonBidi]],
  ["only zero-width characters", "​‍​", [texts.reasonNoVisible]],
];

for (const [label, name, reasons] of REFUSED_NAMES) {
  test(`a name with ${label} is refused with the module's message`, async ({ world }) => {
    await world.asUser("Alice");
    await world.slash("/ge create", { name });

    const reply = world.messageWithContentContaining(texts.notSaved);
    await expect(reply).toHaveAttribute("data-ephemeral", "true");
    const lines = reasons.map((reason) => texts.format(texts.notSavedLine, `name ${reason}`));
    await expect(reply.getByTestId("message-content")).toHaveText(`${texts.notSaved} ${lines.join(" ")}`);
    await expect(world.messages).toHaveCount(1);
  });
}

test("a question with @here and a mention reaches the recipient as text and pings nobody", async ({ world }) => {
  const exchange = "Winter exchange";
  await world.createExchange("Alice", exchange, CAST);
  await world.draw("Alice", exchange, CAST.length);
  const round = await world.recipientsFromDms(CAST);

  const mentioned = world.user("Carla");
  const question = `Hello @here and <@${mentioned.id}>`;
  await world.asUser("Bruno");
  await world.slash("/ge ask", { question });
  await expect(world.messageWithContent(texts.format(texts.asked, exchange))).toBeVisible();

  const recipient = round.get("Bruno") ?? "";
  await world.asUser(recipient);
  await world.openDirectMessages();
  const dm = world.messageWithContentContaining(texts.format(texts.dmQuestion, exchange, GUILD_NAME));
  await expect(dm).toBeVisible({ timeout: DM_BUDGET_MS });
  await expect(dm).toContainText("Hello @here and <@");
  await expect(dm.getByTestId("mention")).toHaveCount(0);
  await expect(dm.getByTestId("ping-badge")).toHaveCount(0);

  const records = (await world.castRecords()).filter(
    (record) => record.source === "dm" && record.content.includes("Hello"),
  );
  expect(records).toHaveLength(1);
  expect(records[0]?.pings).toEqual([]);
  expect(records[0]?.allowed_mentions_effective.parse).toEqual([]);
  expect(records[0]?.mentions_rendered).toEqual([]);
});

test("custom Discord emojis in a question render as custom-emoji elements while escaped emojis and mentions stay literal", async ({
  world,
}) => {
  const exchange = "Winter exchange";
  await world.createExchange("Alice", exchange, CAST);
  await world.draw("Alice", exchange, CAST.length);
  const round = await world.recipientsFromDms(CAST);

  const mentioned = world.user("Carla");
  const question = `Look <:CatHmph:123456789012345678> <a:cat_dance:123456789012345678> <@${mentioned.id}> *bold* <:a:123>`;
  await world.asUser("Bruno");
  await world.slash("/ge ask", { question });
  await expect(world.messageWithContent(texts.format(texts.asked, exchange))).toBeVisible();

  const recipient = round.get("Bruno") ?? "";
  await world.asUser(recipient);
  await world.openDirectMessages();
  const dm = world.messageWithContentContaining(texts.format(texts.dmQuestion, exchange, GUILD_NAME));
  await expect(dm).toBeVisible({ timeout: DM_BUDGET_MS });

  const emojis = dm.getByTestId("custom-emoji");
  await expect(emojis).toHaveCount(2);
  await expect(emojis.nth(0)).toHaveAttribute("data-emoji-name", "CatHmph");
  await expect(emojis.nth(0)).toHaveAttribute("data-emoji-id", "123456789012345678");
  await expect(emojis.nth(0)).toHaveAttribute("data-animated", "false");
  await expect(emojis.nth(0)).toHaveText(":CatHmph:");
  await expect(emojis.nth(1)).toHaveAttribute("data-emoji-name", "cat_dance");
  await expect(emojis.nth(1)).toHaveAttribute("data-emoji-id", "123456789012345678");
  await expect(emojis.nth(1)).toHaveAttribute("data-animated", "true");
  await expect(emojis.nth(1)).toHaveText(":cat_dance:");

  await expect(dm.getByTestId("mention")).toHaveCount(0);
  await expect(dm).toContainText("*bold* <:a:123>");
});

test("a repeated Join click and a repeated draw confirmation each get an error and change nothing", async ({
  world,
}) => {
  const exchange = "Winter exchange";
  await world.createExchange("Alice", exchange, ["Bruno", "Carla"]);

  await world.asUser("Bruno");
  await world.card(exchange).getByRole("button", { name: texts.buttonJoin, exact: true }).click();
  await expect(world.messageWithContent(texts.alreadyParticipant)).toBeVisible();

  const exchangeId = await world.exchangeId(exchange);
  await world.asUser("Alice");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, exchange))).toBeVisible();
  await world.slash("/ge draw");
  const prompt = world.messageWithContent(texts.format(texts.confirmDraw, exchange, 3));
  await expect(prompt).toBeVisible();
  const promptId = await prompt.getAttribute("data-message-id");
  expect(promptId).not.toBeNull();
  await world.clickButton(texts.buttonConfirm);
  await expect(world.messageWithContent(texts.format(texts.drawnPublic, exchange))).toBeVisible();

  // The first click removed the button from the prompt, so the second one is a replay.
  const replay = await world.control.interact({
    as: world.user("Alice").id,
    kind: "button",
    message_id: promptId ?? "",
    custom_id: `/gx/draw-confirm/${exchangeId}`,
    forged: true,
  });
  expect(replay.status).not.toBe("rejected");
  await expect(world.messageWithContent(texts.format(texts.invalidState, "drawn"))).toBeVisible();
  await expect(world.messageWithContent(texts.format(texts.drawnPublic, exchange))).toHaveCount(1);
});

test("an exchange id of another guild or a made-up id is refused and changes nothing", async ({ world }) => {
  const exchange = "Winter exchange";
  await world.createExchange("Alice", exchange, ["Bruno", "Carla"]);

  // An exchange of someone else, in a guild the cast is not in.
  const outsider = await world.control.createUser("Zed");
  const otherGuild = await world.control.createGuild("Other server", [outsider.id]);
  const created = await world.control.interact({
    as: outsider.id,
    kind: "slash",
    guild_id: otherGuild.id,
    path: "/ge/create",
    options: { name: "Foreign exchange" },
  });
  expect(created.status).not.toBe("rejected");
  const joinIdOf = async (): Promise<string> => {
    const messages = await world.control.channelMessages(otherGuild.channel_id, outsider.id);
    return /\/gx\/join\/([\w-]+)/.exec(JSON.stringify(messages))?.[1] ?? JSON.stringify(messages);
  };
  await expect.poll(joinIdOf).toMatch(/^[\w-]+$/);
  const foreignId = await joinIdOf();

  await world.asUser("Alice");
  const attempts: [string, string][] = [
    [foreignId, texts.notFound],
    ["not-an-exchange-id", texts.noMatch],
  ];
  for (const [wrongId, refusal] of attempts) {
    for (const [position, command] of ["/ge draw", "/ge cancel"].entries()) {
      await world.slash(command, { exchange: wrongId });
      await expect(world.messageWithContent(refusal)).toHaveCount(position + 1);
    }
  }

  // Neither exchange changed state and no confirmation was offered.
  await expect(world.messages.getByRole("button", { name: texts.buttonConfirm, exact: true })).toHaveCount(0);
  await world.slash("/ge list");
  await expect(world.messageWithEmbed(texts.listTitle).getByTestId("embed-description")).toHaveText(
    texts.format(texts.listLine, exchange, "open", 2, texts.roleOrganizer),
  );
});
