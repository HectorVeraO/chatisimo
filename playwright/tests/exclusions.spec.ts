import { expect, test, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const RULES_TITLE = texts.format(texts.rulesTitle, EXCHANGE);

// Alice organizes; Bruno and Carla join.
async function createExchangeWithTwoParticipants(world: World): Promise<void> {
  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });
  await expect(world.messageWithEmbed(EXCHANGE)).toBeVisible();

  for (const name of ["Bruno", "Carla"]) {
    await world.asUser(name);
    await world.clickButton(texts.buttonJoin);
    await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();
  }
}

// The Exclusions field the view ends with; the first one is the rule row.
function exclusionsOf(world: World, view: ReturnType<World["messageWithEmbed"]>) {
  return world.fieldValue(view, texts.exclusionsField, 1);
}

test("the organizer sees who cannot draw whom and a participant sees only the count", async ({ world }) => {
  await createExchangeWithTwoParticipants(world);

  await world.asUser("Alice");
  await world.slash("/ge exclude", { giver: { user: "Bruno" }, recipient: { user: "Carla" } });
  await expect(world.messageWithContent(texts.format(texts.excluded, "@Bruno", "@Carla"))).toBeVisible();

  await world.clickButton(texts.buttonRules);
  const organizerView = world.messageWithEmbed(RULES_TITLE);
  await expect(organizerView).toBeVisible();
  await expect(exclusionsOf(world, organizerView)).toHaveText(`@Bruno${texts.rulesPairArrow}@Carla`);

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonRules);
  const participantView = world.messageWithEmbed(RULES_TITLE);
  await expect(participantView).toBeVisible();
  await expect(exclusionsOf(world, participantView)).toHaveText("1");
  await expect(exclusionsOf(world, participantView).getByTestId("mention")).toHaveCount(0);
});

test("a participant who runs /ge exclude is refused and the rules stay unchanged", async ({ world }) => {
  await createExchangeWithTwoParticipants(world);

  await world.asUser("Bruno");
  await world.slash("/ge exclude", { giver: { user: "Bruno" }, recipient: { user: "Carla" } });
  await expect(world.messageWithContent(texts.noCandidates)).toBeVisible();
  await expect(world.messageWithContent(texts.format(texts.excluded, "@Bruno", "@Carla"))).toHaveCount(0);

  await world.asUser("Alice");
  await world.clickButton(texts.buttonRules);
  const organizerView = world.messageWithEmbed(RULES_TITLE);
  await expect(organizerView).toBeVisible();
  await expect(exclusionsOf(world, organizerView)).toHaveText("0");
});

test("a mutual exclusion excludes both directions", async ({ world }) => {
  await createExchangeWithTwoParticipants(world);

  await world.asUser("Alice");
  await world.slash("/ge exclude", { giver: { user: "Bruno" }, recipient: { user: "Carla" }, both: true });
  await expect(
    world.messageWithContent(texts.format(texts.excluded, "@Bruno", "@Carla") + texts.excludedBoth),
  ).toBeVisible();

  await world.clickButton(texts.buttonRules);
  const bothView = world.messageWithEmbed(RULES_TITLE);
  await expect(exclusionsOf(world, bothView)).toHaveText(
    [`@Bruno${texts.rulesPairArrow}@Carla`, `@Carla${texts.rulesPairArrow}@Bruno`].join(" "),
  );
});

test("unexclude removes one direction of a mutual exclusion", async ({ world }) => {
  await createExchangeWithTwoParticipants(world);

  await world.asUser("Alice");
  await world.slash("/ge exclude", { giver: { user: "Bruno" }, recipient: { user: "Carla" }, both: true });
  await world.slash("/ge unexclude", { giver: { user: "Bruno" }, recipient: { user: "Carla" } });
  await expect(world.messageWithContent(texts.format(texts.unexcluded, "@Bruno", "@Carla"))).toBeVisible();

  await world.clickButton(texts.buttonRules);
  const afterView = world.messageWithEmbed(RULES_TITLE);
  await expect(exclusionsOf(world, afterView)).toHaveText(`@Carla${texts.rulesPairArrow}@Bruno`);
});
