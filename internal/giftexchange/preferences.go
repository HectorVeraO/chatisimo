package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Preferences holds a participant's gift guidance for their assigned giver. Because preferences
// identify a participant's tastes and are shared with their secret giver, String, GoString, Format
// and LogValue render "[redacted]".
type Preferences struct {
	Likes    string
	Dislikes string
	Details  string
}

// IsEmpty reports whether all preference fields are empty.
func (p Preferences) IsEmpty() bool {
	return p.Likes == "" && p.Dislikes == "" && p.Details == ""
}

// String returns "[redacted]".
func (Preferences) String() string {
	return redacted
}

// GoString returns "[redacted]".
func (Preferences) GoString() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (Preferences) Format(f fmt.State, _ rune) {
	writeText(f, redacted)
}

// LogValue returns "[redacted]" so slog never records preference text.
func (Preferences) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// ParsePreferences validates and normalizes a participant's gift preferences. Empty fields are
// valid; each non-empty field may be at most MaxPreferenceRunes runes and may contain newlines.
func ParsePreferences(raw Preferences) (Preferences, error) {
	likes, likesErr := parsePreferenceField("likes", raw.Likes)
	dislikes, dislikesErr := parsePreferenceField("dislikes", raw.Dislikes)
	details, detailsErr := parsePreferenceField("details", raw.Details)
	err := errors.Join(likesErr, dislikesErr, detailsErr)
	if err != nil {
		return Preferences{}, err //nolint:wrapcheck // joins this function's own validation errors
	}

	parsed := Preferences{
		Likes:    likes,
		Dislikes: dislikes,
		Details:  details,
	}
	return parsed, nil
}

func parsePreferenceField(field string, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return parseText(field, raw, MaxPreferenceRunes, true)
}

// Preferences returns the caller's own stored gift preferences in exchange id. Active participant
// only; a person with no tie to the exchange gets ErrNotFound, and an organizer who never joined
// or a participant who left gets ErrNotParticipant.
func (s *Service) Preferences(ctx context.Context, a Actor, id ExchangeID) (Preferences, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return Preferences{}, authErr
	}
	idErr := requireID("exchange_id", id.IsZero())
	if idErr != nil {
		return Preferences{}, idErr
	}

	q := db.New(s.pool)
	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return Preferences{}, err
	}

	participant, isRelated, isActive, relationErr := callerParticipant(ctx, q, a, row.exchange)
	if relationErr != nil {
		return Preferences{}, relationErr
	}
	if !isRelated {
		return Preferences{}, ErrNotFound
	}
	if !isActive {
		return Preferences{}, ErrNotParticipant
	}

	prefs := preferencesOf(participant)
	return prefs, nil
}

// SetPreferences sets or clears the caller's own gift preferences in exchange id. Active
// participant; Open or Drawn. When the exchange is Drawn and the normalized preferences differ
// from the previously stored preferences, SetPreferences queues a KindPreferencesUpdated
// notification to the caller's current giver.
func (s *Service) SetPreferences(
	ctx context.Context,
	a Actor,
	id ExchangeID,
	prefs Preferences,
) (Preferences, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return Preferences{}, authErr
	}

	idErr := requireID("exchange_id", id.IsZero())
	parsed, prefsErr := ParsePreferences(prefs)
	validationErr := errors.Join(idErr, prefsErr)
	if validationErr != nil {
		return Preferences{}, validationErr //nolint:wrapcheck // joins this function's own validation errors
	}

	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		return s.setPreferencesLocked(ctx, q, tx, a, id, parsed)
	})
	if txErr != nil {
		return Preferences{}, txErr
	}

	s.logger.InfoContext(ctx, "preferences updated", "exchange_id", id.String())

	return parsed, nil
}

func (s *Service) setPreferencesLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	a Actor,
	id ExchangeID,
	parsed Preferences,
) error {
	exchange, err := lockExchangeByID(ctx, q, id)
	if err != nil {
		return err
	}

	participant, isRelated, isActive, relationErr := callerParticipant(ctx, q, a, exchange)
	if relationErr != nil {
		return relationErr
	}
	if !isRelated {
		return ErrNotFound
	}
	if !isActive {
		return ErrNotParticipant
	}

	isEditable := exchange.State == string(StateOpen) || exchange.State == string(StateDrawn)
	if !isEditable {
		return &StateError{Op: "set_preferences", State: State(exchange.State)}
	}

	previous := preferencesOf(participant)
	if previous == parsed {
		return nil
	}

	updateErr := q.UpdateParticipantPreferences(ctx, db.UpdateParticipantPreferencesParams{
		Likes:      parsed.Likes,
		Dislikes:   parsed.Dislikes,
		Details:    parsed.Details,
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(a.person),
	})
	if updateErr != nil {
		return fmt.Errorf("update preferences in %s: %w", exchange.ID, updateErr)
	}

	isDrawn := exchange.State == string(StateDrawn)
	if !isDrawn {
		return nil
	}

	giver, lookupErr := q.GetAssignmentOfRecipient(ctx, db.GetAssignmentOfRecipientParams{
		ExchangeID:  exchange.ID,
		RecipientID: uuid.UUID(a.person),
	})
	if lookupErr != nil {
		return fmt.Errorf("get giver in %s: %w", exchange.ID, lookupErr)
	}

	return s.queueNotification(ctx, q, tx, KindPreferencesUpdated, exchange, giver)
}

func preferencesOf(participant db.GiftexchangeParticipant) Preferences {
	return Preferences{
		Likes:    participant.Likes,
		Dislikes: participant.Dislikes,
		Details:  participant.Details,
	}
}
