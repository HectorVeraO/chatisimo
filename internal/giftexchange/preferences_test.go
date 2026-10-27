package giftexchange_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestPreferencesLifecycleInOpenState(t *testing.T) {
	s := newScene(t)
	ctx := t.Context()
	alice := s.h.newPerson(t)
	s.h.join(t, alice, s.group, s.exchange)

	initial, err := s.h.svc.Preferences(ctx, alice.actor, s.exchange)
	if err != nil {
		t.Fatalf("initial Preferences: %v", err)
	}
	if !initial.IsEmpty() {
		t.Fatalf("initial Preferences = %+v, want empty", initial)
	}

	firstInput := giftexchange.Preferences{
		Likes:    "  Pour-over coffee\nand sci-fi novels  ",
		Dislikes: "  Scented candles ",
		Details:  " T-shirt size M ",
	}
	wantFirst := giftexchange.Preferences{
		Likes:    "Pour-over coffee\nand sci-fi novels",
		Dislikes: "Scented candles",
		Details:  "T-shirt size M",
	}

	saved, err := s.h.svc.SetPreferences(ctx, alice.actor, s.exchange, firstInput)
	if err != nil {
		t.Fatalf("SetPreferences in open: %v", err)
	}
	if saved != wantFirst {
		t.Errorf("SetPreferences returned %#v, want %#v", saved, wantFirst)
	}

	readBack, err := s.h.svc.Preferences(ctx, alice.actor, s.exchange)
	if err != nil {
		t.Fatalf("Preferences after set: %v", err)
	}
	if readBack != wantFirst {
		t.Errorf("Preferences readBack = %#v, want %#v", readBack, wantFirst)
	}

	updatedInput := giftexchange.Preferences{
		Likes:   "Board games",
		Details: "No allergies",
	}
	updated, err := s.h.svc.SetPreferences(ctx, alice.actor, s.exchange, updatedInput)
	if err != nil {
		t.Fatalf("SetPreferences update in open: %v", err)
	}
	if updated != updatedInput {
		t.Errorf("updated Preferences = %#v, want %#v", updated, updatedInput)
	}

	cleared, err := s.h.svc.SetPreferences(ctx, alice.actor, s.exchange, giftexchange.Preferences{})
	if err != nil {
		t.Fatalf("SetPreferences clear in open: %v", err)
	}
	if !cleared.IsEmpty() {
		t.Errorf("cleared Preferences = %#v, want empty", cleared)
	}

	afterClear, err := s.h.svc.Preferences(ctx, alice.actor, s.exchange)
	if err != nil {
		t.Fatalf("Preferences after clear: %v", err)
	}
	if !afterClear.IsEmpty() {
		t.Errorf("Preferences after clear = %#v, want empty", afterClear)
	}

	if rows := notificationRows(t, s.exchange); len(rows) != 0 {
		t.Errorf("notifications queued in open = %+v, want none", rows)
	}
}

