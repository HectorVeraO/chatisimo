package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Join adds the caller as a participant. Open only; ErrAlreadyParticipant if active already;
// ErrExchangeFull at MaxParticipants. A person who left an Open exchange may join again.
func (s *Service) Join(ctx context.Context, a Actor, g GroupID, id ExchangeID) error {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return validationErr
	}

	return s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		exchange, err := lockExchange(ctx, q, g, id)
		if err != nil {
			return err
		}

		isOpen := exchange.State == string(StateOpen)
		if !isOpen {
			return &StateError{Op: "join", State: State(exchange.State)}
		}

		return s.addParticipant(ctx, q, exchange, a.person)
	})
}

func (s *Service) addParticipant(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	person PersonID,
) error {
	existing, err := q.GetParticipant(ctx, db.GetParticipantParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(person),
	})
	hasRow := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get participant of %s: %w", exchange.ID, err)
	}

	isActive := hasRow && !existing.LeftAt.Valid
	if isActive {
		return ErrAlreadyParticipant
	}

	active, countErr := q.CountActiveParticipants(ctx, exchange.ID)
	if countErr != nil {
		return fmt.Errorf("count participants of %s: %w", exchange.ID, countErr)
	}
	isFull := active >= MaxParticipants
	if isFull {
		return ErrExchangeFull
	}

	joinedAt := timestamptz(s.now())
	if hasRow {
		reviveErr := q.ReviveParticipant(ctx, db.ReviveParticipantParams{
			ExchangeID: exchange.ID,
			PersonID:   uuid.UUID(person),
			JoinedAt:   joinedAt,
		})
		if reviveErr != nil {
			return fmt.Errorf("rejoin exchange %s: %w", exchange.ID, reviveErr)
		}
		return nil
	}

	insertErr := q.InsertParticipant(ctx, db.InsertParticipantParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(person),
		JoinedAt:   joinedAt,
	})
	if insertErr != nil {
		return fmt.Errorf("join exchange %s: %w", exchange.ID, insertErr)
	}
	return nil
}

// Leave removes the caller and deletes their exclusions. In Open it returns OutcomeRemoved; in
// Drawn it repairs the assignments (patch, full redraw, or void back to Open) and returns that
// outcome, never a pair. In any other state it returns a StateError.
func (s *Service) Leave(ctx context.Context, a Actor, g GroupID, id ExchangeID) (DropoutOutcome, error) {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return "", validationErr
	}

	var outcome DropoutOutcome
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, err := lockExchange(ctx, q, g, id)
		if err != nil {
			return err
		}

		isActive, activeErr := isActiveParticipant(ctx, q, exchange, a.person)
		if activeErr != nil {
			return activeErr
		}
		if !isActive {
			return ErrNotParticipant
		}

		var removeErr error
		outcome, removeErr = s.removeLocked(ctx, q, tx, "leave", exchange, a.person)
		return removeErr
	})
	if txErr != nil {
		return "", txErr
	}

	return outcome, nil
}

// RemoveParticipant removes person p on the organizer's behalf, like Leave for p, exclusions
// included. Open and Drawn accept it, with the outcomes of Leave. In Drawn an organizer who is
// also a participant cannot remove their own current recipient (ErrForbidden, nothing changes).
func (s *Service) RemoveParticipant(
	ctx context.Context,
	a Actor,
	g GroupID,
	id ExchangeID,
	p PersonID,
) (DropoutOutcome, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return "", authErr
	}

	scopeErr := validateScope(a, g, id)
	personErr := requireID("person_id", p.IsZero())
	validationErr := errors.Join(scopeErr, personErr)
	if validationErr != nil {
		return "", validationErr //nolint:wrapcheck // joins this function's own validation errors
	}

	var outcome DropoutOutcome
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, err := lockExchange(ctx, q, g, id)
		if err != nil {
			return err
		}

		guardErr := requireOrganizer(exchange, a)
		if guardErr != nil {
			return guardErr
		}

		stateErr := requireDropoutState("remove_participant", exchange)
		if stateErr != nil {
			return stateErr
		}

		isActive, activeErr := isActiveParticipant(ctx, q, exchange, p)
		if activeErr != nil {
			return activeErr
		}
		if !isActive {
			return ErrNotParticipant
		}

		ownErr := requireNotOwnRecipient(ctx, q, exchange, a.person, p)
		if ownErr != nil {
			return ownErr
		}

		var removeErr error
		outcome, removeErr = s.removeLocked(ctx, q, tx, "remove_participant", exchange, p)
		return removeErr
	})
	if txErr != nil {
		return "", txErr
	}

	return outcome, nil
}

func isActiveParticipant(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	person PersonID,
) (bool, error) {
	row, err := q.GetParticipant(ctx, db.GetParticipantParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(person),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get participant of %s: %w", exchange.ID, err)
	}
	return !row.LeftAt.Valid, nil
}

// requireDropoutState allows dropouts in Open and Drawn; Revealed and Cancelled are final.
func requireDropoutState(op string, exchange db.GiftexchangeExchange) error {
	isOpen := exchange.State == string(StateOpen)
	isDrawn := exchange.State == string(StateDrawn)
	if !isOpen && !isDrawn {
		return &StateError{Op: op, State: State(exchange.State)}
	}
	return nil
}

// removeLocked marks person as left in an Open or Drawn exchange whose row the caller holds
// locked and deletes their exclusions. In Drawn it then repairs the assignments.
func (s *Service) removeLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	op string,
	exchange db.GiftexchangeExchange,
	person PersonID,
) (DropoutOutcome, error) {
	stateErr := requireDropoutState(op, exchange)
	if stateErr != nil {
		return "", stateErr
	}

	exclusionsErr := q.DeleteExclusionsOfPerson(ctx, db.DeleteExclusionsOfPersonParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(person),
	})
	if exclusionsErr != nil {
		return "", fmt.Errorf("delete exclusions of %s: %w", exchange.ID, exclusionsErr)
	}

	leftErr := q.MarkParticipantLeft(ctx, db.MarkParticipantLeftParams{
		ExchangeID: exchange.ID,
		PersonID:   uuid.UUID(person),
		LeftAt:     timestamptz(s.now()),
	})
	if leftErr != nil {
		return "", fmt.Errorf("remove participant of %s: %w", exchange.ID, leftErr)
	}

	isOpen := exchange.State == string(StateOpen)
	if isOpen {
		return OutcomeRemoved, nil
	}
	return s.repairDrawn(ctx, q, tx, exchange, uuid.UUID(person))
}
