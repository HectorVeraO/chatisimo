import type { Page } from "@playwright/test";
import { expect, test } from "../fixtures/world";

const VERBS = [
  "create",
  "list",
  "join",
  "leave",
  "remove",
  "rules",
  "exclude",
  "unexclude",
  "rename",
  "draw",
  "recipient",
  "giver",
  "ask",
  "questions",
  "preferences",
  "reveal",
  "cancel",
  "help",
];

test("the composer offers the four top-level commands in a server and hides /uc-config in DM", async ({ world }) => {
  const { page } = world;
  const input = page.getByTestId("slash-input");

  await input.fill("/");
  await expect.poll(() => suggestedPaths(page)).toEqual(["/chatisimo", "/ge", "/uc", "/uc-config"]);

  await input.fill("/uc-config ");
  await expect.poll(() => suggestedPaths(page)).toEqual(["/uc-config clear", "/uc-config set", "/uc-config show"]);

  await input.fill("");
  await world.openDirectMessages();
  await input.fill("/");
  await expect.poll(() => suggestedPaths(page)).toEqual(["/chatisimo", "/ge", "/uc"]);
});

test("a server manager configures /uc-config and a regular member is refused ephemerally", async ({ world }) => {
  const alice = world.user("Alice");
  const bruno = world.user("Bruno");

  await world.asUser("Bruno");
  await world.slash("/uc-config set", { friend: { user: "Carla" } });
  await expect(world.ephemeralMessages).toHaveCount(1);

  await world.control.setMemberPermissions(world.guild.id, alice.id, "32");
  await world.asUser("Alice");
  await expect(world.ephemeralMessages).toHaveCount(0);

  await world.slash("/uc-config set", {
    friend: { user: "Carla" },
    channel: { channel: world.guild.channel_id },
  });
  await expect(world.ephemeralMessages).toHaveCount(1);

  await world.asUser("Bruno");
  await expect(world.ephemeralMessages).toHaveCount(1);

  const records = await world.castRecords();
  expect(records).toHaveLength(2);
  for (const record of records) {
    expect(record.ephemeral).toBe(true);
    expect(record.pings).toEqual([]);
  }
});

test("/ge lists the 18 verbs", async ({ world }) => {
  const { page } = world;
  await page.getByTestId("slash-input").fill("/ge ");

  const expected = VERBS.map((verb) => `/ge ${verb}`).sort();
  await expect.poll(() => suggestedPaths(page)).toEqual(expected);
});

test("/chatisimo giftexchange lists the same 18 verbs", async ({ world }) => {
  const { page } = world;
  const input = page.getByTestId("slash-input");

  await input.fill("/chatisimo ");
  await expect.poll(() => suggestedPaths(page)).toEqual(["/chatisimo giftexchange", "/chatisimo uc"]);

  await input.fill("/chatisimo giftexchange ");
  const expected = VERBS.map((verb) => `/chatisimo giftexchange ${verb}`).sort();
  await expect.poll(() => suggestedPaths(page)).toEqual(expected);
});

// The command paths the composer suggests now, sorted.
async function suggestedPaths(page: Page): Promise<string[]> {
  const suggestions = page.getByTestId("slash-suggestion");
  const paths = await suggestions.evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label") ?? ""));
  return paths.sort();
}
