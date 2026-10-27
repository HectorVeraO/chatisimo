package giftexchange

import (
	"fmt"
	"io"
	"log/slog"
)

// NotificationKind names what a notification tells its addressee. Values are stable (stored).
type NotificationKind string

// The notification kinds.
const (
	// KindDrawn: to each giver after a draw; Counterpart is the giver's recipient.
	KindDrawn NotificationKind = "drawn"
	// KindRedrawn: to each giver after a dropout redraw; Counterpart is the new recipient.
	KindRedrawn NotificationKind = "redrawn"
	// KindAssignmentChanged: to the one giver whose recipient a patch changed; Counterpart is the
	// new recipient.
	KindAssignmentChanged NotificationKind = "assignment_changed"
	// KindRoundVoided: to the organizer and every giver of a voided round. Reason set.
	KindRoundVoided NotificationKind = "round_voided"
	// KindDrawFailed: to the organizer when the deadline draw could not run. Reason set.
	KindDrawFailed NotificationKind = "draw_failed"
	// KindDrawSoon: to the organizer and each active participant, reminder_lead before draw_at.
	KindDrawSoon NotificationKind = "draw_soon"
	// KindRevealSoon: to each giver, reminder_lead before reveal_at; Counterpart is the giver's
	// recipient.
	KindRevealSoon NotificationKind = "reveal_soon"
	// KindRevealed: to each recipient at the reveal; Counterpart is the recipient's giver.
	KindRevealed NotificationKind = "revealed"
	// KindCancelled: to each active participant other than the organizer.
	KindCancelled NotificationKind = "cancelled"
	// KindQuestionReceived: to the recipient of a question. Question set, no asker.
	KindQuestionReceived NotificationKind = "question_received"
	// KindAnswerReceived: to the asker of an answered question. Question set, with the answer.
	KindAnswerReceived NotificationKind = "answer_received"
	// KindPreferencesUpdated: to a giver when their recipient updates or clears preferences;
	// Counterpart is the recipient, Preferences is the new preferences.
	KindPreferencesUpdated NotificationKind = "preferences_updated"
)

// Reason explains a voided round or a failed deadline draw. It never names a person or pair.
type Reason string

// The reasons a round is voided or a deadline draw fails.
const (
	ReasonTooFewParticipants Reason = "too_few_participants"
	ReasonNoValidAssignment  Reason = "no_valid_assignment"
)

// NotifiedQuestion is the question content a question notice carries. It has no asker.
type NotifiedQuestion struct {
	ID     QuestionID
	Text   string
	Answer string
}

// Notification is one message to one person, built from IDs at delivery time.
// Counterpart is set only for KindDrawn, KindRedrawn, KindAssignmentChanged, KindRevealSoon and
// KindPreferencesUpdated (the addressee's own recipient) and KindRevealed (the addressee's own
// giver); nil otherwise. Preferences is populated for KindDrawn, KindRedrawn,
// KindAssignmentChanged and KindPreferencesUpdated. Question is set only for KindQuestionReceived
// and KindAnswerReceived. Reason only for KindRoundVoided and KindDrawFailed. It holds a pair, so
// it formats and logs as "notification <id> <kind>".
type Notification struct {
	ID          NotificationID
	Kind        NotificationKind
	To          PersonRef
	Exchange    ExchangeSummary
	Counterpart *PersonRef
	Preferences Preferences
	Question    *NotifiedQuestion
	Reason      Reason
}

// String returns "notification <id> <kind>", with no person.
func (n Notification) String() string {
	text := "notification " + n.ID.String() + " " + string(n.Kind)
	return text
}

// Format writes the String form for every verb.
func (n Notification) Format(f fmt.State, _ rune) {
	writeText(f, n.String())
}

// LogValue returns the String form so slog never records a pair or a question.
func (n Notification) LogValue() slog.Value {
	return slog.StringValue(n.String())
}

// redacted is what a type holding a giver-recipient pair renders in place of its content.
const redacted = "[redacted]"

// Assignment is the caller's own recipient in the current round. It holds a pair (the caller and
// Recipient) and the recipient's gift preferences, so String, Format (every verb, %v %+v %#v
// included) and LogValue render "[redacted]".
type Assignment struct {
	Exchange    ExchangeID
	Recipient   PersonRef
	Preferences Preferences
}

// String returns "[redacted]".
func (Assignment) String() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (Assignment) Format(f fmt.State, _ rune) {
	writeText(f, redacted)
}

// LogValue returns "[redacted]" so slog never records the pair.
func (Assignment) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// RevealedGiver is the caller's own giver after the reveal. Redacted like Assignment.
type RevealedGiver struct {
	Exchange ExchangeID
	Giver    PersonRef
}

// String returns "[redacted]".
func (RevealedGiver) String() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (RevealedGiver) Format(f fmt.State, _ rune) {
	writeText(f, redacted)
}

// LogValue returns "[redacted]" so slog never records the pair.
func (RevealedGiver) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

func writeText(w io.Writer, text string) {
	// A failed write to a fmt.State has nowhere to be reported and the verb is already over.
	_, _ = io.WriteString(w, text)
}
