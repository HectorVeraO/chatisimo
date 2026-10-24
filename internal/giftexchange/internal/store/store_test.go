package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	platformdb "github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const (
	closeDeadline = 30 * time.Second
	fakeSubject   = "100000000000000001"
	otherSubject  = "100000000000000002"
	fakeGuild     = "200000000000000001"
	otherGuild    = "200000000000000002"
	providerKey   = "discord"
)

var shared *pgtest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	sources, err := migrationSources()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: sources})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	shared = database
	code := m.Run()

	closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
	defer cancel()

	err = database.Close(closeCtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return code
}

func migrationSources() ([]platformdb.Source, error) {
	platformSource, err := config.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("platform source: %w", err)
	}

	giftexchangeSource, err := giftexchange.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("giftexchange source: %w", err)
	}

	return []platformdb.Source{platformSource, giftexchangeSource, jobs.NewMigrationSource()}, nil
}

func now() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
}

func newPerson(t *testing.T, q *db.Queries) uuid.UUID {
	t.Helper()

	id := uuid.New()

	err := q.InsertPerson(t.Context(), db.InsertPersonParams{ID: id, CreatedAt: now()})
	if err != nil {
		t.Fatalf("InsertPerson: %v", err)
	}

	return id
}

func TestInsertIdentityThenGetPersonByIdentityReturnsThePerson(t *testing.T) {
	q := db.New(shared.Pool())
	person := newPerson(t, q)

	err := q.InsertIdentity(t.Context(), db.InsertIdentityParams{
		Provider: providerKey, Subject: fakeSubject, PersonID: person, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("InsertIdentity: %v", err)
	}

	got, err := q.GetPersonByIdentity(t.Context(),
		db.GetPersonByIdentityParams{Provider: providerKey, Subject: fakeSubject})
	if err != nil {
		t.Fatalf("GetPersonByIdentity: %v", err)
	}

	if got != person {
		t.Errorf("person = %v, want %v", got, person)
	}
}

func TestInsertIdentityOfKnownIdentityKeepsTheFirstPerson(t *testing.T) {
	q := db.New(shared.Pool())
	winner := newPerson(t, q)
	loser := newPerson(t, q)

	for _, person := range []uuid.UUID{winner, loser} {
		err := q.InsertIdentity(t.Context(), db.InsertIdentityParams{
			Provider: providerKey, Subject: otherSubject, PersonID: person, CreatedAt: now(),
		})
		if err != nil {
			t.Fatalf("InsertIdentity for %v: %v", person, err)
		}
	}

	got, err := q.GetPersonByIdentity(t.Context(),
		db.GetPersonByIdentityParams{Provider: providerKey, Subject: otherSubject})
	if err != nil {
		t.Fatalf("GetPersonByIdentity: %v", err)
	}

	if got != winner {
		t.Errorf("person = %v after a conflicting insert, want the first person %v", got, winner)
	}
}

func TestGetPersonByIdentityOfUnknownIdentityFails(t *testing.T) {
	q := db.New(shared.Pool())

	_, err := q.GetPersonByIdentity(t.Context(),
		db.GetPersonByIdentityParams{Provider: providerKey, Subject: "999999999999999999"})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetPersonByIdentity of an unknown identity error = %v, want pgx.ErrNoRows", err)
	}
}

