// Package giftexchange runs secret gift exchanges ("amigo secreto"): who joins, the rules of
// each exchange, the draw, dropouts, anonymous questions, the reveal and the timed jobs around
// them. It knows nothing about Discord; clients reach it through *Service.
package giftexchange

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

// Provider names an identity or group provider.
type Provider string

// ProviderDiscord is the only provider in v1.
const ProviderDiscord Provider = "discord"

// ExternalIdentity is a login at a provider linked to a person, or the provider object a group
// stands for. (Provider, Subject) is unique per kind. Subject is opaque to the module.
type ExternalIdentity struct {
	Provider Provider
	Subject  string
}

// IdentityRef is how a client names a person it authenticated: its provider identity.
type IdentityRef struct {
	Provider Provider
	Subject  string
}

// GroupRef is how a client names the group an interaction came from (v1: the guild).
type GroupRef struct {
	Provider Provider
	Subject  string
}

// PersonRef is everything a client needs to address or mention a person, and nothing else.
// The module stores no display names; clients render a person from Identities.
type PersonRef struct {
	ID         PersonID
	Identities []ExternalIdentity
}

// Group is the circle exchanges belong to. It scopes "last round" and exchange visibility.
type Group struct {
	ID       GroupID
	Provider Provider
	Subject  string
}

// Actor is the authenticated caller of an operation. It is opaque: only ResolveActor builds
// one, so a client cannot claim a person by typing an ID. Its zero value is rejected by every
// operation with ErrUnauthenticated.
type Actor struct{ person PersonID }

// Person returns the actor's own person ID, so a client can compare it with PersonRef values.
func (a Actor) Person() PersonID {
	return a.person
}

// State is the lifecycle phase of an exchange. The string values are stable (stored, and used
// by clients in custom IDs).
type State string

// The lifecycle states of an exchange.
const (
	StateOpen      State = "open"
	StateDrawn     State = "drawn"
	StateRevealed  State = "revealed"
	StateCancelled State = "cancelled"
)

// DropoutPolicy decides what a dropout after the draw does to the assignments.
type DropoutPolicy string

// The dropout policies.
const (
	DropoutPatch  DropoutPolicy = "patch"
	DropoutRedraw DropoutPolicy = "redraw"
)

// Limits fixed by the design. They are also shown as fixed rules.
const (
	MinParticipants  = 3
	MaxParticipants  = 100
	RedrawBelow      = 4
	MaxNameRunes     = 100
	MaxQuestionRunes = 1000
	QuestionsPerDay  = 20
	MaxReminderLead  = 14 * 24 * time.Hour
)

// Participant is an active member of an exchange.
type Participant struct {
	Person   PersonRef
	JoinedAt time.Time
}

// Exclusion forbids Giver from drawing Recipient. It is directed; AddExclusion with mutual=true
// stores both directions. Only the organizer ever receives Exclusion values.
type Exclusion struct {
	Giver     PersonRef
	Recipient PersonRef
}

// ExchangeSummary is one row of an exchange listing, with the caller's relation to it.
type ExchangeSummary struct {
	ID       ExchangeID
	Group    Group
	Name     string
	State    State
	DrawAt   *time.Time
	RevealAt *time.Time
	// ParticipantCount counts active participants.
	ParticipantCount int
	// CallerIsOrganizer and CallerIsParticipant describe the Actor of the listing call.
	CallerIsOrganizer   bool
	CallerIsParticipant bool
}

// ExchangeView is the full view of one exchange as the caller may see it. It never contains a
// pair or an exclusion pair.
type ExchangeView struct {
	ExchangeSummary
	Organizer    PersonRef
	Participants []Participant
	// ExclusionCount is the number of stored directed exclusions; the pairs are organizer-only.
	ExclusionCount int
	Rules          []RuleView
	CreatedAt      time.Time
	DrawnAt        *time.Time
	RevealedAt     *time.Time
}

// AskedQuestion is a question as its asker (the giver) sees it. It names no recipient.
type AskedQuestion struct {
	ID         QuestionID
	Exchange   ExchangeID
	Text       string
	AskedAt    time.Time
	Answer     string
	AnsweredAt *time.Time
	ClosedAt   *time.Time
}

// ReceivedQuestion is a question as its recipient sees it. It has no asker field at all.
type ReceivedQuestion struct {
	ID         QuestionID
	Exchange   ExchangeID
	Text       string
	AskedAt    time.Time
	Answer     string
	AnsweredAt *time.Time
	ClosedAt   *time.Time
}

// DropoutOutcome tells the caller of Leave or RemoveParticipant what happened, without pairs.
type DropoutOutcome string

// The outcomes of a dropout.
const (
	// OutcomeRemoved: the exchange was Open; the person is no longer a participant.
	OutcomeRemoved DropoutOutcome = "removed"
	// OutcomePatched: the dropout's giver took over the dropout's recipient (patch policy).
	OutcomePatched DropoutOutcome = "patched"
	// OutcomeRedrawn: a full redraw among the remaining participants.
	OutcomeRedrawn DropoutOutcome = "redrawn"
	// OutcomeReopened: the round was voided and the exchange is Open again.
	OutcomeReopened DropoutOutcome = "reopened"
)

// Clock is the module's time source. Production passes a wrapper over time.Now; tests pass a
// fake that starts at time.Now() and only moves forward (River needs that).
type Clock interface {
	Now() time.Time
}

// Random drives the draw. It must be safe for concurrent use: interactive draws and the deadline
// worker run at the same time. *math/rand/v2.Rand over a concurrency-safe Source satisfies it.
// Production: rand.New(CryptoSource{}); tests: a mutex-guarded rand.New(rand.NewChaCha8(seed)).
type Random interface {
	IntN(n int) int
	Shuffle(n int, swap func(i, j int))
}

// CryptoSource is a math/rand/v2 Source that reads crypto/rand on every call. It holds no state,
// so it is safe for concurrent use. Uint64 panics if crypto/rand fails, which the standard
// library documents as unrecoverable.
type CryptoSource struct{}

// Uint64 returns 8 bytes from crypto/rand.
func (CryptoSource) Uint64() uint64 {
	var buf [8]byte
	_, err := rand.Read(buf[:])
	if err != nil {
		panic(fmt.Sprintf("giftexchange: crypto/rand failed: %v", err))
	}
	value := binary.LittleEndian.Uint64(buf[:])
	return value
}

// Notifier delivers module notifications to people. internal/discord implements it. Delivery is
// at least once: Notify may be called again with the same Notification.ID after a crash or a
// retry, so an implementation deduplicates on it where the client allows (Discord nonce).
// Returning an error that wraps ErrUndeliverable stops retries for that notification.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}
