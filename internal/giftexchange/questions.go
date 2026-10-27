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

const questionRateWindow = 24 * time.Hour

// Ask rephrases text with the configured Rephraser, stores only the rephrased text, and sends it
// anonymously from the caller to the caller's current recipient via a QuestionReceived
// notification. Active participant with an assignment; Drawn only; questions_enabled; at most
// QuestionsPerDay per caller per exchange in any rolling 24 hours (ErrRateLimited). A person with
// no tie to the exchange gets ErrNotFound. If rephrasing fails or returns invalid text, Ask
// returns ErrRephraseUnavailable without storing a question or consuming rate-limit quota.
func (s *Service) Ask(ctx context.Context, a Actor, id ExchangeID, text string) (QuestionID, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return QuestionID{}, authErr
	}

	idErr := requireID("exchange_id", id.IsZero())
	parsed, textErr := ParseQuestionText(text)
	validationErr := errors.Join(idErr, textErr)
	if validationErr != nil {
		return QuestionID{}, validationErr //nolint:wrapcheck // joins this function's own validation errors
	}

	precheckErr := s.askPrecheck(ctx, a, id)
	if precheckErr != nil {
		return QuestionID{}, precheckErr
	}

	rephrasedRaw, rephraseErr := s.rephraser.Rephrase(ctx, parsed)
	if rephraseErr != nil {
		s.logger.WarnContext(ctx, "question rephrase failed", "exchange_id", id.String())
		return QuestionID{}, fmt.Errorf("rephrase question: %w", ErrRephraseUnavailable)
	}

	rephrased, rephraseParseErr := ParseQuestionText(rephrasedRaw)
	if rephraseParseErr != nil {
		s.logger.WarnContext(ctx, "question rephrase returned invalid text", "exchange_id", id.String())
		return QuestionID{}, fmt.Errorf("rephrase question output invalid: %w", ErrRephraseUnavailable)
	}

	var asked QuestionID
	txErr := s.inTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		var err error
		asked, err = s.askLocked(ctx, q, tx, a, id, rephrased)
		return err
	})
	if txErr != nil {
		return QuestionID{}, txErr
	}

	s.logger.InfoContext(ctx, "question asked", "question_id", asked.String())

	return asked, nil
}

// askPrecheck verifies that the caller may ask a question before invoking the external Rephraser
// boundary, without holding a database row lock across the network call.
func (s *Service) askPrecheck(ctx context.Context, a Actor, id ExchangeID) error {
	q := db.New(s.pool)

	exchange, err := activeParticipantExchange(ctx, q, a, id)
	if err != nil {
		return err
	}

	isDrawn := exchange.State == string(StateDrawn)
	if !isDrawn {
		return &StateError{Op: "ask", State: State(exchange.State)}
	}
	if !exchange.QuestionsEnabled {
		return ErrQuestionsDisabled
	}

	now := s.now()
	recent, countErr := q.CountQuestionsAskedSince(ctx, db.CountQuestionsAskedSinceParams{
		ExchangeID: exchange.ID,
		AskerID:    uuid.UUID(a.person),
		Since:      timestamptz(now.Add(-questionRateWindow)),
	})
	if countErr != nil {
		return fmt.Errorf("count questions of %s: %w", exchange.ID, countErr)
	}
	isOverLimit := recent >= QuestionsPerDay
	if isOverLimit {
		return ErrRateLimited
	}

	_, lookupErr := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    uuid.UUID(a.person),
	})
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return &StateError{Op: "ask", State: State(exchange.State)}
	}
	if lookupErr != nil {
		return fmt.Errorf("get assignment in %s: %w", exchange.ID, lookupErr)
	}

	return nil
}

