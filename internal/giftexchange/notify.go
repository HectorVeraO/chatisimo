package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// The outcomes a finished notification row records.
const (
	outcomeDelivered     = "delivered"
	outcomeUndeliverable = "undeliverable"
)

// notifyWorker delivers one outbox row through the Notifier port.
type notifyWorker struct {
	river.WorkerDefaults[notifyArgs]

	service *Service
}

// Work delivers the notification named by the job. Every error it returns carries IDs and kinds
// only, because River stores error text and panic traces.
func (w *notifyWorker) Work(ctx context.Context, job *river.Job[notifyArgs]) error {
	id, err := uuid.Parse(job.Args.NotificationID)
	if err != nil {
		// A malformed ID never becomes valid, so retrying would only repeat the failure.
		return river.JobCancel(fmt.Errorf("notify job %d: malformed notification ID", job.ID)) //nolint:wrapcheck // river.JobCancel is the cancel signal River reads
	}

	return w.service.deliver(ctx, id)
}

// deliver runs the delivery steps of one notification: load, decide, build, notify, record.
func (s *Service) deliver(ctx context.Context, id uuid.UUID) error {
	q := db.New(s.pool)

	row, err := q.GetNotification(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return river.JobCancel(fmt.Errorf("notify %s: notification not found", id)) //nolint:wrapcheck // river.JobCancel is the cancel signal River reads
	}
	if err != nil {
		return fmt.Errorf("notify %s: load notification: %w", id, err)
	}

	isFinished := row.Outcome != nil
	if isFinished {
		return nil
	}

	notification, err := s.buildNotification(ctx, q, row)
	if err != nil {
		return fmt.Errorf("notify %s %s: %w", id, row.Kind, err)
	}

	notifyErr := s.notifier.Notify(ctx, notification)
	if notifyErr == nil {
		return s.recordOutcome(ctx, q, row, outcomeDelivered)
	}

	if errors.Is(notifyErr, ErrUndeliverable) {
		return s.cancelUndeliverable(ctx, q, row, notifyErr)
	}

	return fmt.Errorf("notify %s %s: %w", id, row.Kind, notifyErr)
}

// cancelUndeliverable records the outcome and ends the job without retry.
func (s *Service) cancelUndeliverable(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	notifyErr error,
) error {
	recordErr := s.recordOutcome(ctx, q, row, outcomeUndeliverable)
	if recordErr != nil {
		return recordErr
	}

	s.logger.Warn("notification undeliverable",
		slog.String("notification_id", row.ID.String()),
		slog.String("kind", row.Kind))

	wrapped := fmt.Errorf("notify %s %s: %w", row.ID, row.Kind, notifyErr)
	return river.JobCancel(wrapped) //nolint:wrapcheck // river.JobCancel is the cancel signal River reads
}

// recordOutcome marks the row finished in its own statement, so no lock or transaction spans the
// Notify call.
func (s *Service) recordOutcome(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	outcome string,
) error {
	err := q.FinishNotification(ctx, db.FinishNotificationParams{
		ID:         row.ID,
		Outcome:    &outcome,
		FinishedAt: timestamptz(s.now()),
	})
	if err != nil {
		return fmt.Errorf("notify %s %s: record %s: %w", row.ID, row.Kind, outcome, err)
	}

	return nil
}

// buildNotification builds the Notification of row from current data. A kind with no delivery
// rule is a bug and panics with the kind only.
func (s *Service) buildNotification(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
) (Notification, error) {
	kind := NotificationKind(row.Kind)

	switch kind {
	case KindCancelled:
		return s.buildCancelled(ctx, q, row)
	default:
		panic("giftexchange: notify: no delivery rule for kind " + row.Kind)
	}
}

// buildCancelled builds the notice of a cancelled exchange: always delivered, no counterpart,
// question or reason.
func (s *Service) buildCancelled(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
) (Notification, error) {
	addressee := PersonID(row.PersonID)

	exchange, err := readRow(ctx, q, addressee, row.ExchangeID)
	if err != nil {
		return Notification{}, fmt.Errorf("read exchange: %w", err)
	}

	identities, err := identitiesOf(ctx, q, []uuid.UUID{row.PersonID})
	if err != nil {
		return Notification{}, fmt.Errorf("read identities: %w", err)
	}

	notification := Notification{
		ID:       NotificationID(row.ID),
		Kind:     KindCancelled,
		To:       PersonRef{ID: addressee, Identities: identities[row.PersonID]},
		Exchange: summaryOf(exchange, addressee),
	}

	return notification, nil
}
