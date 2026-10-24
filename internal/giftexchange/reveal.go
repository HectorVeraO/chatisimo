package giftexchange

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Reveal moves a Drawn exchange to Revealed and queues one Revealed notification per recipient of
// the final round, each naming only that recipient's own giver. Organizer only; Drawn only.
// Reveal returns no pair: each recipient reads only their own giver through MyGiver.
func (s *Service) Reveal(ctx context.Context, a Actor, g GroupID, id ExchangeID) error {
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

		return s.revealLocked(ctx, q, tx, exchange)
	})
	if txErr != nil {
		return txErr
	}

	s.logger.InfoContext(ctx, "exchange revealed", "exchange_id", id.String())

	return nil
}

// revealLocked reveals a Drawn exchange whose row the caller holds locked, whoever the caller is:
// the organizer's Reveal.
func (s *Service) revealLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
) error {
	isDrawn := exchange.State == string(StateDrawn)
	if !isDrawn {
		return &StateError{Op: "reveal", State: State(exchange.State)}
	}

	recipients, listErr := q.ListAssignmentRecipients(ctx, exchange.ID)
	if listErr != nil {
		return fmt.Errorf("list recipients of %s: %w", exchange.ID, listErr)
	}
	hasRecipients := len(recipients) > 0
	if !hasRecipients {
		panic("giftexchange: reveal: a Drawn exchange has no assignments")
	}

	markErr := q.MarkExchangeRevealed(ctx, db.MarkExchangeRevealedParams{
		ID:         exchange.ID,
		RevealedAt: timestamptz(s.now()),
	})
	if markErr != nil {
		return fmt.Errorf("mark exchange %s revealed: %w", exchange.ID, markErr)
	}

	closeErr := s.closeOpenQuestions(ctx, q, exchange.ID)
	if closeErr != nil {
		return closeErr
	}

	revealed := exchange
	revealed.State = string(StateRevealed)
	for _, recipient := range recipients {
		queueErr := s.queueNotification(ctx, q, tx, KindRevealed, revealed, recipient)
		if queueErr != nil {
			return fmt.Errorf("queue revealed notice of %s: %w", exchange.ID, queueErr)
		}
	}
	return nil
}
