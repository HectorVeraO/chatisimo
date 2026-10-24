package giftexchange_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestCreateExchangeReturnsAnOpenExchangeOrganizedByTheCaller(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	view, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "  Winter exchange  ",
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}

	if view.Name != "Winter exchange" {
		t.Errorf("name = %q, want trimmed name", view.Name)
	}
	if view.State != giftexchange.StateOpen {
		t.Errorf("state = %v, want open", view.State)
	}
	if view.Organizer.ID != organizer.id {
		t.Errorf("organizer = %v, want the caller", view.Organizer.ID)
	}
	if view.Group.ID != group {
		t.Errorf("group = %v, want %v", view.Group.ID, group)
	}
	if !view.CallerIsOrganizer || view.CallerIsParticipant {
		t.Errorf("caller flags = organizer %v participant %v, want true false",
			view.CallerIsOrganizer, view.CallerIsParticipant)
	}
	if view.ParticipantCount != 0 || len(view.Participants) != 0 {
		t.Errorf("participants = %d/%d, want none: the organizer must join", view.ParticipantCount, len(view.Participants))
	}
	if view.DrawAt != nil || view.RevealAt != nil {
		t.Errorf("draw_at/reveal_at = %v/%v, want unset", view.DrawAt, view.RevealAt)
	}
	wantIdentity := giftexchange.ExternalIdentity{Provider: giftexchange.ProviderDiscord, Subject: organizer.subject}
	if len(view.Organizer.Identities) != 1 || view.Organizer.Identities[0] != wantIdentity {
		t.Errorf("organizer identities = %v, want the caller's identity", view.Organizer.Identities)
	}
}

func TestCreateExchangeNameBoundaries(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"rejects an empty name", "", true},
		{"accepts a one rune name", "A", false},
		{"accepts a 100 rune name", strings.Repeat("ñ", 100), false},
		{"rejects a 101 rune name", strings.Repeat("ñ", 101), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.CreateExchange(t.Context(), organizer.actor,
				giftexchange.CreateExchangeRequest{Group: group, Name: tc.input})

			if tc.wantErr {
				if !errors.Is(err, giftexchange.ErrInvalidInput) {
					t.Errorf("err = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestCreateExchangeReportsNameAndInitialRulesTogether(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "",
		Rules: []giftexchange.RuleChange{{Name: giftexchange.RuleReminderLead, Raw: "2d"}},
	})

	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	fields := inputFields(err)
	if !slices.Contains(fields, "name") || !slices.Contains(fields, "rules") {
		t.Errorf("fields = %v, want both name and rules", fields)
	}
}

func TestCreateExchangeRejectsInitialRulesEvenWithAValidName(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "Winter exchange",
		Rules: []giftexchange.RuleChange{{Name: giftexchange.RuleDropoutPolicy, Raw: "redraw"}},
	})

	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !slices.Equal(inputFields(err), []string{"rules"}) {
		t.Errorf("fields = %v, want [rules]", inputFields(err))
	}
}

