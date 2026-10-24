package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// CreateExchangeRequest creates an exchange in Group with the caller as organizer. Rules are
// applied in order as if by SetRule in state Open; every invalid rule and the name are reported
// together. The organizer is not a participant until they Join.
type CreateExchangeRequest struct {
	Group GroupID
	Name  string
	Rules []RuleChange
}

// CreateExchange opens a new exchange. Any person calling with the group may create one.
func (s *Service) CreateExchange(ctx context.Context, a Actor, req CreateExchangeRequest) (ExchangeView, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return ExchangeView{}, authErr
	}

	name, settings, validationErr := validateCreate(req, s.now())
	if validationErr != nil {
		return ExchangeView{}, validationErr
	}

	var view ExchangeView
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		var createErr error
		view, createErr = s.createExchange(ctx, q, tx, a, req.Group, name, settings)
		return createErr
	})
	if txErr != nil {
		return ExchangeView{}, txErr
	}

	s.logger.InfoContext(ctx, "exchange created", "exchange_id", view.ID.String())

	return view, nil
}

// validateCreate reports the group, the name and every rule error together, and returns the
// settings the initial rules produce.
func validateCreate(req CreateExchangeRequest, now time.Time) (string, ruleSettings, error) {
	var problems []error
	groupErr := requireID("group_id", req.Group.IsZero())
	if groupErr != nil {
		problems = append(problems, groupErr)
	}

	name, nameErr := ParseExchangeName(req.Name)
	if nameErr != nil {
		problems = append(problems, nameErr)
	}

	settings := defaultSettings()
	for _, change := range req.Rules {
		value, parseErr := parseChange(change)
		if parseErr != nil {
			problems = append(problems, parseErr)
			continue
		}

		checkErr := settings.check(value, now)
		if checkErr != nil {
			problems = append(problems, checkErr)
			continue
		}
		settings = settings.assign(value)
	}

	if len(problems) > 0 {
		return "", ruleSettings{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}
	return name, settings, nil
}

func (s *Service) createExchange(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	a Actor,
	group GroupID,
	name string,
	settings ruleSettings,
) (ExchangeView, error) {
	_, groupErr := q.GetGroup(ctx, uuid.UUID(group))
	if errors.Is(groupErr, pgx.ErrNoRows) {
		return ExchangeView{}, ErrNotFound
	}
	if groupErr != nil {
		return ExchangeView{}, fmt.Errorf("get group: %w", groupErr)
	}

	id := uuid.NewV7()
	insertErr := q.InsertExchange(ctx, db.InsertExchangeParams{
		ID:                  id,
		GroupID:             uuid.UUID(group),
		OrganizerID:         uuid.UUID(a.person),
		Name:                name,
		DrawAt:              optionalTimestamptz(settings.drawAt),
		RevealAt:            optionalTimestamptz(settings.revealAt),
		ReminderLeadSeconds: int32(settings.reminderLead.Seconds()),
		DropoutPolicy:       settings.dropoutPolicy,
		AvoidLastRound:      settings.avoidLastRound,
		QuestionsEnabled:    settings.questionsEnabled,
		CreatedAt:           timestamptz(s.now()),
	})
	if insertErr != nil {
		return ExchangeView{}, fmt.Errorf("insert exchange %s: %w", id, insertErr)
	}

	scheduleErr := s.scheduleInitialJobs(ctx, tx, id, settings)
	if scheduleErr != nil {
		return ExchangeView{}, scheduleErr
	}

	row, err := readRow(ctx, q, a.person, id)
	if err != nil {
		return ExchangeView{}, err
	}

	return viewOf(ctx, q, row, a.person)
}

// readRow loads an exchange with its joins; a missing row is ErrNotFound.
func readRow(ctx context.Context, q *db.Queries, caller PersonID, id uuid.UUID) (exchangeRow, error) {
	loaded, err := q.GetExchangeSummary(ctx, db.GetExchangeSummaryParams{
		CallerID: uuid.UUID(caller),
		ID:       id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return exchangeRow{}, ErrNotFound
	}
	if err != nil {
		return exchangeRow{}, fmt.Errorf("get exchange %s: %w", id, err)
	}

	row := exchangeRow{
		exchange:            loaded.GiftexchangeExchange,
		groupProvider:       loaded.GroupProvider,
		groupSubject:        loaded.GroupSubject,
		participantCount:    loaded.ParticipantCount,
		callerIsParticipant: loaded.CallerIsParticipant,
	}
	return row, nil
}

// Exchange returns the view of one exchange of group g. Any caller in the group may read it.
func (s *Service) Exchange(ctx context.Context, a Actor, g GroupID, id ExchangeID) (ExchangeView, error) {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return ExchangeView{}, validationErr
	}

	q := db.New(s.pool)
	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return ExchangeView{}, err
	}

	isOtherGroup := GroupID(row.exchange.GroupID) != g
	if isOtherGroup {
		return ExchangeView{}, ErrNotFound
	}

	return viewOf(ctx, q, row, a.person)
}