func TestUpsertGroupTwiceReturnsTheSameGroup(t *testing.T) {
	q := db.New(shared.Pool())

	first, err := q.UpsertGroup(t.Context(), db.UpsertGroupParams{
		ID: uuid.New(), Provider: providerKey, Subject: fakeGuild, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("first UpsertGroup: %v", err)
	}

	second, err := q.UpsertGroup(t.Context(), db.UpsertGroupParams{
		ID: uuid.New(), Provider: providerKey, Subject: fakeGuild, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("second UpsertGroup: %v", err)
	}

	if first != second {
		t.Errorf("groups = %v and %v for one guild, want the same id", first, second)
	}
}

func TestUpsertGroupOfDifferentGuildsReturnsDifferentGroups(t *testing.T) {
	q := db.New(shared.Pool())

	a, err := q.UpsertGroup(t.Context(), db.UpsertGroupParams{
		ID: uuid.New(), Provider: providerKey, Subject: fakeGuild, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("UpsertGroup a: %v", err)
	}

	b, err := q.UpsertGroup(t.Context(), db.UpsertGroupParams{
		ID: uuid.New(), Provider: providerKey, Subject: otherGuild, CreatedAt: now(),
	})
	if err != nil {
		t.Fatalf("UpsertGroup b: %v", err)
	}

	if a == b {
		t.Errorf("two guilds share group %v, want distinct groups", a)
	}
}

type constraintCase struct {
	name     string
	provider string
	subject  string
	wantErr  bool
}

func constraintCases() []constraintCase {
	return []constraintCase{
		{name: "rejects an unknown provider", provider: "slack", subject: fakeSubject, wantErr: true},
		{name: "rejects an empty provider", provider: "", subject: fakeSubject, wantErr: true},
		{name: "rejects an empty subject", provider: providerKey, subject: "", wantErr: true},
		{name: "accepts a subject of one character", provider: providerKey, subject: "a"},
		{name: "accepts a subject of 128 characters", provider: providerKey, subject: strings.Repeat("b", 128)},
		{name: "rejects a subject of 129 characters", provider: providerKey, subject: strings.Repeat("c", 129), wantErr: true},
		{name: "accepts a multibyte subject of 128 characters", provider: providerKey, subject: strings.Repeat("ñ", 128)},
		{name: "accepts a four-byte subject of 128 characters", provider: providerKey, subject: strings.Repeat("\U0001F600", 128)},
		{name: "rejects a multibyte subject of 129 characters", provider: providerKey, subject: strings.Repeat("ñ", 129), wantErr: true},
	}
}

// matchesConstraintOutcome reports whether err is a CHECK violation
// (SQLSTATE 23514) when wantViolation is set, and nil otherwise.
func matchesConstraintOutcome(err error, wantViolation bool) bool {
	if !wantViolation {
		return err == nil
	}

	var pgErr *pgconn.PgError
	isPgError := errors.As(err, &pgErr)

	return isPgError && pgErr.Code == "23514"
}

func TestInsertIdentityEnforcesProviderAndSubjectBounds(t *testing.T) {
	q := db.New(shared.Pool())

	for _, tc := range constraintCases() {
		t.Run(tc.name, func(t *testing.T) {
			person := newPerson(t, q)

			err := q.InsertIdentity(t.Context(), db.InsertIdentityParams{
				Provider: tc.provider, Subject: tc.subject, PersonID: person, CreatedAt: now(),
			})
			if !matchesConstraintOutcome(err, tc.wantErr) {
				t.Errorf("InsertIdentity(%q, %d characters) error = %v, want check violation: %t",
					tc.provider, utf8.RuneCountInString(tc.subject), err, tc.wantErr)
			}
			if err != nil || tc.wantErr {
				return
			}

			got, err := q.GetPersonByIdentity(t.Context(),
				db.GetPersonByIdentityParams{Provider: tc.provider, Subject: tc.subject})
			if err != nil {
				t.Fatalf("GetPersonByIdentity after accepted insert: %v", err)
			}

			if got != person {
				t.Errorf("stored person = %v, want %v", got, person)
			}
		})
	}
}

func TestUpsertGroupEnforcesProviderAndSubjectBounds(t *testing.T) {
	q := db.New(shared.Pool())

	for _, tc := range constraintCases() {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()

			got, err := q.UpsertGroup(t.Context(), db.UpsertGroupParams{
				ID: id, Provider: tc.provider, Subject: tc.subject, CreatedAt: now(),
			})
			if !matchesConstraintOutcome(err, tc.wantErr) {
				t.Errorf("UpsertGroup(%q, %d characters) error = %v, want check violation: %t",
					tc.provider, utf8.RuneCountInString(tc.subject), err, tc.wantErr)
			}

			if err == nil && got != id {
				t.Errorf("stored group = %v, want %v", got, id)
			}
		})
	}
}
