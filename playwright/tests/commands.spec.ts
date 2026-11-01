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

test("a server manager configures /uc-config and /uc defaults to es-CL until switched to en-US", async ({ world }) => {
  const alice = world.user("Alice");
  const bruno = world.user("Bruno");

  await world.asUser("Bruno");
  await world.slash("/uc-config set", { friend: { user: "Carla" } });
  await expect(world.ephemeralMessages).toHaveCount(1);

  // Before /uc-config set, /uc posts publicly in es-CL with UC white/blue circles even for en-US users.
  await world.slash("/uc");
  await expect(world.messageWithContentContaining("⚪🔵")).toBeVisible();
  await expect(world.messageWithContentContaining("el hincha cruzado")).toBeVisible();

  await world.control.setMemberPermissions(world.guild.id, alice.id, "32");
  await world.asUser("Alice");
  await expect(world.ephemeralMessages).toHaveCount(0);

  await world.slash("/uc-config set", {
    friend: { user: "Carla" },
    channel: { channel: world.guild.channel_id },
    locale: "en-US",
  });
  await expect(world.ephemeralMessages).toHaveCount(1);

  await world.slash("/uc");
  await expect(world.messageWithContentContaining("All clear")).toBeVisible();

  await world.asUser("Bruno");
  await expect(world.ephemeralMessages).toHaveCount(1);

  const records = await world.castRecords();
  expect(records).toHaveLength(4);
  for (const record of records) {
    expect(record.pings).toEqual([]);
  }
  expect(records[0]?.ephemeral).toBe(true);
  expect(records[1]?.ephemeral).toBe(false);
  expect(records[2]?.ephemeral).toBe(true);
  expect(records[3]?.ephemeral).toBe(false);
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