// ExchangesFor lists exchanges with the caller's relation to each, newest first.
// With g set: every exchange of that group that is not Cancelled (feeds "list" and the "join"
// autocomplete: Open and not CallerIsParticipant).
// With g nil: every exchange in any group that is not Cancelled and where the caller is the
// organizer or an active participant (feeds DM autocomplete for "recipient" and "ask").
func (s *Service) ExchangesFor(ctx context.Context, a Actor, g *GroupID) ([]ExchangeSummary, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return nil, authErr
	}
	if g != nil {
		groupErr := requireID("group_id", g.IsZero())
		if groupErr != nil {
			return nil, groupErr
		}
	}

	q := db.New(s.pool)
	rows, err := listRows(ctx, q, a.person, g)
	if err != nil {
		return nil, err
	}

	return buildSummaries(rows, a.person), nil
}

func listRows(ctx context.Context, q *db.Queries, caller PersonID, g *GroupID) ([]exchangeRow, error) {
	if g != nil {
		listed, err := q.ListGroupExchanges(ctx, db.ListGroupExchangesParams{
			CallerID: uuid.UUID(caller),
			GroupID:  uuid.UUID(*g),
		})
		if err != nil {
			return nil, fmt.Errorf("list group exchanges: %w", err)
		}

		rows := make([]exchangeRow, 0, len(listed))
		for _, item := range listed {
			rows = append(rows, exchangeRow{
				exchange:            item.GiftexchangeExchange,
				groupProvider:       item.GroupProvider,
				groupSubject:        item.GroupSubject,
				participantCount:    item.ParticipantCount,
				callerIsParticipant: item.CallerIsParticipant,
			})
		}
		return rows, nil
	}

	listed, err := q.ListCallerExchanges(ctx, uuid.UUID(caller))
	if err != nil {
		return nil, fmt.Errorf("list caller exchanges: %w", err)
	}

	rows := make([]exchangeRow, 0, len(listed))
	for _, item := range listed {
		rows = append(rows, exchangeRow{
			exchange:            item.GiftexchangeExchange,
			groupProvider:       item.GroupProvider,
			groupSubject:        item.GroupSubject,
			participantCount:    item.ParticipantCount,
			callerIsParticipant: item.CallerIsParticipant,
		})
	}
	return rows, nil
}

