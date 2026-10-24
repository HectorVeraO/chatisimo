package giftexchange_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	giverID     = "0190a1b2-c3d4-7e5f-8a6b-7c8d9e0f1a01"
	recipientID = "0190a1b2-c3d4-7e5f-8a6b-7c8d9e0f1a02"
	subject     = "100000000000000001"
	questionTxt = "Do you like chocolate?"
	answerTxt   = "Only the dark kind"
)

func mustPersonRef(t *testing.T, rawID string) giftexchange.PersonRef {
	t.Helper()
	id, err := giftexchange.ParsePersonID(rawID)
	if err != nil {
		t.Fatal(err)
	}
	identity := giftexchange.ExternalIdentity{Provider: giftexchange.ProviderDiscord, Subject: subject}
	return giftexchange.PersonRef{ID: id, Identities: []giftexchange.ExternalIdentity{identity}}
}

func sensitiveFragments() []string {
	return []string{giverID, recipientID, subject, questionTxt, answerTxt}
}

func assertNoLeak(t *testing.T, label string, x any, want string) {
	t.Helper()
	renders := map[string]string{
		"%v":  fmt.Sprintf("%v", x),
		"%+v": fmt.Sprintf("%+v", x),
		"%#v": fmt.Sprintf("%#v", x),
		"%s":  fmt.Sprintf("%s", x),
		"%d":  fmt.Sprintf("%d", x),
	}
	if s, ok := x.(fmt.Stringer); ok {
		renders["String()"] = s.String()
	}
	for verb, got := range renders {
		if got != want {
			t.Errorf("%s %s = %q, want %q", label, verb, got, want)
		}
	}

	var jsonBuf, textBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("event", slog.Any("x", x))
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("event", slog.Any("x", x))
	for handler, out := range map[string]string{"json": jsonBuf.String(), "text": textBuf.String()} {
		for _, fragment := range sensitiveFragments() {
			if strings.Contains(out, fragment) {
				t.Errorf("%s slog %s output %q contains %q", label, handler, out, fragment)
			}
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s slog %s output %q lacks %q", label, handler, out, want)
		}
	}
}

func TestAssignmentIsRedactedEverywhere(t *testing.T) {
	exchange, err := giftexchange.ParseExchangeID(validID)
	if err != nil {
		t.Fatal(err)
	}
	a := giftexchange.Assignment{Exchange: exchange, Recipient: mustPersonRef(t, recipientID)}
	assertNoLeak(t, "Assignment", a, "[redacted]")
	assertNoLeak(t, "*Assignment", &a, "[redacted]")
}

func TestRevealedGiverIsRedactedEverywhere(t *testing.T) {
	g := giftexchange.RevealedGiver{Giver: mustPersonRef(t, giverID)}
	assertNoLeak(t, "RevealedGiver", g, "[redacted]")
}

func TestNotificationRendersOnlyIDAndKind(t *testing.T) {
	counterpart := mustPersonRef(t, recipientID)
	questionID, err := giftexchange.ParseQuestionID(validID)
	if err != nil {
		t.Fatal(err)
	}
	notificationID := giftexchange.NotificationID(questionID)
	n := giftexchange.Notification{
		ID:          notificationID,
		Kind:        giftexchange.KindAnswerReceived,
		To:          mustPersonRef(t, giverID),
		Counterpart: &counterpart,
		Question:    &giftexchange.NotifiedQuestion{ID: questionID, Text: questionTxt, Answer: answerTxt},
	}
	want := "notification " + validID + " answer_received"
	assertNoLeak(t, "Notification", n, want)
	assertNoLeak(t, "*Notification", &n, want)
}
