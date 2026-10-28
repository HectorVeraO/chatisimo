package giftexchange

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

// exchangeRow is an exchange with the joins every summary needs.
type exchangeRow struct {
	exchange            db.GiftexchangeExchange
	groupProvider       string
	groupSubject        string
	participantCount    int64
	callerIsParticipant bool
}

func summaryOf(row exchangeRow, caller PersonID) ExchangeSummary {
	exchange := row.exchange
	summary := ExchangeSummary{
		ID: ExchangeID(exchange.ID),
		Group: Group{
			ID:       GroupID(exchange.GroupID),
			Provider: Provider(row.groupProvider),
			Subject:  row.groupSubject,
		},
		Name:                exchange.Name,
		State:               State(exchange.State),
		Language:            Language(exchange.Language),
		DrawAt:              optionalTime(exchange.DrawAt),
		RevealAt:            optionalTime(exchange.RevealAt),
		ParticipantCount:    int(row.participantCount),
		CallerIsOrganizer:   PersonID(exchange.OrganizerID) == caller,
		CallerIsParticipant: row.callerIsParticipant,
	}

	return summary
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	instant := value.Time.UTC()
	return &instant
}

// buildSummaries turns a listing's rows into summaries in the order the query returned them.
func buildSummaries(rows []exchangeRow, caller PersonID) []ExchangeSummary {
	summaries := make([]ExchangeSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, summaryOf(row, caller))
	}
	return summaries
}

// viewOf builds the full view of one exchange for caller.
func viewOf(
	ctx context.Context,
	q *db.Queries,
	row exchangeRow,
	caller PersonID,
) (ExchangeView, error) {
	exchange := row.exchange

	participants, err := q.ListActiveParticipants(ctx, exchange.ID)
	if err != nil {
		return ExchangeView{}, fmt.Errorf("list participants of %s: %w", exchange.ID, err)
	}

	exclusionCount, err := q.CountExclusions(ctx, exchange.ID)
	if err != nil {
		return ExchangeView{}, fmt.Errorf("count exclusions of %s: %w", exchange.ID, err)
	}

	personIDs := []uuid.UUID{exchange.OrganizerID}
	for _, participant := range participants {
		personIDs = append(personIDs, participant.PersonID)
	}
	identities, err := identitiesOf(ctx, q, personIDs)
	if err != nil {
		return ExchangeView{}, fmt.Errorf("identities of %s: %w", exchange.ID, err)
	}

	isOrganizer := PersonID(exchange.OrganizerID) == caller
	view := ExchangeView{
		ExchangeSummary: summaryOf(row, caller),
		Organizer:       PersonRef{ID: PersonID(exchange.OrganizerID), Identities: identities[exchange.OrganizerID]},
		ExclusionCount:  int(exclusionCount),
		Rules:           ruleViews(exchange, int(exclusionCount), isOrganizer),
		CreatedAt:       exchange.CreatedAt.Time.UTC(),
		DrawnAt:         optionalTime(exchange.DrawnAt),
		RevealedAt:      optionalTime(exchange.RevealedAt),
	}
	view.ParticipantCount = len(participants)
	for _, participant := range participants {
		view.Participants = append(view.Participants, Participant{
			Person:   PersonRef{ID: PersonID(participant.PersonID), Identities: identities[participant.PersonID]},
			JoinedAt: participant.JoinedAt.Time.UTC(),
		})
	}

	return view, nil
}

func identitiesOf(
	ctx context.Context,
	q *db.Queries,
	personIDs []uuid.UUID,
) (map[uuid.UUID][]ExternalIdentity, error) {
	rows, err := q.ListIdentitiesOfPersons(ctx, personIDs)
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}

	byPerson := make(map[uuid.UUID][]ExternalIdentity, len(personIDs))
	for _, row := range rows {
		identity := ExternalIdentity{Provider: Provider(row.Provider), Subject: row.Subject}
		byPerson[row.PersonID] = append(byPerson[row.PersonID], identity)
	}

	return byPerson, nil
}

// ruleViews builds every rule of the registry for the exchange as the caller sees it.
func ruleViews(exchange db.GiftexchangeExchange, exclusionCount int, isOrganizer bool) []RuleView {
	descriptors := ruleDescriptors()
	views := make([]RuleView, 0, len(descriptors))
	settings := settingsOf(exchange)
	state := State(exchange.State)
	for _, descriptor := range descriptors {
		isEditableState := slices.Contains(descriptor.editableIn, state)
		view := RuleView{
			Name:         descriptor.name,
			Kind:         descriptor.kind,
			Value:        ruleValueText(settings, descriptor, exclusionCount),
			Default:      descriptor.defaultValue,
			Options:      ruleOptions(descriptor),
			Configurable: descriptor.configurable,
			EditableIn:   slices.Clone(descriptor.editableIn),
			EditableNow:  descriptor.configurable && isOrganizer && isEditableState,
		}
		views = append(views, view)
	}

	return views
}

func ruleOptions(descriptor ruleDescriptor) []string {
	if descriptor.kind == RuleKindBool {
		return []string{"on", "off"}
	}
	return slices.Clone(descriptor.options)
}

// ruleValueText returns the stored value of a configurable rule, or the fixed value.
func ruleValueText(settings ruleSettings, descriptor ruleDescriptor, exclusionCount int) string {
	switch descriptor.name {
	case RuleDrawAt:
		return instantText(settings.drawAt)
	case RuleRevealAt:
		return instantText(settings.revealAt)
	case RuleReminderLead:
		return formatLead(settings.reminderLead)
	case RuleDropoutPolicy:
		return settings.dropoutPolicy
	case RuleAvoidLastRound:
		return flagText(settings.avoidLastRound)
	case RuleExclusions:
		return strconv.Itoa(exclusionCount)
	case RuleQuestionsEnabled:
		return flagText(settings.questionsEnabled)
	case RuleLanguage:
		return settings.language
	default:
		return descriptor.defaultValue
	}
}

func flagText(flag bool) string {
	if flag {
		return "on"
	}
	return "off"
}
