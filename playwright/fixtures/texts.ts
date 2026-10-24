// The texts the specs assert. Each constant repeats, byte for byte, a text of the Discord
// adapter, with its Go formatting verbs (%s, %d) left in place; `format` fills them with the
// plain values the page renders (an exchange name arrives in bold, which the page renders as
// plain text, and a mention renders as "@Name").
//
// Source: internal/discord/internal/giftexchangecmd/texts.go, except where a section says
// otherwise. Code Path Parity: .claude/references/code-path-parity.md.

export const created =
  "You created %s and you organize it. You are not a participant unless you join. " +
  "Configure it with /ge rules.";
export const cardBody = "Organized by %s. Join with the button or /ge join.";
export const manual = "not set (organizer runs it)";
export const joined = "You joined %s.";
export const left = "You left %s.";
export const excluded = "Exclusion added: %s will not draw %s.";
export const excludedBoth = " The reverse is excluded too.";
export const unexcluded = "Exclusion removed: %s may draw %s again.";
export const notSaved = "Not saved:";
export const notSavedLine = "- %s";
export const alreadyParticipant = "You are already in this exchange.";
export const noCandidates = "You have no exchange where you can do this.";
export const rulesState = "State: %s. Participants: %d. You are %s.";
export const roleOrganizer = "the organizer";
export const roleParticipant = "a participant";
export const roleBoth = "the organizer and a participant";
export const roleNone = "not in it";

export const ruleDrawAt = "Draw time";
export const ruleDropoutPolicy = "Dropout policy";
export const ruleExclusions = "Exclusions";
export const ruleMinParticipants = "Minimum participants";
export const suffixDefault = " (default)";
export const suffixFixed = " - fixed";

export const buttonJoin = "Join";
export const buttonLeave = "Leave";
export const buttonRules = "Rules";

export const fieldDraw = "Draw";
export const fieldReveal = "Reveal";
export const rulesTitle = "Rules - %s";
export const participantsField = "Participants (%d)";
export const exclusionsField = "Exclusions";
export const noneYet = "None yet.";
export const selectPlaceholder = "Change a rule";
export const modalLabel = "New value";
export const modalTitle = "Set %s";

// Source: internal/discord/internal/giftexchangecmd/list.go.
export const listTitle = "Exchanges in this server";
export const listLine = "- %s - %s - participants: %d - you are %s";

// Source: internal/discord/internal/giftexchangecmd/rules.go.
export const rulesBadTimeReason = "is not a time such as in 3d or 2026-12-20 18:00 (Santiago time)";
export const rulesPairArrow = " -> ";

// Source: internal/giftexchange/rules.go.
export const mustBeInTheFuture = "must be in the future";

// Source: internal/discord/internal/giftexchangecmd/texts.go (flows from the draw to the reveal).
export const confirmDraw = "Draw %s now with %d participants? This can't be undone.";
export const drawn = "Draw done for %s.";
export const drawnPublic =
  "The draw for %s is done. Each giver got a DM with their recipient, or can use /ge recipient.";
export const confirmReveal = "Reveal %s now? Each participant will learn their giver.";
export const revealed = "%s is revealed.";
export const revealedPublic = "%s is revealed. Each participant got a DM with their giver, or can use /ge giver.";
export const confirmCancel = "Cancel %s? This can't be undone.";
export const cancelled = "%s is cancelled.";
export const cancelledPublic = "%s was cancelled by the organizer.";
export const confirmLeave = "%s is already drawn. Leave anyway? Your giver may get a new recipient.";
export const confirmRemove = "%s is already drawn. Remove %s? A giver may get a new recipient.";
export const removed = "Removed %s from %s.";
export const outcomePatched = "Removed. One giver got a new recipient by DM.";
export const outcomeRedrawn = "Removed. The draw was redone and every giver got a DM.";
export const outcomeReopened =
  "Removed. Too few participants or no valid draw remained, so the draw was undone and %s is open again.";
export const ownRecipient = "You can't remove your own recipient. They can leave, or you can cancel the exchange.";
export const asked = "Your question was sent anonymously to your recipient in %s.";
export const yourAnswer = "Your answer:";
export const forbidden = "You are not allowed to do that in this exchange.";
export const notParticipant = "You are not in this exchange.";
export const invalidState = "Not possible while the exchange is %s.";
export const infeasible =
  "No valid draw exists with the current exclusions: %d givers have no possible " +
  "recipient. Remove some exclusions and try again.";
export const questionsAskedField = "Questions you asked";
export const questionsReceivedField = "Questions you received";

export const ruleRevealAt = "Reveal time";
export const ruleReminderLead = "Reminder before each";

export const buttonConfirm = "Confirm";
export const buttonBack = "Back";
export const buttonAnswer = "Answer";

export const titleRecipient = "Your recipient";
export const titleNewRecipient = "Your new recipient";
export const titleGiver = "Your giver";

// The texts of the direct messages: the exchange name and the server name come first.
export const dmDrawn = "The draw for %s (%s) is done.";
export const dmRedrawn = "A participant left %s (%s) and the draw was redone.";
export const dmChanged = "A participant left %s (%s). Your recipient changed.";
export const dmVoided = "The draw for %s (%s) was undone: too few participants or no valid draw. It is open again.";
export const dmDrawSoon = "Reminder: the draw for %s (%s) is %s.";
export const dmRevealSoon = "Reminder: %s (%s) is revealed %s.";
export const dmRevealed = "%s (%s) is revealed.";
export const dmCancelled = "%s (%s) was cancelled by the organizer.";
export const dmQuestion = "Anonymous question from your giver in %s (%s):";
export const dmAnswer = "Your recipient answered your question in %s (%s):";

// Source: internal/fakediscord/web/slash.js (the composer refuses an option over its maximum).
export const composerTooLong = "%s: must be %d characters or fewer.";

// Source: internal/giftexchange/parse.go (the reasons of an exchange name the module refuses).
export const reasonTooLong = "is too long";
export const reasonBidi = "contains bidirectional control characters";
export const reasonNoVisible = "has no visible character";

// Fills the formatting verbs of a Go template in order. A verb without a value, or a value
// without a verb, is a mistake in the spec and throws.
export function format(template: string, ...values: (string | number)[]): string {
  const verbs = template.match(/%[sd]/g) ?? [];
  if (verbs.length !== values.length) {
    throw new Error(`format: ${verbs.length} verbs but ${values.length} values in "${template}"`);
  }

  const queue = [...values];
  return template.replace(/%[sd]/g, () => String(queue.shift()));
}

// The text of a Go template before its first verb that has no value, for a message whose last
// verbs are a time that changes on every render.
export function formatPrefix(template: string, ...values: (string | number)[]): string {
  const verbs = template.match(/%[sd]/g) ?? [];
  if (values.length >= verbs.length) {
    throw new Error(`formatPrefix: "${template}" has no verb left after ${values.length} values`);
  }

  const queue = [...values];
  let seen = 0;
  const filled = template.replace(/%[sd]/g, (verb) => {
    seen += 1;
    return seen <= values.length ? String(queue.shift()) : verb;
  });
  return filled.slice(0, filled.search(/%[sd]/));
}