func TestSetPreferencesInDrawnQueuesUpdateNoticeOnlyWhenChanged(t *testing.T) {
	s := cycleDrawn(t, 4)
	ctx := t.Context()
	giver := s.people[0]
	recipient := s.people[1]

	prefs := giftexchange.Preferences{
		Likes:    "Specialty coffee, vinyl records",
		Dislikes: "Perfume",
		Details:  "Hoodie size L",
	}

	saved, err := s.h.svc.SetPreferences(ctx, recipient.actor, s.exchange, prefs)
	if err != nil {
		t.Fatalf("SetPreferences in drawn: %v", err)
	}
	if saved != prefs {
		t.Fatalf("saved = %#v, want %#v", saved, prefs)
	}

	assignment, err := s.h.svc.MyAssignment(ctx, giver.actor, s.exchange)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	if assignment.Preferences != prefs {
		t.Errorf("giver's MyAssignment.Preferences = %#v, want %#v", assignment.Preferences, prefs)
	}

	queued := preferencesUpdatedRows(t, s.exchange)
	if len(queued) != 1 || queued[0].person != giver.id.String() {
		t.Fatalf("preferences_updated rows after first set = %+v, want 1 row for giver %s", queued, giver.id)
	}

	// Setting identical preferences (after trimming) must not queue a duplicate notification.
	_, err = s.h.svc.SetPreferences(ctx, recipient.actor, s.exchange, giftexchange.Preferences{
		Likes:    "  " + prefs.Likes + "  ",
		Dislikes: prefs.Dislikes,
		Details:  prefs.Details,
	})
	if err != nil {
		t.Fatalf("no-op SetPreferences: %v", err)
	}
	if got := len(preferencesUpdatedRows(t, s.exchange)); got != 1 {
		t.Fatalf("preferences_updated rows after no-op = %d, want 1", got)
	}

	// Clearing preferences in drawn queues a second notification for the giver.
	cleared, err := s.h.svc.SetPreferences(ctx, recipient.actor, s.exchange, giftexchange.Preferences{})
	if err != nil {
		t.Fatalf("clear SetPreferences in drawn: %v", err)
	}
	if !cleared.IsEmpty() {
		t.Errorf("cleared = %#v, want empty", cleared)
	}
	if got := len(preferencesUpdatedRows(t, s.exchange)); got != 2 {
		t.Fatalf("preferences_updated rows after clear = %d, want 2", got)
	}

	// Setting new preferences before the runner delivers the notices means the delivered notice
	// carries the latest preferences of the recipient.
	finalPrefs := giftexchange.Preferences{Likes: "Watercolor supplies"}
	_, err = s.h.svc.SetPreferences(ctx, recipient.actor, s.exchange, finalPrefs)
	if err != nil {
		t.Fatalf("third SetPreferences: %v", err)
	}

	s.h.startRunner(t)
	waitForFinished(t, s.exchange, len(s.people)+3)

	updates := receivedOfKind(s.h.notifier.Received(), giftexchange.KindPreferencesUpdated)
	if len(updates) != 3 {
		t.Fatalf("delivered KindPreferencesUpdated = %d, want 3", len(updates))
	}
	for _, notice := range updates {
		if notice.To.ID != giver.id {
			t.Errorf("notice addressed to %s, want giver %s", notice.To.ID, giver.id)
		}
		if notice.Counterpart == nil || notice.Counterpart.ID != recipient.id {
			t.Errorf("notice counterpart = %+v, want recipient %s", notice.Counterpart, recipient.id)
		}
		if notice.Preferences != finalPrefs {
			t.Errorf("notice preferences = %#v, want %#v", notice.Preferences, finalPrefs)
		}
	}
}

