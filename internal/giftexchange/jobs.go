package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
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
	return s.queueNotice(ctx, q, tx, kind, exchange, addressee, nil)
}

// queueNotice is queueNotification for the kinds that carry a reason (reason is nil for the
// others, as the notifications table requires).
func (s *Service) queueNotice(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	kind NotificationKind,
	exchange db.GiftexchangeExchange,
	addressee uuid.UUID,
	reason *Reason,
) error {
	var storedReason *string
	if reason != nil {
		text := string(*reason)
		storedReason = &text
	}

	params := db.InsertNotificationParams{
		ID:         uuid.NewV7(),
		Kind:       string(kind),
		ExchangeID: exchange.ID,
		PersonID:   addressee,
		Round:      exchange.Round,
		Reason:     storedReason,
		CreatedAt:  timestamptz(s.now()),
	}
	return s.enqueueNotice(ctx, q, tx, params)
}

// queueQuestionNotice queues a question_received or answer_received notice for question.
func (s *Service) queueQuestionNotice(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	kind NotificationKind,
	question uuid.UUID,
	exchange db.GiftexchangeExchange,
	addressee uuid.UUID,
) error {
	params := db.InsertNotificationParams{
		ID:         uuid.NewV7(),
		Kind:       string(kind),
		ExchangeID: exchange.ID,
		PersonID:   addressee,
		Round:      exchange.Round,
		QuestionID: pgtype.UUID{Bytes: question, Valid: true},
		CreatedAt:  timestamptz(s.now()),
	}
	return s.enqueueNotice(ctx, q, tx, params)
}

// enqueueNotice stores the outbox row and enqueues its notify job.
func (s *Service) enqueueNotice(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	params db.InsertNotificationParams,
) error {
	insertErr := q.InsertNotification(ctx, params)
	if insertErr != nil {
		return fmt.Errorf("insert notification: %w", insertErr)
	}

	enqueueErr := s.scheduler.EnqueueTx(ctx, tx, notifyArgs{NotificationID: params.ID.String()})
	if enqueueErr != nil {
		return fmt.Errorf("queue notification %s: %w", params.ID, enqueueErr)
	}

	return nil
}

// RegisterJobs adds the module's River workers to r. The composition root calls it once, after
// New and before building the jobs.Runner from r.
func (s *Service) RegisterJobs(r *jobs.Registry) error {
	if r == nil {
		return &InputError{Field: "registry", Reason: "is required"}
	}

	err := errors.Join(
		jobs.Register[notifyArgs](r, &notifyWorker{service: s}),
		jobs.Register[drawDeadlineArgs](r, &drawDeadlineWorker{service: s}),
		jobs.Register[reminderArgs](r, &reminderWorker{service: s}),
		jobs.Register[revealArgs](r, &revealWorker{service: s}),
	)
	if err != nil {
		return fmt.Errorf("register giftexchange jobs: %w", err)
	}

	return nil
}

const (
	drawDeadlineJobKind = "giftexchange.draw_deadline"
	reminderJobKind     = "giftexchange.reminder"
	revealJobKind       = "giftexchange.reveal"
)

// drawDeadlineArgs are the args of the job that draws at the deadline. IDs and instants only,
// because River stores args.
type drawDeadlineArgs struct {
	ExchangeID string `json:"exchange_id"`
	DrawAt     string `json:"draw_at"`
}

// Kind returns the River job kind.
func (drawDeadlineArgs) Kind() string {
	return drawDeadlineJobKind
}

// reminderArgs are the args of a reminder job: Target is "draw" or "reveal", TargetAt the instant
// it counts down to.
type reminderArgs struct {
	ExchangeID  string `json:"exchange_id"`
	Target      string `json:"target"`
	TargetAt    string `json:"target_at"`
	LeadSeconds int    `json:"lead_seconds"`
}

// Kind returns the River job kind.
func (reminderArgs) Kind() string {
	return reminderJobKind
}

// revealArgs are the args of the job that reveals at reveal_at.
type revealArgs struct {
	ExchangeID string `json:"exchange_id"`
	RevealAt   string `json:"reveal_at"`
}

// Kind returns the River job kind.
func (revealArgs) Kind() string {
	return revealJobKind
}

