package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// MyAssignment returns the caller's own recipient. Active participant; Drawn or Revealed. It is
// the only way a pair leaves the module on request. A person with no tie to the exchange gets
// ErrNotFound; an organizer who never joined, or a participant who left, gets ErrNotParticipant.
func (s *Service) MyAssignment(ctx context.Context, a Actor, id ExchangeID) (Assignment, error) {
	q := db.New(s.pool)
	exchange, err := activeParticipantExchange(ctx, q, a, id)
	if err != nil {
		return Assignment{}, err
	}

	isDrawnOrRevealed := exchange.State == string(StateDrawn) || exchange.State == string(StateRevealed)
	if !isDrawnOrRevealed {
		return Assignment{}, &StateError{Op: "my_assignment", State: State(exchange.State)}
	}

	recipient, lookupErr := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    uuid.UUID(a.person),
	})
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return Assignment{}, ErrNotParticipant
	}
	if lookupErr != nil {
		return Assignment{}, fmt.Errorf("get assignment of %s: %w", exchange.ID, lookupErr)
	}

	ref, refErr := personRef(ctx, q, recipient)
	if refErr != nil {
		return Assignment{}, refErr
	}

	return Assignment{Exchange: id, Recipient: ref}, nil
}

// MyGiver returns who gave to the caller. Revealed only; the caller must be a recipient of the
// final round. It returns the caller's own giver only. A person with no tie to the exchange gets
// ErrNotFound; an organizer who never joined, or a participant who left, gets ErrNotParticipant.
func (s *Service) MyGiver(ctx context.Context, a Actor, id ExchangeID) (RevealedGiver, error) {
	q := db.New(s.pool)
	exchange, err := activeParticipantExchange(ctx, q, a, id)
	if err != nil {
		return RevealedGiver{}, err
	}

	isRevealed := exchange.State == string(StateRevealed)
	if !isRevealed {
		return RevealedGiver{}, &StateError{Op: "my_giver", State: State(exchange.State)}
	}

	giver, lookupErr := q.GetAssignmentOfRecipient(ctx, db.GetAssignmentOfRecipientParams{
		ExchangeID:  exchange.ID,
		RecipientID: uuid.UUID(a.person),
	})
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return RevealedGiver{}, ErrNotParticipant
	}
	if lookupErr != nil {
		return RevealedGiver{}, fmt.Errorf("get giver of %s: %w", exchange.ID, lookupErr)
	}

	ref, refErr := personRef(ctx, q, giver)
	if refErr != nil {
		return RevealedGiver{}, refErr
	}

	return RevealedGiver{Exchange: id, Giver: ref}, nil
}

// activeParticipantExchange loads the exchange for a group-less read of the caller's own pair.
// The caller is related to it when they organize it or hold any participants row, active or
// left; anyone else gets ErrNotFound, as for an unknown ID. A related caller who is not an active
// participant gets ErrNotParticipant.
func activeParticipantExchange(
	ctx context.Context,
	q *db.Queries,
	a Actor,
	id ExchangeID,
) (db.GiftexchangeExchange, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return db.GiftexchangeExchange{}, authErr
	}
	idErr := requireID("exchange_id", id.IsZero())
	if idErr != nil {
		return db.GiftexchangeExchange{}, idErr
	}

	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return db.GiftexchangeExchange{}, err
	}
	exchange := row.exchange

	isRelated, isActive, relationErr := callerRelation(ctx, q, a, exchange)
	if relationErr != nil {
		return db.GiftexchangeExchange{}, relationErr
	}
	if !isRelated {
		return db.GiftexchangeExchange{}, ErrNotFound
	}
	if !isActive {
		return db.GiftexchangeExchange{}, ErrNotParticipant
	}

	return exchange, nil
}

// personRef returns the person with their identities.
func personRef(ctx context.Context, q *db.Queries, person uuid.UUID) (PersonRef, error) {
	identities, err := identitiesOf(ctx, q, []uuid.UUID{person})
	if err != nil {
		return PersonRef{}, fmt.Errorf("read identities: %w", err)
	}

	ref := PersonRef{ID: PersonID(person), Identities: identities[person]}
	return ref, nil
}

// callerRelation reports whether the caller is related to the exchange (its organizer, or holding
// any participants row, active or left) and whether they are an active participant.
func callerRelation(
	ctx context.Context,
	q *db.Queries,
	a Actor,
	exchange db.GiftexchangeExchange,
) (bool, bool, error) {
	participant, err := q.GetParticipant(ctx, db.GetParticipantParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(a.person),
	})
	hasRow := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("get participant of %s: %w", exchange.ID, err)
	}

	isOrganizer := PersonID(exchange.OrganizerID) == a.person
	isRelated := hasRow || isOrganizer
	isActive := hasRow && !participant.LeftAt.Valid
	return isRelated, isActive, nil
}