func TestDrawPatchAndRedrawNotificationsCarryRecipientPreferences(t *testing.T) {
	t.Run("draw and dropout patch carry preferences to the assigned giver", func(t *testing.T) {
		s, assigned := drawnWhere(t, 5, func(s drawScene) {
			for i, p := range s.people {
				prefs := giftexchange.Preferences{
					Likes:    "Likes of participant " + string(rune('A'+i)),
					Dislikes: "Dislikes of participant " + string(rune('A'+i)),
					Details:  "Details of participant " + string(rune('A'+i)),
				}
				_, err := s.h.svc.SetPreferences(t.Context(), p.actor, s.exchange, prefs)
				if err != nil {
					t.Fatalf("SetPreferences in open: %v", err)
				}
			}
		}, func(s drawScene, a assignmentMap) bool {
			return slices.ContainsFunc(s.people, func(p person) bool { return giverOf(a, p.id) != a[p.id] })
		})

		dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
			return giver != recipient
		})
		giver := personWithID(s.people, giverOf(assigned, dropout.id))
		newRecipient := personWithID(s.people, assigned[dropout.id])

		wantNewPrefs, err := s.h.svc.Preferences(t.Context(), newRecipient.actor, s.exchange)
		if err != nil {
			t.Fatalf("Preferences of newRecipient: %v", err)
		}

		outcome := s.leave(t, dropout)
		if outcome != giftexchange.OutcomePatched {
			t.Fatalf("Leave outcome = %v, want patched", outcome)
		}

		patchedAssignment, err := s.h.svc.MyAssignment(t.Context(), giver.actor, s.exchange)
		if err != nil {
			t.Fatalf("MyAssignment after patch: %v", err)
		}
		if patchedAssignment.Recipient.ID != newRecipient.id {
			t.Fatalf("patched recipient = %s, want %s", patchedAssignment.Recipient.ID, newRecipient.id)
		}
		if patchedAssignment.Preferences != wantNewPrefs {
			t.Errorf("patched MyAssignment.Preferences = %#v, want %#v", patchedAssignment.Preferences, wantNewPrefs)
		}

		s.h.startRunner(t)
		waitForFinished(t, s.exchange, len(s.people)+1)

		changed := receivedOfKind(s.h.notifier.Received(), giftexchange.KindAssignmentChanged)
		if len(changed) != 1 {
			t.Fatalf("delivered KindAssignmentChanged = %d, want 1", len(changed))
		}
		if changed[0].To.ID != giver.id || changed[0].Counterpart == nil || changed[0].Counterpart.ID != newRecipient.id {
			t.Errorf("KindAssignmentChanged pair = (%s -> %+v), want (%s -> %s)",
				changed[0].To.ID, changed[0].Counterpart, giver.id, newRecipient.id)
		}
		if changed[0].Preferences != wantNewPrefs {
			t.Errorf("KindAssignmentChanged.Preferences = %#v, want %#v", changed[0].Preferences, wantNewPrefs)
		}

		// Updating newRecipient's preferences after the patch notifies the new giver.
		afterPatchPrefs := giftexchange.Preferences{Likes: "Updated after patch"}
		_, err = s.h.svc.SetPreferences(t.Context(), newRecipient.actor, s.exchange, afterPatchPrefs)
		if err != nil {
			t.Fatalf("SetPreferences after patch: %v", err)
		}
		waitForFinished(t, s.exchange, len(s.people)+2)

		updates := receivedOfKind(s.h.notifier.Received(), giftexchange.KindPreferencesUpdated)
		if len(updates) != 1 || updates[0].To.ID != giver.id || updates[0].Preferences != afterPatchPrefs {
			t.Errorf("KindPreferencesUpdated after patch = %+v, want addressed to %s with %#v",
				updates, giver.id, afterPatchPrefs)
		}
	})

	t.Run("dropout redraw carries existing preferences to each new giver", func(t *testing.T) {
		s := newDrawScene(t, 4)
		prefsByPerson := map[giftexchange.PersonID]giftexchange.Preferences{}
		for i, p := range s.people {
			prefs := giftexchange.Preferences{
				Likes:   "Redraw likes " + string(rune('1'+i)),
				Details: "Redraw details " + string(rune('1'+i)),
			}
			_, err := s.h.svc.SetPreferences(t.Context(), p.actor, s.exchange, prefs)
			if err != nil {
				t.Fatalf("SetPreferences: %v", err)
			}
			prefsByPerson[p.id] = prefs
		}
		s.mustDraw(t)

		// With 4 participants (RedrawBelow = 4), a dropout triggers a redraw among the 3 survivors.
		outcome := s.leave(t, s.people[0])
		if outcome != giftexchange.OutcomeRedrawn {
			t.Fatalf("Leave outcome = %v, want redrawn", outcome)
		}

		s.h.startRunner(t)
		waitForFinished(t, s.exchange, 4+3)

		redrawn := receivedOfKind(s.h.notifier.Received(), giftexchange.KindRedrawn)
		if len(redrawn) != 3 {
			t.Fatalf("delivered KindRedrawn = %d, want 3", len(redrawn))
		}
		for _, notice := range redrawn {
			if notice.Counterpart == nil {
				t.Fatalf("KindRedrawn notice %+v has nil Counterpart", notice)
			}
			wantPrefs := prefsByPerson[notice.Counterpart.ID]
			if notice.Preferences != wantPrefs {
				t.Errorf("KindRedrawn to %s for recipient %s: Preferences = %#v, want %#v",
					notice.To.ID, notice.Counterpart.ID, notice.Preferences, wantPrefs)
			}
			giver := personWithID(s.people, notice.To.ID)
			assign, err := s.h.svc.MyAssignment(t.Context(), giver.actor, s.exchange)
			if err != nil {
				t.Fatalf("MyAssignment for %s: %v", giver.id, err)
			}
			if assign.Preferences != wantPrefs {
				t.Errorf("MyAssignment.Preferences after redraw = %#v, want %#v", assign.Preferences, wantPrefs)
			}
		}
	})
}

func TestSetPreferencesRejectsRevealedAndCancelledExchanges(t *testing.T) {
	t.Run("revealed exchange rejects SetPreferences", func(t *testing.T) {
		s := cycleDrawn(t, 3)
		initial := giftexchange.Preferences{Likes: "Books"}
		_, err := s.h.svc.SetPreferences(t.Context(), s.people[0].actor, s.exchange, initial)
		if err != nil {
			t.Fatalf("SetPreferences before reveal: %v", err)
		}
		s.mustReveal(t)
		before := takeSnapshot(t, s.h, s.exchange)

		_, err = s.h.svc.SetPreferences(
			t.Context(), s.people[0].actor, s.exchange, giftexchange.Preferences{Likes: "Games"},
		)
		if !errors.Is(err, giftexchange.ErrInvalidState) {
			t.Fatalf("SetPreferences in revealed: err = %v, want ErrInvalidState", err)
		}
		var stateErr *giftexchange.StateError
		if !errors.As(err, &stateErr) || stateErr.Op != "set_preferences" || stateErr.State != giftexchange.StateRevealed {
			t.Errorf("StateError = %+v, want op=set_preferences state=revealed", stateErr)
		}
		assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
	})

	t.Run("cancelled exchange rejects SetPreferences", func(t *testing.T) {
		s := newScene(t)
		alice := s.h.newPerson(t)
		s.h.join(t, alice, s.group, s.exchange)
		if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		before := takeSnapshot(t, s.h, s.exchange)

		_, err := s.h.svc.SetPreferences(
			t.Context(), alice.actor, s.exchange, giftexchange.Preferences{Likes: "Games"},
		)
		if !errors.Is(err, giftexchange.ErrInvalidState) {
			t.Fatalf("SetPreferences in cancelled: err = %v, want ErrInvalidState", err)
		}
		var stateErr *giftexchange.StateError
		if !errors.As(err, &stateErr) || stateErr.Op != "set_preferences" || stateErr.State != giftexchange.StateCancelled {
			t.Errorf("StateError = %+v, want op=set_preferences state=cancelled", stateErr)
		}
		assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
	})
}