// malformedJob cancels a job whose args can never become valid, so it is not retried.
func malformedJob(kind string, id int64) error {
	return river.JobCancel(fmt.Errorf("%s job %d: malformed args", kind, id)) //nolint:wrapcheck // river.JobCancel is the cancel signal River reads
}

// jobTarget is the parsed exchange ID and instant of a job.
type jobTarget struct {
	exchangeID uuid.UUID
	at         time.Time
}

func parseJobTarget(exchangeID, instant string) (jobTarget, bool) {
	id, idErr := uuid.Parse(exchangeID)
	at, atErr := time.Parse(time.RFC3339Nano, instant)
	if idErr != nil || atErr != nil {
		return jobTarget{}, false
	}

	return jobTarget{exchangeID: id, at: at}, true
}

// lockForJob locks the exchange row. found is false when the exchange does not exist.
func lockForJob(ctx context.Context, q *db.Queries, id uuid.UUID) (db.GiftexchangeExchange, bool, error) {
	exchange, err := q.LockExchange(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GiftexchangeExchange{}, false, nil
	}
	if err != nil {
		return db.GiftexchangeExchange{}, false, fmt.Errorf("lock exchange %s: %w", id, err)
	}

	return exchange, true, nil
}

// instantMatches reports whether a stored nullable instant equals at.
func instantMatches(stored pgtype.Timestamptz, at time.Time) bool {
	return stored.Valid && stored.Time.Equal(at)
}

// drawDeadlineWorker draws an Open exchange at its draw_at.
type drawDeadlineWorker struct {
	river.WorkerDefaults[drawDeadlineArgs]

	service *Service
}

// Work runs the deadline draw when the job is current; a failed draw is reported to the organizer
// and the job completes.
func (w *drawDeadlineWorker) Work(ctx context.Context, job *river.Job[drawDeadlineArgs]) error {
	target, ok := parseJobTarget(job.Args.ExchangeID, job.Args.DrawAt)
	if !ok {
		return malformedJob(drawDeadlineJobKind, job.ID)
	}

	return w.service.runDrawDeadline(ctx, target)
}

func (s *Service) runDrawDeadline(ctx context.Context, target jobTarget) error {
	didDraw := false
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, found, err := lockForJob(ctx, q, target.exchangeID)
		if err != nil {
			return err
		}

		isCurrent := found && exchange.State == string(StateOpen) && instantMatches(exchange.DrawAt, target.at)
		if !isCurrent {
			return nil
		}

		drawErr := s.drawLocked(ctx, q, tx, exchange)
		if drawErr == nil {
			didDraw = true
			return nil
		}

		reason, isFailure := drawFailureReason(drawErr)
		if !isFailure {
			return fmt.Errorf("deadline draw of %s: %w", exchange.ID, drawErr)
		}

		return s.failDeadlineDraw(ctx, q, tx, exchange, reason)
	})
	if txErr != nil {
		return txErr
	}

	if didDraw {
		s.logger.InfoContext(ctx, "exchange drawn at deadline", "exchange_id", target.exchangeID.String())
	}

	return nil
}

// drawFailureReason maps the two draw failures a deadline reports to the organizer.
func drawFailureReason(err error) (Reason, bool) {
	if errors.Is(err, ErrTooFewParticipants) {
		return ReasonTooFewParticipants, true
	}
	if errors.Is(err, ErrNoValidAssignment) {
		return ReasonNoValidAssignment, true
	}

	return "", false
}

// failDeadlineDraw clears draw_at and tells the organizer. A failed draw wrote nothing, so the
// transaction can commit.
func (s *Service) failDeadlineDraw(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	reason Reason,
) error {
	clearErr := q.ClearDrawAt(ctx, exchange.ID)
	if clearErr != nil {
		return fmt.Errorf("clear draw_at of %s: %w", exchange.ID, clearErr)
	}

	return s.queueNotice(ctx, q, tx, KindDrawFailed, exchange, exchange.OrganizerID, &reason)
}

// revealWorker reveals a Drawn exchange at its reveal_at.
type revealWorker struct {
	river.WorkerDefaults[revealArgs]

	service *Service
}

// Work reveals the exchange when the job is current.
func (w *revealWorker) Work(ctx context.Context, job *river.Job[revealArgs]) error {
	target, ok := parseJobTarget(job.Args.ExchangeID, job.Args.RevealAt)
	if !ok {
		return malformedJob(revealJobKind, job.ID)
	}

	return w.service.runReveal(ctx, target)
}

