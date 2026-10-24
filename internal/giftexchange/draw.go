package giftexchange

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/draw"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Draw assigns every active participant a recipient, moves the exchange to Drawn and queues one
// Drawn notification per giver. Organizer only; Open only. Fewer than MinParticipants active
// participants is ErrTooFewParticipants; exclusions that leave no assignment are an
// *InfeasibleError. Either way nothing changes. Draw returns no pair: each giver reads only their
// own through MyAssignment.
func (s *Service) Draw(ctx context.Context, a Actor, g GroupID, id ExchangeID) error {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return validationErr
	}

	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, err := lockExchange(ctx, q, g, id)
		if err != nil {
			return err
		}

		guardErr := requireOrganizer(exchange, a)
		if guardErr != nil {
			return guardErr
		}

		isOpen := exchange.State == string(StateOpen)
		if !isOpen {
			return &StateError{Op: "draw", State: State(exchange.State)}
		}

		return s.drawLocked(ctx, q, tx, exchange)
	})
	if txErr != nil {
		return txErr
	}

	s.logger.InfoContext(ctx, "exchange drawn", "exchange_id", id.String())

	return nil
}

// drawLocked draws an Open exchange whose row the caller holds locked.
func (s *Service) drawLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
) error {
	givers, err := sortedActiveParticipants(ctx, q, exchange.ID)
	if err != nil {
		return err
	}

	isTooFew := len(givers) < MinParticipants
	if isTooFew {
		return ErrTooFewParticipants
	}
	isTooMany := len(givers) > MaxParticipants
	if isTooMany {
		return fmt.Errorf("exchange %s has more than %d participants", exchange.ID, MaxParticipants)
	}

	forbidden, err := forbiddenPairs(ctx, q, exchange, givers)
	if err != nil {
		return err
	}

	perm, assignErr := draw.Assign(len(givers), forbidden, s.random)
	var infeasible *draw.InfeasibleError
	if errors.As(assignErr, &infeasible) {
		return &InfeasibleError{Stranded: infeasible.Stranded}
	}
	if assignErr != nil {
		return fmt.Errorf("assign exchange %s: %w", exchange.ID, assignErr)
	}

	drawn := exchange
	drawn.State = string(StateDrawn)
	drawn.Round = exchange.Round + 1

	writeErr := s.writeAssignments(ctx, q, drawn, givers, perm)
	if writeErr != nil {
		return writeErr
	}

	for _, giver := range givers {
		queueErr := s.queueNotification(ctx, q, tx, KindDrawn, drawn, giver)
		if queueErr != nil {
			return queueErr
		}
	}
	return nil
}

// sortedActiveParticipants returns the active participants ordered by the bytes of their person
// ID, so a seeded Random fixes the draw whatever order the database returns rows in.
func sortedActiveParticipants(ctx context.Context, q *db.Queries, exchangeID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.ListActiveParticipants(ctx, exchangeID)
	if err != nil {
		return nil, fmt.Errorf("list participants of %s: %w", exchangeID, err)
	}

	people := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		people = append(people, row.PersonID)
	}
	slices.SortFunc(people, func(x, y uuid.UUID) int {
		return bytes.Compare(x[:], y[:])
	})

	return people, nil
}

// forbiddenPairs returns the stored exclusions as index pairs into givers. A pair that names a
// person outside givers is dropped.
func forbiddenPairs(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	givers []uuid.UUID,
) ([]draw.Pair, error) {
	index := make(map[uuid.UUID]int, len(givers))
	for position, person := range givers {
		index[person] = position
	}

	var pairs []draw.Pair
	keep := func(giver, recipient uuid.UUID) {
		giverIndex, hasGiver := index[giver]
		recipientIndex, hasRecipient := index[recipient]
		if hasGiver && hasRecipient {
			pairs = append(pairs, draw.Pair{Giver: giverIndex, Recipient: recipientIndex})
		}
	}

	exclusions, err := q.ListExclusions(ctx, exchange.ID)
	if err != nil {
		return nil, fmt.Errorf("list exclusions of %s: %w", exchange.ID, err)
	}
	for _, exclusion := range exclusions {
		keep(exclusion.GiverID, exclusion.RecipientID)
	}

	return pairs, nil
}

// writeAssignments stores the permutation as the exchange's new round and marks it Drawn.
func (s *Service) writeAssignments(
	ctx context.Context,
	q *db.Queries,
	drawn db.GiftexchangeExchange,
	givers []uuid.UUID,
	perm []int,
) error {
	rows := make([]db.InsertAssignmentsParams, 0, len(givers))
	for position, giver := range givers {
		rows = append(rows, db.InsertAssignmentsParams{
			ExchangeID:  drawn.ID,
			GiverID:     giver,
			RecipientID: givers[perm[position]],
			Round:       drawn.Round,
		})
	}

	inserted, insertErr := q.InsertAssignments(ctx, rows)
	if insertErr != nil {
		return fmt.Errorf("insert assignments of %s: %w", drawn.ID, insertErr)
	}
	hasAllRows := inserted == int64(len(rows))
	if !hasAllRows {
		return fmt.Errorf("insert assignments of %s: stored %d of %d rows", drawn.ID, inserted, len(rows))
	}

	markErr := q.MarkExchangeDrawn(ctx, db.MarkExchangeDrawnParams{
		ID:      drawn.ID,
		Round:   drawn.Round,
		DrawnAt: timestamptz(s.now()),
	})
	if markErr != nil {
		return fmt.Errorf("mark exchange %s drawn: %w", drawn.ID, markErr)
	}
	return nil
}
