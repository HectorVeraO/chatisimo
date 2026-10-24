import { expect, test, DM_BUDGET_MS, GUILD_NAME, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";

function listLine(state: string, participants: number): string {
  return texts.format(texts.listLine, EXCHANGE, state, participants, texts.roleOrganizer);
}

// Olga organizes without joining; the participants join and the draw runs.
async function drawnExchange(world: World, participants: string[]) {
  await world.createExchange("Olga", EXCHANGE, participants);
  await world.draw("Olga", EXCHANGE, participants.length);
  return world.recipientsFromDms(participants);
}

// The organizer removes a participant after the draw and confirms.
async function removeAfterDraw(world: World, target: string, outcome: string): Promise<void> {
  await world.asUser("Olga");
  await world.slash("/ge remove", { participant: { user: target } });
  await expect(
    world.messageWithContent(texts.format(texts.confirmRemove, EXCHANGE, `@${target}`)),
  ).toBeVisible();
  await world.clickButton(texts.buttonConfirm);
  await expect(world.messageWithContent(outcome)).toBeVisible();
}

test.describe("five participants and the patch policy", () => {
  test.use({ cast: ["Olga", "Alice", "Bruno", "Carla", "Dario", "Erik"] });

  test("a leaver's giver alone gets the leaver's recipient", async ({ world }) => {
    const participants = ["Alice", "Bruno", "Carla", "Dario", "Erik"];
    const round = await drawnExchange(world, participants);

    // A patch needs the leaver's giver and recipient to be different people; a two-person
    // cycle makes the module redraw instead, so the leaver is chosen outside of one.
    const giverOf = new Map([...round].map(([giver, recipient]) => [recipient, giver]));
    const leaver = participants.find((name) => giverOf.get(name) !== round.get(name));
    expect(leaver).toBeDefined();
    const left = leaver ?? "";
    const giver = giverOf.get(left) ?? "";
    const inheritedRecipient = round.get(left) ?? "";

    await world.asUser(left);
    await world.slash("/ge leave");
    await expect(world.messageWithContent(texts.format(texts.confirmLeave, EXCHANGE))).toBeVisible();
    await world.clickButton(texts.buttonConfirm);
    await expect(world.messageWithContent(texts.format(texts.left, EXCHANGE))).toBeVisible();

    await world.asUser(giver);
    await world.openDirectMessages();
    const patched = world.messageWithEmbed(texts.titleNewRecipient);
    await expect(patched).toHaveCount(1, { timeout: DM_BUDGET_MS });
    await expect(patched.getByTestId("message-content")).toHaveText(
      texts.format(texts.dmChanged, EXCHANGE, GUILD_NAME),
    );
    await expect(patched.getByTestId("embed-description")).toHaveText(`@${inheritedRecipient}`);

    await world.letLateDmsArrive();
    for (const other of participants.filter((name) => name !== giver)) {
      await world.asUser(other);
      await world.openDirectMessages();
      await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(0);
      await expect(world.messageWithEmbed(texts.titleRecipient)).toHaveCount(1);
    }

    const patchedRound = new Map(round);
    patchedRound.delete(left);
    patchedRound.set(giver, inheritedRecipient);
    await world.expectConfidential([round, patchedRound], ["Olga"], participants);
  });
});

test.describe("four participants", () => {
  test.use({ cast: ["Olga", "Alice", "Bruno", "Carla", "Dario"] });

  test("a removal that leaves three redraws for every remaining giver", async ({ world }) => {
    const participants = ["Alice", "Bruno", "Carla", "Dario"];
    const round = await drawnExchange(world, participants);

    await removeAfterDraw(world, "Alice", texts.outcomeRedrawn);

    const remaining = participants.filter((name) => name !== "Alice");
    for (const name of remaining) {
      await world.asUser(name);
      await world.openDirectMessages();
      const redrawn = world.messageWithEmbed(texts.titleNewRecipient);
      await expect(redrawn).toHaveCount(1, { timeout: DM_BUDGET_MS });
      await expect(redrawn.getByTestId("message-content")).toHaveText(
        texts.format(texts.dmRedrawn, EXCHANGE, GUILD_NAME),
      );
    }

    await world.asUser("Alice");
    await world.openDirectMessages();
    await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(0);

    const redrawnRound = await world.recipientsFromDms(remaining);
    expect([...redrawnRound.values()].sort()).toEqual([...remaining].sort());
    for (const [giver, recipient] of redrawnRound) {
      expect(recipient).not.toBe(giver);
    }
    await world.expectConfidential([round, redrawnRound], ["Olga"], participants);
  });

  test("the organizer cannot remove their own recipient", async ({ world }) => {
    const participants = ["Alice", "Bruno", "Carla", "Dario"];
    await world.createExchange("Alice", EXCHANGE, participants);
    await world.draw("Alice", EXCHANGE, participants.length);
    const round = await world.recipientsFromDms(participants);
    const recipient = round.get("Alice") ?? "";

    await world.asUser("Alice");
    await world.slash("/ge remove", { participant: { user: recipient } });
    await expect(
      world.messageWithContent(texts.format(texts.confirmRemove, EXCHANGE, `@${recipient}`)),
    ).toBeVisible();
    await world.clickButton(texts.buttonConfirm);
    await expect(world.messageWithContent(texts.ownRecipient)).toBeVisible();

    // Nobody got a new recipient: the draw stands.
    for (const name of participants) {
      await world.asUser(name);
      await world.openDirectMessages();
      await expect(world.messageWithEmbed(texts.titleNewRecipient)).toHaveCount(0);
    }
    await world.expectConfidential([round], [], participants);
  });
});

test.describe("three participants", () => {
  test.use({ cast: ["Olga", "Alice", "Bruno", "Carla"] });

  test("a removal below the minimum undoes the draw and tells the organizer and the givers", async ({ world }) => {
    const participants = ["Alice", "Bruno", "Carla"];
    await drawnExchange(world, participants);

    await removeAfterDraw(world, "Alice", texts.format(texts.outcomeReopened, EXCHANGE));

    const voided = texts.format(texts.dmVoided, EXCHANGE, GUILD_NAME);
    for (const name of ["Olga", "Bruno", "Carla"]) {
      await world.asUser(name);
      await world.openDirectMessages();
      await expect(world.messageWithContentContaining(voided)).toBeVisible({ timeout: DM_BUDGET_MS });
    }

    await world.asUser("Alice");
    await world.openDirectMessages();
    await expect(world.messageWithContentContaining(voided)).toHaveCount(0);

    await world.asUser("Olga");
    await world.slash("/ge list");
    await expect(world.messageWithEmbed(texts.listTitle).getByTestId("embed-description")).toHaveText(
      listLine("open", 2),
    );
  });
});
