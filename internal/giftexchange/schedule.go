package giftexchange

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// Reminder targets: the instant a reminder counts down to.
const (
	targetDraw   = "draw"
	targetReveal = "reveal"
)

// scheduleDrawJobs inserts the deadline draw at drawAt and its reminder. A zero drawAt schedules
// nothing.
func (s *Service) scheduleDrawJobs(
	ctx context.Context,
	tx pgx.Tx,
	exchangeID uuid.UUID,
	drawAt time.Time,
	lead time.Duration,
) error {
	if drawAt.IsZero() {
		return nil
	}

	deadline := drawDeadlineArgs{ExchangeID: exchangeID.String(), DrawAt: formatArgInstant(drawAt)}
	scheduleErr := s.scheduler.ScheduleTx(ctx, tx, deadline, drawAt)
	if scheduleErr != nil {
		return fmt.Errorf("schedule draw deadline of %s: %w", exchangeID, scheduleErr)
	}

	return s.scheduleReminder(ctx, tx, exchangeID, targetDraw, drawAt, lead)
}

// scheduleRevealJobs inserts the timed reveal at revealAt and its reminder. A zero revealAt
// schedules nothing.
func (s *Service) scheduleRevealJobs(
	ctx context.Context,
	tx pgx.Tx,
	exchangeID uuid.UUID,
	revealAt time.Time,
	lead time.Duration,
) error {
	if revealAt.IsZero() {
		return nil
	}

	reveal := revealArgs{ExchangeID: exchangeID.String(), RevealAt: formatArgInstant(revealAt)}
	scheduleErr := s.scheduler.ScheduleTx(ctx, tx, reveal, revealAt)
	if scheduleErr != nil {
		return fmt.Errorf("schedule reveal of %s: %w", exchangeID, scheduleErr)
	}

	return s.scheduleReminder(ctx, tx, exchangeID, targetReveal, revealAt, lead)
}

// scheduleReminder inserts a reminder at targetAt - lead when lead is positive and that instant
// is still ahead.
func (s *Service) scheduleReminder(
	ctx context.Context,
	tx pgx.Tx,
	exchangeID uuid.UUID,
	target string,
	targetAt time.Time,
	lead time.Duration,
) error {
	hasLead := lead > 0
	if !hasLead {
		return nil
	}

	remindAt := targetAt.Add(-lead)
	isAhead := remindAt.After(s.now())
	if !isAhead {
		return nil
	}

	reminder := reminderArgs{
		ExchangeID:  exchangeID.String(),
		Target:      target,
		TargetAt:    formatArgInstant(targetAt),
		LeadSeconds: int(lead / time.Second),
	}
	scheduleErr := s.scheduler.ScheduleTx(ctx, tx, reminder, remindAt)
	if scheduleErr != nil {
		return fmt.Errorf("schedule %s reminder of %s: %w", target, exchangeID, scheduleErr)
	}

	return nil
}

// scheduleInitialJobs inserts the jobs of a new exchange's initial rules.
func (s *Service) scheduleInitialJobs(
	ctx context.Context,
	tx pgx.Tx,
	exchangeID uuid.UUID,
	settings ruleSettings,
) error {
	drawErr := s.scheduleDrawJobs(ctx, tx, exchangeID, settings.drawAt, settings.reminderLead)
	if drawErr != nil {
		return drawErr
	}

	return s.scheduleRevealJobs(ctx, tx, exchangeID, settings.revealAt, settings.reminderLead)
}

// scheduleRuleJobs inserts the jobs a changed rule makes due. Nothing is cancelled: a job whose
// rule changed again finds a different stored value and does nothing.
func (s *Service) scheduleRuleJobs(
	ctx context.Context,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	name RuleName,
	after ruleSettings,
) error {
	switch name {
	case RuleDrawAt:
		return s.scheduleDrawJobs(ctx, tx, exchange.ID, after.drawAt, after.reminderLead)
	case RuleRevealAt:
		return s.scheduleRevealJobs(ctx, tx, exchange.ID, after.revealAt, after.reminderLead)
	case RuleReminderLead:
		return s.scheduleLeadReminders(ctx, tx, exchange, after)
	default:
		return nil
	}
}

// scheduleLeadReminders inserts the reminders a new reminder_lead makes due.
func (s *Service) scheduleLeadReminders(
	ctx context.Context,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
	after ruleSettings,
) error {
	isOpen := exchange.State == string(StateOpen)
	if isOpen && !after.drawAt.IsZero() {
		drawErr := s.scheduleReminder(ctx, tx, exchange.ID, targetDraw, after.drawAt, after.reminderLead)
		if drawErr != nil {
			return drawErr
		}
	}

	if after.revealAt.IsZero() {
		return nil
	}

	return s.scheduleReminder(ctx, tx, exchange.ID, targetReveal, after.revealAt, after.reminderLead)
}

// scheduleRevealAfterDraw re-inserts the reveal jobs after a draw from Open, because a reveal job
// that fired while the exchange was Open completed as stale. A reveal_at that has passed is
// cleared instead.
func (s *Service) scheduleRevealAfterDraw(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
) error {
	if !exchange.RevealAt.Valid {
		return nil
	}

	revealAt := exchange.RevealAt.Time.UTC()
	isFuture := revealAt.After(s.now())
	if !isFuture {
		clearErr := q.ClearRevealAt(ctx, exchange.ID)
		if clearErr != nil {
			return fmt.Errorf("clear reveal_at of %s: %w", exchange.ID, clearErr)
		}
		return nil
	}

	lead := time.Duration(exchange.ReminderLeadSeconds) * time.Second

	return s.scheduleRevealJobs(ctx, tx, exchange.ID, revealAt, lead)
}

// formatArgInstant writes an instant the way job args carry it.
func formatArgInstant(instant time.Time) string {
	return instant.UTC().Format(time.RFC3339Nano)
}
