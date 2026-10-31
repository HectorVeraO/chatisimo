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

test("the composer offers the three top-level commands", async ({ world }) => {
  const { page } = world;
  await page.getByTestId("slash-input").fill("/");

  await expect.poll(() => suggestedPaths(page)).toEqual(["/chatisimo", "/ge", "/uc"]);
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