func TestParsePreferencesAndSetPreferencesValidation(t *testing.T) {
	maxText := strings.Repeat("a", giftexchange.MaxPreferenceRunes)
	tooLong := maxText + "a"

	validCases := []struct {
		name string
		raw  giftexchange.Preferences
		want giftexchange.Preferences
	}{
		{
			name: "all empty",
			raw:  giftexchange.Preferences{},
			want: giftexchange.Preferences{},
		},
		{
			name: "single rune and max runes",
			raw: giftexchange.Preferences{
				Likes:    "a",
				Dislikes: maxText,
				Details:  "Line 1\nLine 2",
			},
			want: giftexchange.Preferences{
				Likes:    "a",
				Dislikes: maxText,
				Details:  "Line 1\nLine 2",
			},
		},
		{
			name: "unicode combining marks, RTL, emoji, and mentions",
			raw: giftexchange.Preferences{
				Likes:    "  Cafe\u0301 \U0001F381  ",
				Dislikes: "שלום עולם",
				Details:  "@everyone <@100000000000000001>",
			},
			want: giftexchange.Preferences{
				Likes:    "Cafe\u0301 \U0001F381",
				Dislikes: "שלום עולם",
				Details:  "@everyone <@100000000000000001>",
			},
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := giftexchange.ParsePreferences(tc.raw)
			if err != nil {
				t.Fatalf("ParsePreferences(%+v) error = %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ParsePreferences = %#v, want %#v", got, tc.want)
			}
		})
	}

	invalidFieldValues := map[string]string{
		"one past maximum": tooLong,
		"spaces only":      "   ",
		"newlines only":    "\n\n",
		"zero-width only":  "\u200b\u200c\u200d\ufeff",
		"control rune":     "coffee\x07tea",
		"bidi override":    "coffee\u202etea",
		"invalid UTF-8":    "coffee\xfftea",
	}

	for name, bad := range invalidFieldValues {
		t.Run("invalid_"+name, func(t *testing.T) {
			_, err := giftexchange.ParsePreferences(giftexchange.Preferences{Likes: bad})
			if !errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Fatalf("ParsePreferences(%s): err = %v, want ErrInvalidInput", name, err)
			}
			if fields := inputFields(err); !slices.Equal(fields, []string{"likes"}) {
				t.Errorf("inputFields = %v, want [likes]", fields)
			}
		})
	}

	t.Run("aggregates all invalid fields with exchange_id", func(t *testing.T) {
		s := newScene(t)
		var zeroID giftexchange.ExchangeID
		_, err := s.h.svc.SetPreferences(t.Context(), s.organizer.actor, zeroID, giftexchange.Preferences{
			Likes:    tooLong,
			Dislikes: "   ",
			Details:  "bad\x00detail",
		})
		if !errors.Is(err, giftexchange.ErrInvalidInput) {
			t.Fatalf("SetPreferences err = %v, want ErrInvalidInput", err)
		}
		wantFields := []string{"exchange_id", "likes", "dislikes", "details"}
		if gotFields := inputFields(err); !slices.Equal(gotFields, wantFields) {
			t.Errorf("inputFields = %v, want %v", gotFields, wantFields)
		}
	})
}

func preferencesUpdatedRows(t *testing.T, x giftexchange.ExchangeID) []notificationRow {
	t.Helper()

	var out []notificationRow
	for _, row := range notificationRows(t, x) {
		if row.kind == string(giftexchange.KindPreferencesUpdated) {
			out = append(out, row)
		}
	}
	return out
}