func (s *Service) runReveal(ctx context.Context, target jobTarget) error {
	didReveal := false
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, found, err := lockForJob(ctx, q, target.exchangeID)
		if err != nil {
			return err
		}

		isCurrent := found && exchange.State == string(StateDrawn) && instantMatches(exchange.RevealAt, target.at)
		if !isCurrent {
			return nil
		}

		didReveal = true

		return s.revealLocked(ctx, q, tx, exchange)
	})
	if txErr != nil {
		return txErr
	}

	if didReveal {
		s.logger.InfoContext(ctx, "exchange revealed at reveal_at", "exchange_id", target.exchangeID.String())
	}

	return nil
}

// reminderWorker queues the reminder notices of a draw or reveal.
type reminderWorker struct {
	river.WorkerDefaults[reminderArgs]

	service *Service
}

// Work queues the reminder notices when the job is current.
func (w *reminderWorker) Work(ctx context.Context, job *river.Job[reminderArgs]) error {
	target, ok := parseJobTarget(job.Args.ExchangeID, job.Args.TargetAt)
	isKnownTarget := job.Args.Target == targetDraw || job.Args.Target == targetReveal
	if !ok || !isKnownTarget {
		return malformedJob(reminderJobKind, job.ID)
	}

	return w.service.runReminder(ctx, target, job.Args.Target, job.Args.LeadSeconds)
}

func (s *Service) runReminder(ctx context.Context, target jobTarget, kind string, leadSeconds int) error {
	return s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		exchange, found, err := lockForJob(ctx, q, target.exchangeID)
		if err != nil {
			return err
		}

		hasLead := found && int(exchange.ReminderLeadSeconds) == leadSeconds
		if !hasLead {
			return nil
		}

		if kind == targetDraw {
			return s.remindDraw(ctx, q, tx, exchange, target.at)
		}

		return s.remindReveal(ctx, q, tx, exchange, target.at)
	})
}

// remindDraw tells the organizer and every active participant, once each, that the draw is near.
func (s *Service) remindDraw(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	targetAt time.Time,
) error {
	isCurrent := exchange.State == string(StateOpen) &&
		instantMatches(exchange.DrawAt, targetAt) &&
		!instantMatches(exchange.DrawReminderSentFor, targetAt)
	if !isCurrent {
		return nil
	}

	participants, err := sortedActiveParticipants(ctx, q, exchange.ID)
	if err != nil {
		return err
	}

	addressees := []uuid.UUID{exchange.OrganizerID}
	for _, participant := range participants {
		isOrganizer := participant == exchange.OrganizerID
		if !isOrganizer {
			addressees = append(addressees, participant)
		}
	}
	for _, addressee := range addressees {
		queueErr := s.queueNotification(ctx, q, tx, KindDrawSoon, exchange, addressee)
		if queueErr != nil {
			return queueErr
		}
	}

	markErr := q.MarkDrawReminderSent(ctx, db.MarkDrawReminderSentParams{
		ID:      exchange.ID,
		SentFor: timestamptz(targetAt),
	})
	if markErr != nil {
		return fmt.Errorf("mark draw reminder of %s: %w", exchange.ID, markErr)
	}

	return nil
}

// remindReveal tells every giver that the reveal is near.
func (s *Service) remindReveal(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	targetAt time.Time,
) error {
	isCurrent := exchange.State == string(StateDrawn) &&
		instantMatches(exchange.RevealAt, targetAt) &&
		!instantMatches(exchange.RevealReminderSentFor, targetAt)
	if !isCurrent {
		return nil
	}

	givers, err := q.ListAssignmentGivers(ctx, exchange.ID)
	if err != nil {
		return fmt.Errorf("list givers of %s: %w", exchange.ID, err)
	}

	for _, giver := range givers {
		queueErr := s.queueNotification(ctx, q, tx, KindRevealSoon, exchange, giver)
		if queueErr != nil {
			return queueErr
		}
	}

	markErr := q.MarkRevealReminderSent(ctx, db.MarkRevealReminderSentParams{
		ID:      exchange.ID,
		SentFor: timestamptz(targetAt),
	})
	if markErr != nil {
		return fmt.Errorf("mark reveal reminder of %s: %w", exchange.ID, markErr)
	}

	return nil
}