func TestCreateExchangeInUnknownGroupIsNotFound(t *testing.T) {
	h := newService(t)
	organizer := h.newPerson(t)
	unknown := newUnknownGroup(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor,
		giftexchange.CreateExchangeRequest{Group: unknown, Name: "Winter exchange"})

	if !errors.Is(err, giftexchange.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// newUnknownGroup returns a well-formed group ID that no row has.
func newUnknownGroup(t *testing.T) giftexchange.GroupID {
	t.Helper()

	id, err := giftexchange.ParseExchangeID("018f0000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("ParseExchangeID: %v", err)
	}

	return giftexchange.GroupID(id)
}

func TestExchangeViewListsRulesWithDefaultsAndCallerEditability(t *testing.T) {
	s := newScene(t)
	other := s.h.newPerson(t)

	asOrganizer, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange as organizer: %v", err)
	}
	asMember, err := s.h.svc.Exchange(t.Context(), other.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange as member: %v", err)
	}

	wantNames := []giftexchange.RuleName{
		giftexchange.RuleDrawAt, giftexchange.RuleRevealAt, giftexchange.RuleReminderLead,
		giftexchange.RuleDropoutPolicy, giftexchange.RuleAvoidLastRound, giftexchange.RuleExclusions,
		giftexchange.RuleQuestionsEnabled, giftexchange.RuleMinParticipants, giftexchange.RuleMaxParticipants,
		giftexchange.RuleNoSelfAssignment, giftexchange.RuleRedrawBelow, giftexchange.RuleQuestionMaxLength,
		giftexchange.RuleQuestionsPerDay,
	}
	var gotNames []giftexchange.RuleName
	for _, rule := range asOrganizer.Rules {
		gotNames = append(gotNames, rule.Name)
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("rule names = %v, want %v", gotNames, wantNames)
	}

	wantValues := map[giftexchange.RuleName]string{
		giftexchange.RuleDrawAt: "unset", giftexchange.RuleRevealAt: "unset",
		giftexchange.RuleReminderLead: "1d", giftexchange.RuleDropoutPolicy: "patch",
		giftexchange.RuleAvoidLastRound: "off", giftexchange.RuleExclusions: "0",
		giftexchange.RuleQuestionsEnabled: "on", giftexchange.RuleMinParticipants: "3",
		giftexchange.RuleMaxParticipants: "100", giftexchange.RuleNoSelfAssignment: "on",
		giftexchange.RuleRedrawBelow: "4", giftexchange.RuleQuestionMaxLength: "1000",
		giftexchange.RuleQuestionsPerDay: "20",
	}
	for _, rule := range asOrganizer.Rules {
		if rule.Value != wantValues[rule.Name] || rule.Default != wantValues[rule.Name] {
			t.Errorf("rule %s value/default = %q/%q, want %q", rule.Name, rule.Value, rule.Default, wantValues[rule.Name])
		}
		if rule.EditableNow != rule.Configurable {
			t.Errorf("organizer rule %s EditableNow = %v, want %v", rule.Name, rule.EditableNow, rule.Configurable)
		}
	}
	for _, rule := range asMember.Rules {
		if rule.EditableNow {
			t.Errorf("member rule %s is editable now, want false", rule.Name)
		}
	}
}

func TestExchangeViewShowsParticipantsAndTheCallersRole(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	view, err := s.h.svc.Exchange(t.Context(), joiner.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if view.ParticipantCount != 1 || len(view.Participants) != 1 {
		t.Fatalf("participants = %d/%d, want 1", view.ParticipantCount, len(view.Participants))
	}
	if view.Participants[0].Person.ID != joiner.id {
		t.Errorf("participant = %v, want the joiner", view.Participants[0].Person.ID)
	}
	if view.CallerIsOrganizer || !view.CallerIsParticipant {
		t.Errorf("caller flags = organizer %v participant %v, want false true",
			view.CallerIsOrganizer, view.CallerIsParticipant)
	}
}

func TestExchangesForGroupListsNewestFirstWithoutCancelled(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)
	member := h.newPerson(t)

	first := h.newExchange(t, organizer, group)
	h.clock.Advance(time.Minute)
	second := h.newExchange(t, organizer, group)
	h.clock.Advance(time.Minute)
	cancelled := h.newExchange(t, organizer, group)
	if err := h.svc.Cancel(t.Context(), organizer.actor, group, cancelled.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	h.join(t, member, group, first.ID)

	got, err := h.svc.ExchangesFor(t.Context(), member.actor, &group)
	if err != nil {
		t.Fatalf("ExchangesFor: %v", err)
	}

	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Fatalf("listed %d exchanges, want [second, first] newest first", len(got))
	}
	if got[0].CallerIsParticipant || !got[1].CallerIsParticipant {
		t.Errorf("participant flags = %v %v, want false true", got[0].CallerIsParticipant, got[1].CallerIsParticipant)
	}
	if got[1].ParticipantCount != 1 {
		t.Errorf("participant count = %d, want 1", got[1].ParticipantCount)
	}
}

func TestExchangesForGroupExcludesOtherGroups(t *testing.T) {
	h := newService(t)
	organizer := h.newPerson(t)
	home := h.newGroup(t)
	away := h.newGroup(t)
	h.newExchange(t, organizer, away)
	mine := h.newExchange(t, organizer, home)

	got, err := h.svc.ExchangesFor(t.Context(), organizer.actor, &home)
	if err != nil {
		t.Fatalf("ExchangesFor: %v", err)
	}

	if len(got) != 1 || got[0].ID != mine.ID {
		t.Errorf("listed %d exchanges, want only the exchange of the group", len(got))
	}
}

func TestExchangesForWithoutGroupListsOnlyWhatTheCallerOrganizesOrJoined(t *testing.T) {
	h := newService(t)
	caller := h.newPerson(t)
	stranger := h.newPerson(t)
	groupA := h.newGroup(t)
	groupB := h.newGroup(t)

	organized := h.newExchange(t, caller, groupA)
	h.clock.Advance(time.Minute)
	joined := h.newExchange(t, stranger, groupB)
	h.join(t, caller, groupB, joined.ID)
	left := h.newExchange(t, stranger, groupB)
	h.join(t, caller, groupB, left.ID)
	if _, err := h.svc.Leave(t.Context(), caller.actor, groupB, left.ID); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	unrelated := h.newExchange(t, stranger, groupA)
	cancelled := h.newExchange(t, caller, groupA)
	if err := h.svc.Cancel(t.Context(), caller.actor, groupA, cancelled.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := h.svc.ExchangesFor(t.Context(), caller.actor, nil)
	if err != nil {
		t.Fatalf("ExchangesFor: %v", err)
	}

	var ids []giftexchange.ExchangeID
	for _, summary := range got {
		ids = append(ids, summary.ID)
	}
	want := []giftexchange.ExchangeID{joined.ID, organized.ID}
	if !slices.Equal(ids, want) {
		t.Errorf("listed %v, want organized and joined only (not left %v, unrelated %v, cancelled %v)",
			ids, left.ID, unrelated.ID, cancelled.ID)
	}
}

func TestRenameChangesTheNameForTheOrganizer(t *testing.T) {
	s := newScene(t)

	err := s.h.svc.Rename(t.Context(), s.organizer.actor, s.group, s.exchange, "  Spring exchange ")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.Name != "Spring exchange" {
		t.Errorf("name = %q, want %q", view.Name, "Spring exchange")
	}
}

func TestRenameRejectsInvalidNamesAndKeepsTheOldOne(t *testing.T) {
	s := newScene(t)

	for _, name := range []string{"", "   ", strings.Repeat("x", 101)} {
		err := s.h.svc.Rename(t.Context(), s.organizer.actor, s.group, s.exchange, name)
		if !errors.Is(err, giftexchange.ErrInvalidInput) {
			t.Errorf("Rename(%d runes) err = %v, want ErrInvalidInput", len(name), err)
		}
	}

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.Name != "Winter exchange" {
		t.Errorf("name = %q, want it unchanged", view.Name)
	}
}

func TestJoinTwiceReportsAlreadyParticipant(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	err := s.h.svc.Join(t.Context(), joiner.actor, s.group, s.exchange)

	if !errors.Is(err, giftexchange.ErrAlreadyParticipant) {
		t.Errorf("err = %v, want ErrAlreadyParticipant", err)
	}
}

func TestOrganizerJoinsLikeAnyoneElse(t *testing.T) {
	s := newScene(t)

	s.h.join(t, s.organizer, s.group, s.exchange)

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if !view.CallerIsOrganizer || !view.CallerIsParticipant || view.ParticipantCount != 1 {
		t.Errorf("view = organizer %v participant %v count %d, want true true 1",
			view.CallerIsOrganizer, view.CallerIsParticipant, view.ParticipantCount)
	}
}

func TestJoinAcceptsTheHundredthAndRefusesTheHundredAndFirst(t *testing.T) {
	s := newScene(t)

	for range giftexchange.MaxParticipants {
		s.h.join(t, s.h.newPerson(t), s.group, s.exchange)
	}
	late := s.h.newPerson(t)

	err := s.h.svc.Join(t.Context(), late.actor, s.group, s.exchange)

	if !errors.Is(err, giftexchange.ErrExchangeFull) {
		t.Fatalf("101st join err = %v, want ErrExchangeFull", err)
	}
	view, err := s.h.svc.Exchange(t.Context(), late.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.ParticipantCount != giftexchange.MaxParticipants || view.CallerIsParticipant {
		t.Errorf("count/caller = %d/%v, want 100/false", view.ParticipantCount, view.CallerIsParticipant)
	}
}

func TestLeaveFreesASeatInAFullExchange(t *testing.T) {
	s := newScene(t)
	first := s.h.newPerson(t)
	s.h.join(t, first, s.group, s.exchange)
	for range giftexchange.MaxParticipants - 1 {
		s.h.join(t, s.h.newPerson(t), s.group, s.exchange)
	}
	late := s.h.newPerson(t)

	if _, err := s.h.svc.Leave(t.Context(), first.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	err := s.h.svc.Join(t.Context(), late.actor, s.group, s.exchange)
	if err != nil {
		t.Errorf("Join after a seat freed: %v", err)
	}
}

func TestLeaveRemovesTheParticipantAndTheyCanRejoin(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	outcome, err := s.h.svc.Leave(t.Context(), joiner.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if outcome != giftexchange.OutcomeRemoved {
		t.Errorf("outcome = %v, want removed", outcome)
	}
	afterLeave, err := s.h.svc.Exchange(t.Context(), joiner.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if afterLeave.ParticipantCount != 0 || afterLeave.CallerIsParticipant {
		t.Errorf("after leave count/caller = %d/%v, want 0/false", afterLeave.ParticipantCount, afterLeave.CallerIsParticipant)
	}

	s.h.clock.Advance(time.Minute)
	rejoinErr := s.h.svc.Join(t.Context(), joiner.actor, s.group, s.exchange)
	if rejoinErr != nil {
		t.Fatalf("rejoin: %v", rejoinErr)
	}
	afterRejoin, err := s.h.svc.Exchange(t.Context(), joiner.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if afterRejoin.ParticipantCount != 1 || !afterRejoin.CallerIsParticipant {
		t.Errorf("after rejoin count/caller = %d/%v, want 1/true", afterRejoin.ParticipantCount, afterRejoin.CallerIsParticipant)
	}
	if !afterRejoin.Participants[0].JoinedAt.Equal(s.h.clock.Now()) {
		t.Errorf("joined at = %v, want the rejoin time %v", afterRejoin.Participants[0].JoinedAt, s.h.clock.Now())
	}
}

func TestLeaveTwiceReportsNotParticipant(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)
	if _, err := s.h.svc.Leave(t.Context(), joiner.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	_, err := s.h.svc.Leave(t.Context(), joiner.actor, s.group, s.exchange)

	if !errors.Is(err, giftexchange.ErrNotParticipant) {
		t.Errorf("err = %v, want ErrNotParticipant", err)
	}
}

func TestRemoveParticipantRemovesTheTargetForTheOrganizer(t *testing.T) {
	s := newScene(t)
	target := s.h.newPerson(t)
	s.h.join(t, target, s.group, s.exchange)

	outcome, err := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, target.id)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}
	if outcome != giftexchange.OutcomeRemoved {
		t.Errorf("outcome = %v, want removed", outcome)
	}
	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.ParticipantCount != 0 {
		t.Errorf("participant count = %d, want 0", view.ParticipantCount)
	}
}

func TestRemoveParticipantOfSomeoneNotActiveReportsNotParticipant(t *testing.T) {
	s := newScene(t)
	never := s.h.newPerson(t)
	left := s.h.newPerson(t)
	s.h.join(t, left, s.group, s.exchange)
	if _, err := s.h.svc.Leave(t.Context(), left.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	for name, target := range map[string]person{"never joined": never, "already left": left} {
		_, err := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, target.id)
		if !errors.Is(err, giftexchange.ErrNotParticipant) {
			t.Errorf("%s: err = %v, want ErrNotParticipant", name, err)
		}
	}
}

func TestCancelEndsTheExchangeAndNotifiesEveryActiveParticipantButTheOrganizer(t *testing.T) {
	s := newScene(t)
	first := s.h.newPerson(t)
	second := s.h.newPerson(t)
	dropout := s.h.newPerson(t)
	s.h.join(t, s.organizer, s.group, s.exchange)
	s.h.join(t, first, s.group, s.exchange)
	s.h.join(t, second, s.group, s.exchange)
	s.h.join(t, dropout, s.group, s.exchange)
	if _, err := s.h.svc.Leave(t.Context(), dropout.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	jobsBefore := countJobs(t, s.h)

	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.State != giftexchange.StateCancelled {
		t.Errorf("state = %v, want cancelled", view.State)
	}

	rows := notificationRows(t, s.exchange)
	wantAddressees := []string{first.id.String(), second.id.String()}
	slices.Sort(wantAddressees)
	var gotAddressees []string
	for _, row := range rows {
		if row.kind != string(giftexchange.KindCancelled) {
			t.Errorf("notification kind = %q, want cancelled", row.kind)
		}
		gotAddressees = append(gotAddressees, row.person)
	}
	slices.Sort(gotAddressees)
	if !slices.Equal(gotAddressees, wantAddressees) {
		t.Errorf("addressees = %v, want the two active participants only", gotAddressees)
	}

	newJobs := jobsOf(t, s.h)[jobsBefore:]
	if len(newJobs) != len(rows) {
		t.Fatalf("cancel queued %d jobs, want %d", len(newJobs), len(rows))
	}
	var jobIDs []string
	for _, job := range newJobs {
		var args map[string]string
		if err := json.Unmarshal([]byte(job.args), &args); err != nil {
			t.Fatalf("job args %q: %v", job.args, err)
		}
		if len(args) != 1 {
			t.Errorf("job args = %v, want the notification ID only", args)
		}
		jobIDs = append(jobIDs, args["notification_id"])
	}
	var rowIDs []string
	for _, row := range rows {
		rowIDs = append(rowIDs, row.id)
	}
	slices.Sort(jobIDs)
	slices.Sort(rowIDs)
	if !slices.Equal(jobIDs, rowIDs) {
		t.Errorf("job notification IDs = %v, want %v", jobIDs, rowIDs)
	}
}

// notificationRow is one outbox row.
type notificationRow struct {
	id       string
	kind     string
	person   string
	round    int
	question *string
	reason   *string
}

func notificationRows(t *testing.T, x giftexchange.ExchangeID) []notificationRow {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT id::text, kind, person_id::text, round, question_id::text, reason "+
			"FROM giftexchange.notifications WHERE exchange_id = $1 ORDER BY id", x.String())
	if err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	defer rows.Close()

	var found []notificationRow
	for rows.Next() {
		var row notificationRow
		if scanErr := rows.Scan(&row.id, &row.kind, &row.person, &row.round, &row.question, &row.reason); scanErr != nil {
			t.Fatalf("scan notification: %v", scanErr)
		}
		if row.question != nil || row.reason != nil {
			t.Errorf("notification %s holds a question or reason, want IDs and kind only", row.id)
		}
		found = append(found, row)
	}
	if rows.Err() != nil {
		t.Fatalf("read notifications rows: %v", rows.Err())
	}

	return found
}

func TestCancelTwiceReportsStateErrorAndQueuesNothingMore(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)

	var stateErr *giftexchange.StateError
	if !errors.As(err, &stateErr) || stateErr.State != giftexchange.StateCancelled {
		t.Fatalf("err = %v, want *StateError in cancelled", err)
	}
	if !errors.Is(err, giftexchange.ErrInvalidState) {
		t.Errorf("err = %v, want ErrInvalidState", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestOperationsOnACancelledExchangeReportItsState(t *testing.T) {
	s := newScene(t)
	member := s.h.newPerson(t)
	joined := s.h.newPerson(t)
	s.h.join(t, joined, s.group, s.exchange)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	tests := []struct {
		name string
		call func() error
	}{
		{"rename", func() error {
			return s.h.svc.Rename(t.Context(), s.organizer.actor, s.group, s.exchange, "New name")
		}},
		{"join", func() error { return s.h.svc.Join(t.Context(), member.actor, s.group, s.exchange) }},
		{"leave", func() error {
			return errOf(s.h.svc.Leave(t.Context(), joined.actor, s.group, s.exchange))
		}},
		{"remove participant", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, joined.id))
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()

			var stateErr *giftexchange.StateError
			if !errors.As(err, &stateErr) || stateErr.State != giftexchange.StateCancelled {
				t.Errorf("err = %v, want *StateError in cancelled", err)
			}
		})
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestCancelledExchangesStayReadableByID(t *testing.T) {
	s := newScene(t)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.State != giftexchange.StateCancelled {
		t.Errorf("State = %v, want cancelled", view.State)
	}
	if len(view.Rules) == 0 {
		t.Error("Rules is empty, want the rules of the cancelled exchange")
	}
	for _, rule := range view.Rules {
		if rule.EditableNow {
			t.Errorf("rule %s is editable in a cancelled exchange", rule.Name)
		}
	}
}

func TestConcurrentJoinsOfOnePersonAdmitExactlyOne(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)

	const attempts = 6
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.h.svc.Join(t.Context(), joiner.actor, s.group, s.exchange)
		}()
	}
	wg.Wait()

	var succeeded, duplicates int
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, giftexchange.ErrAlreadyParticipant):
			duplicates++
		default:
			t.Errorf("unexpected error %v", err)
		}
	}
	if succeeded != 1 || duplicates != attempts-1 {
		t.Errorf("succeeded/duplicates = %d/%d, want 1/%d", succeeded, duplicates, attempts-1)
	}
}

func TestConcurrentCancelsNotifyOnlyOnce(t *testing.T) {
	s := newScene(t)
	s.h.join(t, s.h.newPerson(t), s.group, s.exchange)

	const attempts = 4
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
		}()
	}
	wg.Wait()

	var succeeded int
	for _, err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, giftexchange.ErrInvalidState) {
			t.Errorf("unexpected error %v", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("%d cancels succeeded, want 1", succeeded)
	}
	if got := len(notificationRows(t, s.exchange)); got != 1 {
		t.Errorf("%d notifications, want 1", got)
	}
	if got := countJobs(t, s.h); got != 1 {
		t.Errorf("%d jobs, want 1", got)
	}
}
