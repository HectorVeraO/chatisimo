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
