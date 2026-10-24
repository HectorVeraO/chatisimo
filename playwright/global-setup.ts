import { expect, request, type FullConfig } from "@playwright/test";

const READY_BUDGET_MS = 60_000;

// Waits until the fake reports that the bot is ready, so no spec starts against a stack that is
// still registering its commands.
export default async function globalSetup(config: FullConfig): Promise<void> {
  const baseURL = config.projects[0]?.use.baseURL;
  expect(baseURL, "the first project must set use.baseURL").toBeTruthy();

  const api = await request.newContext({ baseURL });
  try {
    await expect
      .poll(
        async () => {
          try {
            const response = await api.get("/_control/healthz?bot=1");
            return response.status();
          } catch {
            return 0;
          }
        },
        {
          message: "the fake Discord never reported a ready bot; is the e2e stack up?",
          timeout: READY_BUDGET_MS,
          intervals: [500, 1_000, 2_000],
        },
      )
      .toBe(200);
  } finally {
    await api.dispose();
  }
}
