package giftexchange

import (
	"context"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

const notifyJobKind = "giftexchange.notify"

// notifyArgs are the args of the job that delivers one notification. They carry the row ID and
// nothing else, because River stores args.
type notifyArgs struct {
	NotificationID string `json:"notification_id"`
}

// Kind returns the River job kind.
func (notifyArgs) Kind() string {
	return notifyJobKind
}

// queueNotification writes one outbox row and its notify job in the caller's transaction, so a
// rolled-back change sends nothing and a committed one cannot lose its notice.
func (s *Service) queueNotification(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	kind NotificationKind,
	exchange db.GiftexchangeExchange,
	addressee uuid.UUID,
) error {
	id := uuid.NewV7()
	insertErr := q.InsertNotification(ctx, db.InsertNotificationParams{
		ID:         id,
		Kind:       string(kind),
		ExchangeID: exchange.ID,
		PersonID:   addressee,
		Round:      exchange.Round,
		CreatedAt:  timestamptz(s.now()),
	})
	if insertErr != nil {
		return fmt.Errorf("insert notification: %w", insertErr)
	}

	enqueueErr := s.scheduler.EnqueueTx(ctx, tx, notifyArgs{NotificationID: id.String()})
	if enqueueErr != nil {
		return fmt.Errorf("queue notification %s: %w", id, enqueueErr)
	}

	return nil
}
