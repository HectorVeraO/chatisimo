package giftexchange_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestNewReportsEveryMissingDependencyAtOnce(t *testing.T) {
	svc, err := giftexchange.New(giftexchange.Deps{})
	if svc != nil {
		t.Errorf("service = %v, want nil", svc)
	}
	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}

	got := inputFields(err)
	want := []string{"deps.Pool", "deps.Scheduler", "deps.Clock", "deps.Random", "deps.Notifier", "deps.Rephraser", "deps.Logger"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestNewReportsOnlyTheMissingDependencies(t *testing.T) {
	_, err := giftexchange.New(giftexchange.Deps{Pool: shared.Pool(), Clock: newFakeClock()})
	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}

	got := inputFields(err)
	want := []string{"deps.Scheduler", "deps.Random", "deps.Notifier", "deps.Rephraser", "deps.Logger"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestResolveActorIsIdempotent(t *testing.T) {
	h := newService(t)
	ref := giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}

	first, err := h.svc.ResolveActor(t.Context(), ref)
	if err != nil {
		t.Fatalf("first ResolveActor: %v", err)
	}
	second, err := h.svc.ResolveActor(t.Context(), ref)
	if err != nil {
		t.Fatalf("second ResolveActor: %v", err)
	}

	if first.Person() != second.Person() {
		t.Errorf("second call returned person %v, want %v", second.Person(), first.Person())
	}
	if first.Person().IsZero() {
		t.Error("person is the zero ID")
	}
}

func TestResolvePersonMatchesResolveActor(t *testing.T) {
	h := newService(t)
	ref := giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}

	asTarget, err := h.svc.ResolvePerson(t.Context(), ref)
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	asActor, err := h.svc.ResolveActor(t.Context(), ref)
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}

	if asTarget != asActor.Person() {
		t.Errorf("ResolvePerson = %v, ResolveActor person = %v", asTarget, asActor.Person())
	}
}

func TestResolveActorGivesDistinctSubjectsDistinctPersons(t *testing.T) {
	h := newService(t)
	a := h.newPerson(t)
	b := h.newPerson(t)

	if a.id == b.id {
		t.Errorf("two subjects resolved to the same person %v", a.id)
	}
}

func TestResolveActorIsSafeUnderConcurrentFirstSight(t *testing.T) {
	h := newService(t)
	ref := giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}

	const callers = 8
	results := make(chan giftexchange.PersonID, callers)
	errs := make(chan error, callers)
	for range callers {
		go func() {
			id, err := h.svc.ResolvePerson(t.Context(), ref)
			errs <- err
			results <- id
		}()
	}

	var first giftexchange.PersonID
	for range callers {
		err := <-errs
		if err != nil {
			t.Fatalf("ResolvePerson: %v", err)
		}
		id := <-results
		if first.IsZero() {
			first = id
		}
		if id != first {
			t.Errorf("concurrent first sight returned two persons: %v and %v", first, id)
		}
	}
}

func TestResolveGroupIsIdempotentAndDistinct(t *testing.T) {
	h := newService(t)
	ref := giftexchange.GroupRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}
	other := giftexchange.GroupRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}

	first, err := h.svc.ResolveGroup(t.Context(), ref)
	if err != nil {
		t.Fatalf("first ResolveGroup: %v", err)
	}
	second, err := h.svc.ResolveGroup(t.Context(), ref)
	if err != nil {
		t.Fatalf("second ResolveGroup: %v", err)
	}
	different, err := h.svc.ResolveGroup(t.Context(), other)
	if err != nil {
		t.Fatalf("ResolveGroup of another subject: %v", err)
	}

	if first != second {
		t.Errorf("second call returned %v, want %v", second, first)
	}
	if first == different {
		t.Error("two subjects resolved to the same group")
	}
}

func TestResolveRejectsInvalidReferences(t *testing.T) {
	h := newService(t)
	tooLong := strings.Repeat("1", 129)
	atLimit := strings.Repeat("2", 128)

	tests := []struct {
		name     string
		provider giftexchange.Provider
		subject  string
		wantErr  bool
	}{
		{"unknown provider", "slack", fakeUserA, true},
		{"empty provider", "", fakeUserA, true},
		{"empty subject", giftexchange.ProviderDiscord, "", true},
		{"subject over 128 bytes", giftexchange.ProviderDiscord, tooLong, true},
		{"multibyte subject over 128 bytes", giftexchange.ProviderDiscord, strings.Repeat("é", 65), true},
		{"subject with a NUL byte", giftexchange.ProviderDiscord, "10\x0000", true},
		{"subject with invalid UTF-8", giftexchange.ProviderDiscord, "10\xff00", true},
		{"subject of exactly 128 bytes", giftexchange.ProviderDiscord, atLimit, false},
		{"subject of 1 byte", giftexchange.ProviderDiscord, "7", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, actorErr := h.svc.ResolveActor(t.Context(),
				giftexchange.IdentityRef{Provider: tc.provider, Subject: tc.subject})
			_, personErr := h.svc.ResolvePerson(t.Context(),
				giftexchange.IdentityRef{Provider: tc.provider, Subject: tc.subject})
			_, groupErr := h.svc.ResolveGroup(t.Context(),
				giftexchange.GroupRef{Provider: tc.provider, Subject: tc.subject})

			for name, err := range map[string]error{"actor": actorErr, "person": personErr, "group": groupErr} {
				if tc.wantErr {
					if !errors.Is(err, giftexchange.ErrInvalidInput) {
						t.Errorf("%s err = %v, want ErrInvalidInput", name, err)
					}
					continue
				}
				if err != nil {
					t.Errorf("%s err = %v, want nil", name, err)
				}
			}
		})
	}
}

func TestResolveErrorsNeverEchoTheRejectedSubject(t *testing.T) {
	h := newService(t)
	subject := strings.Repeat("9", 129)

	_, err := h.svc.ResolveActor(t.Context(),
		giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: subject})
	if err == nil {
		t.Fatal("err = nil, want ErrInvalidInput")
	}

	assertConfidential(t, h, []error{err}, []string{subject})
}
