import { expect, test, type World } from "../fixtures/world";
import * as texts from "../fixtures/texts";

const EXCHANGE = "Winter exchange";
const TWO_HOURS_S = 2 * 60 * 60;
const CLOCK_SKEW_S = 5 * 60;

const RULES_TITLE = texts.format(texts.rulesTitle, EXCHANGE);

async function createExchange(world: World): Promise<void> {
  await world.asUser("Alice");
  await world.slash("/ge create", { name: EXCHANGE });
  await expect(world.messageWithEmbed(EXCHANGE)).toBeVisible();
}

// Opens the rules view from the card and returns it.
async function openRules(world: World) {
  await world.clickButton(texts.buttonRules);
  const view = world.messageWithEmbed(RULES_TITLE);
  await expect(view).toBeVisible();
  return view;
}

test("a participant sees the rules and has no select to change them", async ({ world }) => {
  await createExchange(world);

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();

  const view = await openRules(world);
  await expect(view.getByTestId("embed-description")).toHaveText(
    texts.format(texts.rulesState, "open", 1, texts.roleParticipant),
  );
  await expect(world.fieldValue(view, texts.ruleDropoutPolicy)).toHaveText("patch" + texts.suffixDefault);
  await expect(world.fieldValue(view, texts.ruleMinParticipants)).toHaveText(
    "3" + texts.suffixDefault + texts.suffixFixed,
  );
  await expect(view.getByTestId("component-select")).toHaveCount(0);
});

test("the organizer changes the dropout policy with a select", async ({ world }) => {
  await createExchange(world);

  const view = await openRules(world);
  await expect(view.getByTestId("embed-description")).toHaveText(
    texts.format(texts.rulesState, "open", 0, texts.roleOrganizer),
  );
  await expect(world.fieldValue(view, texts.ruleDropoutPolicy)).toHaveText("patch" + texts.suffixDefault);

  await world.choose(texts.selectPlaceholder, texts.ruleDropoutPolicy);
  await world.choose(texts.ruleDropoutPolicy, "redraw");

  await expect(world.fieldValue(view, texts.ruleDropoutPolicy)).toHaveText("redraw");

  await world.asUser("Bruno");
  await world.clickButton(texts.buttonJoin);
  await expect(world.messageWithContent(texts.format(texts.joined, EXCHANGE))).toBeVisible();
  await world.clickButton(texts.buttonRules);
  const participantView = world.messageWithEmbed(RULES_TITLE);
  await expect(world.fieldValue(participantView, texts.ruleDropoutPolicy)).toHaveText("redraw");
});

test("the organizer sets the draw time in a modal", async ({ world, page }) => {
  await createExchange(world);
  const view = await openRules(world);

  const submittedAt = Math.floor(Date.now() / 1000);
  await world.choose(texts.selectPlaceholder, texts.ruleDrawAt);
  await expect(page.getByTestId("modal-title")).toHaveText(texts.format(texts.modalTitle, texts.ruleDrawAt));
  await expect(page.getByTestId("modal-field")).toHaveCount(1);
  await world.submitModal({ value: "in 2h" });

  const timestamp = world.fieldValue(view, texts.ruleDrawAt).getByTestId("timestamp").first();
  await expect(timestamp).toBeVisible();
  const acceptable = await page.evaluate(
    ([from, to]) => {
      const format = new Intl.DateTimeFormat(undefined, {
        weekday: "long",
        year: "numeric",
        month: "long",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
      });
      const texts: string[] = [];
      for (let unix = from; unix <= to; unix += 60) {
        texts.push(format.format(new Date(unix * 1000)));
      }
      texts.push(format.format(new Date(to * 1000)));
      return texts;
    },
    [submittedAt + TWO_HOURS_S - CLOCK_SKEW_S, submittedAt + TWO_HOURS_S + CLOCK_SKEW_S],
  );
  expect(acceptable).toContain((await timestamp.innerText()).trim());
  await expect(world.fieldValue(view, texts.ruleDrawAt)).not.toContainText(texts.manual);
});

test("an invalid draw time is not saved and the view lists the problem", async ({ world }) => {
  await createExchange(world);
  const view = await openRules(world);

  await world.choose(texts.selectPlaceholder, texts.ruleDrawAt);
  await world.submitModal({ value: "someday" });
  await expect(view.getByTestId("message-content")).toHaveText(
    `${texts.notSaved} ${texts.format(texts.notSavedLine, `draw_at ${texts.rulesBadTimeReason}`)}`,
  );
  await expect(world.fieldValue(view, texts.ruleDrawAt)).toHaveText(texts.manual + texts.suffixDefault);

  await world.choose(texts.selectPlaceholder, texts.ruleDrawAt);
  await world.submitModal({ value: "2020-01-01 10:00" });
  await expect(view.getByTestId("message-content")).toHaveText(
    `${texts.notSaved} ${texts.format(texts.notSavedLine, `draw_at ${texts.mustBeInTheFuture}`)}`,
  );
  await expect(world.fieldValue(view, texts.ruleDrawAt)).toHaveText(texts.manual + texts.suffixDefault);
});