// askLocked runs the checks of Ask in order under the exchange row lock, which also serializes
// the rate-limit count against concurrent asks.
func (s *Service) askLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	a Actor,
	id ExchangeID,
	text string,
) (QuestionID, error) {
	exchange, err := lockExchangeByID(ctx, q, id)
	if err != nil {
		return QuestionID{}, err
	}

	isRelated, isActive, relationErr := callerRelation(ctx, q, a, exchange)
	if relationErr != nil {
		return QuestionID{}, relationErr
	}
	if !isRelated {
		return QuestionID{}, ErrNotFound
	}
	if !isActive {
		return QuestionID{}, ErrNotParticipant
	}

	isDrawn := exchange.State == string(StateDrawn)
	if !isDrawn {
		return QuestionID{}, &StateError{Op: "ask", State: State(exchange.State)}
	}
	if !exchange.QuestionsEnabled {
		return QuestionID{}, ErrQuestionsDisabled
	}

	now := s.now()
	recent, countErr := q.CountQuestionsAskedSince(ctx, db.CountQuestionsAskedSinceParams{
		ExchangeID: exchange.ID,
		AskerID:    uuid.UUID(a.person),
		Since:      timestamptz(now.Add(-questionRateWindow)),
	})
	if countErr != nil {
		return QuestionID{}, fmt.Errorf("count questions of %s: %w", exchange.ID, countErr)
	}
	isOverLimit := recent >= QuestionsPerDay
	if isOverLimit {
		return QuestionID{}, ErrRateLimited
	}

	recipient, lookupErr := q.GetAssignmentOfGiver(ctx, db.GetAssignmentOfGiverParams{
		ExchangeID: exchange.ID,
		GiverID:    uuid.UUID(a.person),
	})
	if lookupErr != nil {
		return QuestionID{}, fmt.Errorf("get assignment of %s: %w", exchange.ID, lookupErr)
	}

	question := db.InsertQuestionParams{
		ID:          uuid.NewV7(),
		ExchangeID:  exchange.ID,
		Round:       exchange.Round,
		AskerID:     uuid.UUID(a.person),
		RecipientID: recipient,
		Text:        text,
		AskedAt:     timestamptz(now),
	}
	insertErr := q.InsertQuestion(ctx, question)
	if insertErr != nil {
		return QuestionID{}, fmt.Errorf("insert question in %s: %w", exchange.ID, insertErr)
	}

	queueErr := s.queueQuestionNotice(ctx, q, tx, KindQuestionReceived, question.ID, exchange, recipient)
	if queueErr != nil {
		return QuestionID{}, queueErr
	}

	return QuestionID(question.ID), nil
}

// Answer replies once (ParseQuestionText rules) to a question addressed to the caller and queues
// an AnswerReceived notification to the asker. The question must be open and unanswered
// (ErrQuestionClosed, ErrAlreadyAnswered). Anyone but the question's recipient gets ErrNotFound,
// because a question's existence is private. Turning questions off does not block answers.
func (s *Service) Answer(ctx context.Context, a Actor, q QuestionID, text string) error {
	authErr := requireActor(a)
	if authErr != nil {
		return authErr
	}

	idErr := requireID("question_id", q.IsZero())
	parsed, textErr := ParseQuestionText(text)
	validationErr := errors.Join(idErr, textErr)
	if validationErr != nil {
		return validationErr //nolint:wrapcheck // joins this function's own validation errors
	}

	txErr := s.inTx(ctx, func(queries *db.Queries, tx pgx.Tx) error {
		return s.answerLocked(ctx, queries, tx, a, q, parsed)
	})
	if txErr != nil {
		return txErr
	}

	s.logger.InfoContext(ctx, "question answered", "question_id", q.String())

	return nil
}

// answerLocked finds the question, locks its exchange and reads the question again under the
// lock, so a closure that committed first is seen.
func (s *Service) answerLocked(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	a Actor,
	id QuestionID,
	text string,
) error {
	found, err := getQuestionFor(ctx, q, a, id)
	if err != nil {
		return err
	}

	exchange, lockErr := lockExchangeByID(ctx, q, ExchangeID(found.ExchangeID))
	if lockErr != nil {
		return lockErr
	}

	question, err := getQuestionFor(ctx, q, a, id)
	if err != nil {
		return err
	}

	if question.ClosedAt.Valid {
		return ErrQuestionClosed
	}
	if question.AnsweredAt.Valid {
		return ErrAlreadyAnswered
	}

	updated, answerErr := q.AnswerQuestion(ctx, db.AnswerQuestionParams{
		ID:         question.ID,
		Answer:     &text,
		AnsweredAt: timestamptz(s.now()),
	})
	if answerErr != nil {
		return fmt.Errorf("answer question %s: %w", question.ID, answerErr)
	}
	didUpdate := updated == 1
	if !didUpdate {
		panic(fmt.Sprintf("giftexchange: answer: an unanswered question held under the exchange lock updated %d rows", updated))
	}

	return s.queueQuestionNotice(ctx, q, tx, KindAnswerReceived, question.ID, exchange, question.AskerID)
}

// getQuestionFor loads a question addressed to the caller; any other case is ErrNotFound.
func getQuestionFor(ctx context.Context, q *db.Queries, a Actor, id QuestionID) (db.GiftexchangeQuestion, error) {
	question, err := q.GetQuestion(ctx, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GiftexchangeQuestion{}, ErrNotFound
	}
	if err != nil {
		return db.GiftexchangeQuestion{}, fmt.Errorf("get question %s: %w", id, err)
	}

	isAddressee := question.RecipientID == uuid.UUID(a.person)
	if !isAddressee {
		return db.GiftexchangeQuestion{}, ErrNotFound
	}
	return question, nil
}

