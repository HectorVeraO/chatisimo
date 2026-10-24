package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// requireNotOwnRecipient refuses an organizer who is also an active participant when they try to
// remove their own current recipient in a Drawn exchange: repeating that would let them walk the
// cycle of assignments. Nothing is read or changed beyond the organizer's own pair.
func requireNotOwnRecipient(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	organizer PersonID,
	target PersonID,
) error {
	isDrawn := exchange.State == string(StateDrawn)
	if !isDrawn {
		return nil
	}

	isActive, activeErr := isActiveParticipant(ctx, q, exchange, organizer)
	if activeErr != nil {
		return activeErr
	}
	if !isActive {
		return nil
	}

	recipient, err := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    uuid.UUID(organizer),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get assignment of %s: %w", exchange.ID, err)
	}

	isOwnRecipient := recipient == uuid.UUID(target)
	if isOwnRecipient {
		return ErrForbidden
	}
	return nil
}

// repairDrawn repairs the assignments of a Drawn exchange after dropout left it, as the dropout
// table says: void below MinParticipants, otherwise a full redraw or a patch. dropout's left_at
// is already set and their exclusions deleted, so they do not constrain a redraw.
func (s *Service) repairDrawn(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	dropout uuid.UUID,
) (DropoutOutcome, error) {
	remaining, err := sortedActiveParticipants(ctx, q, exchange.ID)
	if err != nil {
		return "", err
	}

	isTooFew := len(remaining) < MinParticipants
	if isTooFew {
		return s.voidRound(ctx, q, tx, exchange, dropout, ReasonTooFewParticipants)
	}

	giver, recipient, err := neighboursOf(ctx, q, exchange, dropout)
	if err != nil {
		return "", err
	}

	mustRedraw, err := patchIsUnsafe(ctx, q, exchange, len(remaining), giver, recipient)
	if err != nil {
		return "", err
	}
	if mustRedraw {
		return s.redrawRound(ctx, q, tx, exchange, dropout)
	}

	return s.patchRound(ctx, q, tx, exchange, dropout, giver, recipient)
}

// neighboursOf returns the giver of dropout and the recipient of dropout in the current round.
func neighboursOf(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	dropout uuid.UUID,
) (uuid.UUID, uuid.UUID, error) {
	giver, giverErr := q.GetAssignmentOfRecipient(ctx, db.GetAssignmentOfRecipientParams{
		ExchangeID:  exchange.ID,
		RecipientID: dropout,
	})
	if giverErr != nil {
		return uuid.UUID{}, uuid.UUID{}, fmt.Errorf("get giver of dropout in %s: %w", exchange.ID, giverErr)
	}

	recipient, recipientErr := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    dropout,
	})
	if recipientErr != nil {
		return uuid.UUID{}, uuid.UUID{}, fmt.Errorf("get recipient of dropout in %s: %w", exchange.ID, recipientErr)
	}

	return giver, recipient, nil
}

// patchIsUnsafe reports whether the dropout needs a full redraw instead of a patch: the policy
// asks for one, fewer than 4 remain, the giver and recipient of the dropout are the same person,
// or the pair the patch would create is forbidden.
func patchIsUnsafe(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	remaining int,
	giver uuid.UUID,
	recipient uuid.UUID,
) (bool, error) {
	wantsRedraw := exchange.DropoutPolicy == string(DropoutRedraw)
	isSmall := remaining < RedrawBelow
	isSelfPair := giver == recipient
	if wantsRedraw || isSmall || isSelfPair {
		return true, nil
	}

	forbidden, err := forbiddenPersonPairs(ctx, q, exchange)
	if err != nil {
		return false, err
	}
	for _, pair := range forbidden {
		isPatchedPair := pair.giver == giver && pair.recipient == recipient
		if isPatchedPair {
			return true, nil
		}
	}
	return false, nil
}

// redrawRound replaces the assignments with a new round over the active participants and tells
// every giver. A redraw that finds no valid assignment voids the round instead.
func (s *Service) redrawRound(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	dropout uuid.UUID,
) (DropoutOutcome, error) {
	deleteErr := q.DeleteAssignments(ctx, exchange.ID)
	if deleteErr != nil {
		return "", fmt.Errorf("delete assignments of %s: %w", exchange.ID, deleteErr)
	}

	drawErr := s.drawRound(ctx, q, tx, exchange, KindRedrawn)
	if errors.Is(drawErr, ErrNoValidAssignment) {
		return s.voidRound(ctx, q, tx, exchange, dropout, ReasonNoValidAssignment)
	}
	if drawErr != nil {
		return "", drawErr
	}
	return OutcomeRedrawn, nil
}

// patchRound gives the dropout's giver the dropout's recipient and tells that giver only. The
// recipient's row goes first because assignments_recipient_unique is not deferrable.
func (s *Service) patchRound(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	dropout uuid.UUID,
	giver uuid.UUID,
	recipient uuid.UUID,
) (DropoutOutcome, error) {
	deleteErr := q.DeleteAssignmentOfGiver(ctx, db.DeleteAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    dropout,
	})
	if deleteErr != nil {
		return "", fmt.Errorf("delete assignment of %s: %w", exchange.ID, deleteErr)
	}

	updateErr := q.UpdateAssignmentRecipient(ctx, db.UpdateAssignmentRecipientParams{
		ExchangeID:  exchange.ID,
		GiverID:     giver,
		RecipientID: recipient,
	})
	if updateErr != nil {
		return "", fmt.Errorf("patch assignment of %s: %w", exchange.ID, updateErr)
	}

	queueErr := s.queueNotification(ctx, q, tx, KindAssignmentChanged, exchange, giver)
	if queueErr != nil {
		return "", queueErr
	}
	return OutcomePatched, nil
}

// voidRound deletes the assignments, reopens the exchange (round and reveal_at stay) and tells the
// organizer and every remaining giver once, never the dropout.
func (s *Service) voidRound(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	dropout uuid.UUID,
	reason Reason,
) (DropoutOutcome, error) {
	givers, err := sortedActiveParticipants(ctx, q, exchange.ID)
	if err != nil {
		return "", err
	}

	deleteErr := q.DeleteAssignments(ctx, exchange.ID)
	if deleteErr != nil {
		return "", fmt.Errorf("delete assignments of %s: %w", exchange.ID, deleteErr)
	}

	voidErr := q.MarkExchangeVoided(ctx, exchange.ID)
	if voidErr != nil {
		return "", fmt.Errorf("void exchange %s: %w", exchange.ID, voidErr)
	}

	addressees := []uuid.UUID{exchange.OrganizerID}
	for _, giver := range givers {
		isOrganizer := giver == exchange.OrganizerID
		if !isOrganizer {
			addressees = append(addressees, giver)
		}
	}
	for _, addressee := range addressees {
		isDropout := addressee == dropout
		if isDropout {
			continue
		}

		queueErr := s.queueNotice(ctx, q, tx, KindRoundVoided, exchange, addressee, &reason)
		if queueErr != nil {
			return "", queueErr
		}
	}
	return OutcomeReopened, nil
}
