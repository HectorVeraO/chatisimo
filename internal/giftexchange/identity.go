package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
)

const maxSubjectBytes = 128

// ResolveActor returns the actor for a provider identity, creating the person and the link on
// first sight (idempotent upsert). Clients call it on every interaction.
func (s *Service) ResolveActor(ctx context.Context, ref IdentityRef) (Actor, error) {
	person, err := s.resolvePerson(ctx, ref)
	if err != nil {
		return Actor{}, err
	}

	return Actor{person: person}, nil
}

// ResolvePerson returns the person ID for a provider identity named by the caller as a target
// (a user option such as "remove @x"), creating the person and link on first sight. It grants
// nothing: every operation still checks the target's role.
func (s *Service) ResolvePerson(ctx context.Context, ref IdentityRef) (PersonID, error) {
	person, err := s.resolvePerson(ctx, ref)
	return person, err
}

// ResolveGroup returns the group for a provider group, creating it on first sight.
func (s *Service) ResolveGroup(ctx context.Context, ref GroupRef) (GroupID, error) {
	validationErr := validateProviderRef("group", ref.Provider, ref.Subject)
	if validationErr != nil {
		return GroupID{}, validationErr
	}

	id, err := db.New(s.pool).UpsertGroup(ctx, db.UpsertGroupParams{
		ID:        uuid.NewV7(),
		Provider:  string(ref.Provider),
		Subject:   ref.Subject,
		CreatedAt: timestamptz(s.now()),
	})
	if err != nil {
		return GroupID{}, fmt.Errorf("resolve group: %w", err)
	}

	return GroupID(id), nil
}

func (s *Service) resolvePerson(ctx context.Context, ref IdentityRef) (PersonID, error) {
	validationErr := validateProviderRef("identity", ref.Provider, ref.Subject)
	if validationErr != nil {
		return PersonID{}, validationErr
	}

	params := db.GetPersonByIdentityParams{Provider: string(ref.Provider), Subject: ref.Subject}
	existing, err := db.New(s.pool).GetPersonByIdentity(ctx, params)
	if err == nil {
		return PersonID(existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PersonID{}, fmt.Errorf("resolve person: %w", err)
	}

	var resolved uuid.UUID
	txErr := s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		var createErr error
		resolved, createErr = s.createPerson(ctx, q, params)
		return createErr
	})
	if txErr != nil {
		return PersonID{}, fmt.Errorf("resolve person: %w", txErr)
	}

	return PersonID(resolved), nil
}

// createPerson inserts a person and the identity link. When a concurrent first sight wins the
// race, the link insert does nothing and the winner's person is returned.
func (s *Service) createPerson(
	ctx context.Context,
	q *db.Queries,
	params db.GetPersonByIdentityParams,
) (uuid.UUID, error) {
	created := timestamptz(s.now())
	candidate := uuid.NewV7()

	personErr := q.InsertPerson(ctx, db.InsertPersonParams{ID: candidate, CreatedAt: created})
	if personErr != nil {
		return uuid.UUID{}, fmt.Errorf("insert person: %w", personErr)
	}

	linkErr := q.InsertIdentity(ctx, db.InsertIdentityParams{
		Provider:  params.Provider,
		Subject:   params.Subject,
		PersonID:  candidate,
		CreatedAt: created,
	})
	if linkErr != nil {
		return uuid.UUID{}, fmt.Errorf("insert identity: %w", linkErr)
	}

	winner, err := q.GetPersonByIdentity(ctx, params)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("read identity: %w", err)
	}

	return winner, nil
}

// validateProviderRef reports every flaw of a provider reference together. The reasons are
// fixed phrases; the rejected subject never appears in them.
func validateProviderRef(kind string, provider Provider, subject string) error {
	var problems []error
	if provider != ProviderDiscord {
		problems = append(problems, &InputError{Field: kind + ".provider", Code: InputCodeUnknown, Reason: "is unknown"})
	}
	if subject == "" {
		problems = append(problems, &InputError{Field: kind + ".subject", Code: InputCodeEmpty, Reason: "is empty"})
	}
	if len(subject) > maxSubjectBytes {
		problems = append(problems, &InputError{Field: kind + ".subject", Code: InputCodeTooLong, Reason: "is too long", Max: maxSubjectBytes})
	}
	isText := utf8.ValidString(subject) && !strings.ContainsRune(subject, 0)
	if !isText {
		problems = append(problems, &InputError{Field: kind + ".subject", Code: InputCodeInvalidText, Reason: "is not valid text"})
	}
	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}
	return nil
}
