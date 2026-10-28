package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Exclusions lists the stored directed exclusions. Organizer only; any state but Cancelled.
func (s *Service) Exclusions(ctx context.Context, a Actor, g GroupID, id ExchangeID) ([]Exclusion, error) {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return nil, validationErr
	}

	q := db.New(s.pool)
	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return nil, err
	}

	isOtherGroup := GroupID(row.exchange.GroupID) != g
	if isOtherGroup {
		return nil, ErrNotFound
	}
	guardErr := requireOrganizer(row.exchange, a)
	if guardErr != nil {
		return nil, guardErr
	}
	isCancelled := State(row.exchange.State) == StateCancelled
	if isCancelled {
		return nil, &StateError{Op: "exclusions", State: StateCancelled}
	}

	return listExclusions(ctx, q, row.exchange.ID)
}

func listExclusions(ctx context.Context, q *db.Queries, exchangeID uuid.UUID) ([]Exclusion, error) {
	stored, err := q.ListExclusions(ctx, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("list exclusions of %s: %w", exchangeID, err)
	}

	personIDs := make([]uuid.UUID, 0, 2*len(stored))
	for _, pair := range stored {
		personIDs = append(personIDs, pair.GiverID, pair.RecipientID)
	}
	identities, err := identitiesOf(ctx, q, personIDs)
	if err != nil {
		return nil, fmt.Errorf("identities of %s: %w", exchangeID, err)
	}

	exclusions := make([]Exclusion, 0, len(stored))
	for _, pair := range stored {
		exclusions = append(exclusions, Exclusion{
			Giver:     PersonRef{ID: PersonID(pair.GiverID), Identities: identities[pair.GiverID]},
			Recipient: PersonRef{ID: PersonID(pair.RecipientID), Identities: identities[pair.RecipientID]},
		})
	}
	return exclusions, nil
}

// AddExclusion forbids giver from drawing recipient, and the reverse too when mutual. Both must
// be active participants and distinct. Organizer only; Open only. Adding an existing exclusion
// is not an error and changes nothing.
func (s *Service) AddExclusion(
	ctx context.Context,
	a Actor,
	g GroupID,
	id ExchangeID,
	giver, recipient PersonID,
	mutual bool,
) error {
	validationErr := validatePair(a, g, id, giver, recipient)
	if validationErr != nil {
		return validationErr
	}

	isSelfPair := giver == recipient
	if isSelfPair {
		return &InputError{Field: "recipient", Code: InputCodeSameAsGiver}
	}

	return s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		exchange, err := lockOpenForExclusions(ctx, q, a, g, id, "add_exclusion")
		if err != nil {
			return err
		}

		for _, person := range []PersonID{giver, recipient} {
			isActive, activeErr := isActiveParticipant(ctx, q, exchange, person)
			if activeErr != nil {
				return activeErr
			}
			if !isActive {
				return ErrNotParticipant
			}
		}

		insertErr := s.insertExclusion(ctx, q, exchange, giver, recipient)
		if insertErr != nil {
			return insertErr
		}
		if mutual {
			return s.insertExclusion(ctx, q, exchange, recipient, giver)
		}
		return nil
	})
}

func (s *Service) insertExclusion(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	giver, recipient PersonID,
) error {
	err := q.InsertExclusion(ctx, db.InsertExclusionParams{
		ExchangeID:  exchange.ID,
		GiverID:     uuid.UUID(giver),
		RecipientID: uuid.UUID(recipient),
		CreatedAt:   timestamptz(s.now()),
	})
	if err != nil {
		return fmt.Errorf("add exclusion to %s: %w", exchange.ID, err)
	}
	return nil
}

// RemoveExclusion deletes the directed exclusion giver -> recipient (only that direction).
// Organizer only; Open only. Removing a missing exclusion is not an error.
func (s *Service) RemoveExclusion(
	ctx context.Context,
	a Actor,
	g GroupID,
	id ExchangeID,
	giver, recipient PersonID,
) error {
	validationErr := validatePair(a, g, id, giver, recipient)
	if validationErr != nil {
		return validationErr
	}

	return s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		exchange, err := lockOpenForExclusions(ctx, q, a, g, id, "remove_exclusion")
		if err != nil {
			return err
		}

		deleteErr := q.DeleteExclusion(ctx, db.DeleteExclusionParams{
			ExchangeID:  exchange.ID,
			GiverID:     uuid.UUID(giver),
			RecipientID: uuid.UUID(recipient),
		})
		if deleteErr != nil {
			return fmt.Errorf("remove exclusion from %s: %w", exchange.ID, deleteErr)
		}
		return nil
	})
}

// validatePair checks the actor and every ID an exclusion call takes, reporting all at once.
func validatePair(a Actor, g GroupID, id ExchangeID, giver, recipient PersonID) error {
	scopeErr := validateScope(a, g, id)
	if errors.Is(scopeErr, ErrUnauthenticated) {
		return scopeErr
	}

	giverErr := requireID("giver_id", giver.IsZero())
	recipientErr := requireID("recipient_id", recipient.IsZero())
	joined := errors.Join(scopeErr, giverErr, recipientErr)
	if joined != nil {
		return joined //nolint:wrapcheck // joins this function's own validation errors
	}
	return nil
}

// lockOpenForExclusions locks the exchange and lets only its organizer through while it is Open.
func lockOpenForExclusions(
	ctx context.Context,
	q *db.Queries,
	a Actor,
	g GroupID,
	id ExchangeID,
	op string,
) (db.GiftexchangeExchange, error) {
	exchange, err := lockExchange(ctx, q, g, id)
	if err != nil {
		return db.GiftexchangeExchange{}, err
	}

	guardErr := requireOrganizer(exchange, a)
	if guardErr != nil {
		return db.GiftexchangeExchange{}, guardErr
	}

	isOpen := exchange.State == string(StateOpen)
	if !isOpen {
		return db.GiftexchangeExchange{}, &StateError{Op: op, State: State(exchange.State)}
	}
	return exchange, nil
}