// Rename changes the exchange name. Organizer only; Open or Drawn.
func (s *Service) Rename(ctx context.Context, a Actor, g GroupID, id ExchangeID, name string) error {
	authErr := requireActor(a)
	if authErr != nil {
		return authErr
	}

	scopeErr := validateScope(a, g, id)
	parsed, nameErr := ParseExchangeName(name)
	validationErr := errors.Join(scopeErr, nameErr)
	if validationErr != nil {
		return validationErr //nolint:wrapcheck // joins this function's own validation errors
	}

	return s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		exchange, err := lockExchange(ctx, q, g, id)
		if err != nil {
			return err
		}

		guardErr := requireOrganizer(exchange, a)
		if guardErr != nil {
			return guardErr
		}

		isRenamable := exchange.State == string(StateOpen) || exchange.State == string(StateDrawn)
		if !isRenamable {
			return &StateError{Op: "rename", State: State(exchange.State)}
		}

		updateErr := q.UpdateExchangeName(ctx, db.UpdateExchangeNameParams{ID: exchange.ID, Name: parsed})
		if updateErr != nil {
			return fmt.Errorf("rename exchange %s: %w", exchange.ID, updateErr)
		}
		return nil
	})
}

// Cancel ends the exchange: assignments deleted, participants notified.
// Organizer only; Open or Drawn.
func (s *Service) Cancel(ctx context.Context, a Actor, g GroupID, id ExchangeID) error {
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

		isCancellable := exchange.State == string(StateOpen) || exchange.State == string(StateDrawn)
		if !isCancellable {
			return &StateError{Op: "cancel", State: State(exchange.State)}
		}

		return s.cancelLocked(ctx, q, tx, exchange)
	})
	if txErr != nil {
		return txErr
	}

	s.logger.InfoContext(ctx, "exchange cancelled", "exchange_id", id.String())

	return nil
}

func (s *Service) cancelLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	exchange db.GiftexchangeExchange,
) error {
	cancelErr := q.CancelExchange(ctx, db.CancelExchangeParams{
		ID:          exchange.ID,
		CancelledAt: timestamptz(s.now()),
	})
	if cancelErr != nil {
		return fmt.Errorf("cancel exchange %s: %w", exchange.ID, cancelErr)
	}

	deleteErr := q.DeleteAssignments(ctx, exchange.ID)
	if deleteErr != nil {
		return fmt.Errorf("delete assignments of %s: %w", exchange.ID, deleteErr)
	}

	participants, listErr := q.ListActiveParticipants(ctx, exchange.ID)
	if listErr != nil {
		return fmt.Errorf("list participants of %s: %w", exchange.ID, listErr)
	}

	for _, participant := range participants {
		isOrganizer := participant.PersonID == exchange.OrganizerID
		if isOrganizer {
			continue
		}

		queueErr := s.queueNotification(ctx, q, tx, KindCancelled, exchange, participant.PersonID)
		if queueErr != nil {
			return queueErr
		}
	}
	return nil
}

// validateScope checks the actor and the IDs every group-scoped operation takes.
func validateScope(a Actor, g GroupID, id ExchangeID) error {
	authErr := requireActor(a)
	if authErr != nil {
		return authErr
	}

	var problems []error
	groupErr := requireID("group_id", g.IsZero())
	if groupErr != nil {
		problems = append(problems, groupErr)
	}
	idErr := requireID("exchange_id", id.IsZero())
	if idErr != nil {
		problems = append(problems, idErr)
	}
	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}
	return nil
}

// lockExchange takes the row lock of an exchange of group g. An exchange that is missing or
// belongs to another group is ErrNotFound, so a pasted ID reveals nothing across groups.
func lockExchange(ctx context.Context, q *db.Queries, g GroupID, id ExchangeID) (db.GiftexchangeExchange, error) {
	exchange, err := q.LockExchange(ctx, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GiftexchangeExchange{}, ErrNotFound
	}
	if err != nil {
		return db.GiftexchangeExchange{}, fmt.Errorf("lock exchange %s: %w", id, err)
	}

	isOtherGroup := GroupID(exchange.GroupID) != g
	if isOtherGroup {
		return db.GiftexchangeExchange{}, ErrNotFound
	}
	return exchange, nil
}

func requireOrganizer(exchange db.GiftexchangeExchange, a Actor) error {
	isOrganizer := PersonID(exchange.OrganizerID) == a.person
	if !isOrganizer {
		return ErrForbidden
	}
	return nil
}
