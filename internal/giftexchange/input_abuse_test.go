package giftexchange_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestExchangeNamesAcceptTextThatIsOnlyTextAndRejectTheRest(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"stores a mention of everyone as text", "@everyone", false},
		{"stores user mention syntax as text", "<@" + fakeUserB + "> party", false},
		{"accepts emoji", "Fiesta \U0001F381", false},
		{"accepts right-to-left text", "\U000005e9\U000005dc\U000005d5\U000005dd", false},
		{"rejects 10000 runes", strings.Repeat("a", 10_000), true},
		{"rejects only combining marks", "\U00000301\U00000301\U00000301", true},
		{"rejects only zero-width characters", "\U0000200b\U0000200d\U0000feff", true},
		{"rejects a bidi override", "evil\U0000202etxt", true},
		{"rejects a bidi isolate", "evil\U00002066txt", true},
		{"rejects invalid UTF-8", "bad\xff\xfename", true},
		{"rejects a line break", "two\nlines", true},
		{"rejects a NUL", "nul\x00name", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view, err := h.svc.CreateExchange(t.Context(), organizer.actor,
				giftexchange.CreateExchangeRequest{Group: group, Name: tc.input})

			if tc.wantErr {
				if !errors.Is(err, giftexchange.ErrInvalidInput) {
					t.Errorf("err = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if view.Name != tc.input {
				t.Errorf("stored name = %q, want it stored as given", view.Name)
			}
		})
	}
}

func TestRepeatedAndOutOfOrderCallsNeverDoubleTheirEffect(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	if _, err := s.h.svc.Leave(t.Context(), joiner.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	_, secondLeave := s.h.svc.Leave(t.Context(), joiner.actor, s.group, s.exchange)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	afterCancelJoin := s.h.svc.Join(t.Context(), joiner.actor, s.group, s.exchange)

	if !errors.Is(secondLeave, giftexchange.ErrNotParticipant) {
		t.Errorf("second leave err = %v, want ErrNotParticipant", secondLeave)
	}
	if !errors.Is(afterCancelJoin, giftexchange.ErrInvalidState) {
		t.Errorf("join after cancel err = %v, want ErrInvalidState", afterCancelJoin)
	}
	if got := len(notificationRows(t, s.exchange)); got != 0 {
		t.Errorf("%d notifications, want 0: nobody was an active participant at the cancel", got)
	}
}

func TestIdentitySubjectsStayOpaqueText(t *testing.T) {
	h := newService(t)
	subject := "<@" + fakeUserA + "> @everyone \U0000202e"

	actor, err := h.svc.ResolveActor(t.Context(),
		giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: subject})
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}

	again, err := h.svc.ResolveActor(t.Context(),
		giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: subject})
	if err != nil {
		t.Fatalf("second ResolveActor: %v", err)
	}
	if actor.Person() != again.Person() {
		t.Error("the same opaque subject resolved to two persons")
	}
}
