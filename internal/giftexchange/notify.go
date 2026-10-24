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
	outcomeStale         = "stale"
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

	facts, isStale, err := s.assessNotification(ctx, q, row)
	if err != nil {
		return fmt.Errorf("notify %s %s: %w", id, row.Kind, err)
	}
	if isStale {
		return s.recordOutcome(ctx, q, row, outcomeStale)
	}

	notification, err := s.buildNotification(ctx, q, row, facts)
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

// noticeFacts is what the stale check learned about a notification, so the build step does not
// read it again. counterpart is set for the kinds that carry one.
type noticeFacts struct {
	exchange    exchangeRow
	counterpart *uuid.UUID
}

// assessNotification decides whether the notice is still true, and loads what the build needs.
// A kind with no rule here is never stale; buildNotification then decides whether it can be built.
func (s *Service) assessNotification(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
) (noticeFacts, bool, error) {
	exchange, err := readRow(ctx, q, PersonID(row.PersonID), row.ExchangeID)
	if err != nil {
		return noticeFacts{}, false, fmt.Errorf("read exchange: %w", err)
	}
	facts := noticeFacts{exchange: exchange}

	switch NotificationKind(row.Kind) {
	case KindDrawn, KindRedrawn, KindAssignmentChanged:
		return s.assessDrawn(ctx, q, row, facts)
	case KindRevealed:
		return s.assessRevealed(ctx, q, row, facts)
	default:
		return facts, false, nil
	}
}

// assessRevealed, the rule of the revealed kind: stale unless the exchange is Revealed and the
// addressee still has a giver in it.
func (s *Service) assessRevealed(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	facts noticeFacts,
) (noticeFacts, bool, error) {
	isRevealed := facts.exchange.exchange.State == string(StateRevealed)
	if !isRevealed {
		return facts, true, nil
	}

	giver, err := q.GetAssignmentOfRecipient(ctx, db.GetAssignmentOfRecipientParams{
		ExchangeID:  row.ExchangeID,
		RecipientID: row.PersonID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return facts, true, nil
	}
	if err != nil {
		return facts, false, fmt.Errorf("read giver: %w", err)
	}

	facts.counterpart = &giver
	return facts, false, nil
}

// assessDrawn, the rule of every pair kind: stale unless the exchange is Drawn in the notice's round and the addressee still
// gives to someone.
func (s *Service) assessDrawn(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	facts noticeFacts,
) (noticeFacts, bool, error) {
	exchange := facts.exchange.exchange
	isCurrent := exchange.State == string(StateDrawn) && exchange.Round == row.Round
	if !isCurrent {
		return facts, true, nil
	}

	recipient, err := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: row.ExchangeID,
		GiverID:    row.PersonID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return facts, true, nil
	}
	if err != nil {
		return facts, false, fmt.Errorf("read assignment: %w", err)
	}

	facts.counterpart = &recipient
	return facts, false, nil
}

// buildNotification builds the Notification of row from current data. A kind with no delivery
// rule is a bug and panics with the kind only.
func (s *Service) buildNotification(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	facts noticeFacts,
) (Notification, error) {
	kind := NotificationKind(row.Kind)

	switch kind {
	case KindCancelled, KindDrawn, KindRedrawn, KindAssignmentChanged, KindRoundVoided, KindRevealed:
		return s.buildNotice(ctx, q, row, facts)
	default:
		panic("giftexchange: notify: no delivery rule for kind " + row.Kind)
	}
}

// buildNotice builds a notice from the addressee, the exchange and, when the kind has one, the
// counterpart: the addressee's own recipient (drawn, redrawn, assignment_changed) or own giver
// (revealed). A voided
// round carries its reason. It carries no question.
func (s *Service) buildNotice(
	ctx context.Context,
	q *db.Queries,
	row db.GiftexchangeNotification,
	facts noticeFacts,
) (Notification, error) {
	addressee := PersonID(row.PersonID)

	people := []uuid.UUID{row.PersonID}
	if facts.counterpart != nil {
		people = append(people, *facts.counterpart)
	}
	identities, err := identitiesOf(ctx, q, people)
	if err != nil {
		return Notification{}, fmt.Errorf("read identities: %w", err)
	}

	notification := Notification{
		ID:       NotificationID(row.ID),
		Kind:     NotificationKind(row.Kind),
		To:       PersonRef{ID: addressee, Identities: identities[row.PersonID]},
		Exchange: summaryOf(facts.exchange, addressee),
	}
	if row.Reason != nil {
		notification.Reason = Reason(*row.Reason)
	}
	if facts.counterpart != nil {
		counterpart := PersonRef{ID: PersonID(*facts.counterpart), Identities: identities[*facts.counterpart]}
		notification.Counterpart = &counterpart
	}

	return notification, nil
}