// QuestionsAsked lists the questions the caller asked in the exchange, oldest first. Any state.
// A person with no tie to the exchange gets ErrNotFound.
func (s *Service) QuestionsAsked(ctx context.Context, a Actor, id ExchangeID) ([]AskedQuestion, error) {
	q, exchange, err := s.relatedExchangeForListing(ctx, a, id)
	if err != nil {
		return nil, err
	}

	rows, listErr := q.ListQuestionsAsked(ctx, db.ListQuestionsAskedParams{
		ExchangeID: exchange.ID,
		AskerID:    uuid.UUID(a.person),
	})
	if listErr != nil {
		return nil, fmt.Errorf("list questions of %s: %w", exchange.ID, listErr)
	}

	asked := make([]AskedQuestion, 0, len(rows))
	for _, row := range rows {
		view := questionViewOf(row)
		asked = append(asked, AskedQuestion(view))
	}
	return asked, nil
}

// QuestionsReceived lists the questions addressed to the caller in the exchange, oldest first.
// Any state. A person with no tie to the exchange gets ErrNotFound.
func (s *Service) QuestionsReceived(ctx context.Context, a Actor, id ExchangeID) ([]ReceivedQuestion, error) {
	q, exchange, err := s.relatedExchangeForListing(ctx, a, id)
	if err != nil {
		return nil, err
	}

	rows, listErr := q.ListQuestionsReceived(ctx, db.ListQuestionsReceivedParams{
		ExchangeID:  exchange.ID,
		RecipientID: uuid.UUID(a.person),
	})
	if listErr != nil {
		return nil, fmt.Errorf("list questions of %s: %w", exchange.ID, listErr)
	}

	received := make([]ReceivedQuestion, 0, len(rows))
	for _, row := range rows {
		view := questionViewOf(row)
		received = append(received, ReceivedQuestion(view))
	}
	return received, nil
}

// relatedExchangeForListing validates the call and checks that the caller organizes the exchange
// or holds a participants row for it, active or left; anyone else gets ErrNotFound.
func (s *Service) relatedExchangeForListing(
	ctx context.Context,
	a Actor,
	id ExchangeID,
) (*db.Queries, db.GiftexchangeExchange, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return nil, db.GiftexchangeExchange{}, authErr
	}
	idErr := requireID("exchange_id", id.IsZero())
	if idErr != nil {
		return nil, db.GiftexchangeExchange{}, idErr
	}

	q := db.New(s.pool)
	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return nil, db.GiftexchangeExchange{}, err
	}

	isRelated, _, relationErr := callerRelation(ctx, q, a, row.exchange)
	if relationErr != nil {
		return nil, db.GiftexchangeExchange{}, relationErr
	}
	if !isRelated {
		return nil, db.GiftexchangeExchange{}, ErrNotFound
	}
	return q, row.exchange, nil
}

// questionView is the part of a stored question that both listing types show. AskedQuestion and
// ReceivedQuestion have the same fields, so each converts from it directly.
type questionView struct {
	ID         QuestionID
	Exchange   ExchangeID
	Text       string
	AskedAt    time.Time
	Answer     string
	AnsweredAt *time.Time
	ClosedAt   *time.Time
}

func questionViewOf(row db.GiftexchangeQuestion) questionView {
	view := questionView{
		ID:         QuestionID(row.ID),
		Exchange:   ExchangeID(row.ExchangeID),
		Text:       row.Text,
		AskedAt:    row.AskedAt.Time.UTC(),
		AnsweredAt: optionalTime(row.AnsweredAt),
		ClosedAt:   optionalTime(row.ClosedAt),
	}
	if row.Answer != nil {
		view.Answer = *row.Answer
	}
	return view
}

// lockExchangeByID locks the exchange row; a missing row is ErrNotFound.
func lockExchangeByID(ctx context.Context, q *db.Queries, id ExchangeID) (db.GiftexchangeExchange, error) {
	exchange, err := q.LockExchange(ctx, uuid.UUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GiftexchangeExchange{}, ErrNotFound
	}
	if err != nil {
		return db.GiftexchangeExchange{}, fmt.Errorf("lock exchange %s: %w", id, err)
	}
	return exchange, nil
}

// closeOpenQuestions closes every open question of the exchange.
func (s *Service) closeOpenQuestions(ctx context.Context, q *db.Queries, exchange uuid.UUID) error {
	err := q.CloseOpenQuestions(ctx, db.CloseOpenQuestionsParams{
		ExchangeID: exchange,
		ClosedAt:   timestamptz(s.now()),
	})
	if err != nil {
		return fmt.Errorf("close questions of %s: %w", exchange, err)
	}
	return nil
}

// closeQuestionsOfPerson closes every open question the person asked or was asked.
func (s *Service) closeQuestionsOfPerson(
	ctx context.Context,
	q *db.Queries,
	exchange uuid.UUID,
	person uuid.UUID,
) error {
	err := q.CloseOpenQuestionsOfPerson(ctx, db.CloseOpenQuestionsOfPersonParams{
		ExchangeID: exchange,
		PersonID:   person,
		ClosedAt:   timestamptz(s.now()),
	})
	if err != nil {
		return fmt.Errorf("close questions of %s: %w", exchange, err)
	}
	return nil
}
