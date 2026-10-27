package giftexchangecmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	fakeAddressee   = "100000000000000001"
	fakeCounterpart = "100000000000000002"
	fakeGuildName   = "Test server"
	fakeExchange    = "Winter exchange"
	footerLine      = "Use /ge recipient, /ge giver or /ge questions to see this again."
)

var (
	fakeDrawAt   = time.Unix(1_800_000_000, 0).UTC()
	fakeRevealAt = time.Unix(1_800_086_400, 0).UTC()
	fakeQuestion = giftexchange.QuestionID(uuid.UUID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10})
)

func person(subject string) giftexchange.PersonRef {
	ref := giftexchange.PersonRef{Identities: []giftexchange.ExternalIdentity{
		{Provider: giftexchange.ProviderDiscord, Subject: subject},
	}}
	return ref
}

func notification(kind giftexchange.NotificationKind) giftexchange.Notification {
	n := giftexchange.Notification{
		ID:   giftexchange.NotificationID(uuid.UUID{1}),
		Kind: kind,
		To:   person(fakeAddressee),
		Exchange: giftexchange.ExchangeSummary{
			Name:     fakeExchange,
			DrawAt:   &fakeDrawAt,
			RevealAt: &fakeRevealAt,
		},
	}
	return n
}

func withCounterpart(n giftexchange.Notification) giftexchange.Notification {
	counterpart := person(fakeCounterpart)
	n.Counterpart = &counterpart
	return n
}

func TestRenderNotificationRendersEveryKind(t *testing.T) {
	const x = "**Winter exchange**"
	g := "(" + fakeGuildName + ")"

	drawFailed := notification(giftexchange.KindDrawFailed)
	drawFailed.Reason = giftexchange.ReasonTooFewParticipants
	noValidDraw := notification(giftexchange.KindDrawFailed)
	noValidDraw.Reason = giftexchange.ReasonNoValidAssignment
	question := notification(giftexchange.KindQuestionReceived)
	question.Question = &giftexchange.NotifiedQuestion{ID: fakeQuestion, Text: "What size are you?"}
	answer := notification(giftexchange.KindAnswerReceived)
	answer.Question = &giftexchange.NotifiedQuestion{ID: fakeQuestion, Text: "What size?", Answer: "Medium"}
	prefsUpdated := withCounterpart(notification(giftexchange.KindPreferencesUpdated))
	prefsUpdated.Preferences = giftexchange.Preferences{
		Likes:    "Dark chocolate",
		Dislikes: "Perfume",
		Details:  "Shoe size 42",
	}
	prefsCleared := withCounterpart(notification(giftexchange.KindPreferencesUpdated))

	tests := []struct {
		name         string
		n            giftexchange.Notification
		wantContent  string
		wantTitle    string
		wantDesc     string
		wantRevealIn string
	}{
		{
			name:         "drawn",
			n:            withCounterpart(notification(giftexchange.KindDrawn)),
			wantContent:  "The draw for " + x + " " + g + " is done.",
			wantTitle:    "Your recipient",
			wantDesc:     "<@" + fakeCounterpart + ">",
			wantRevealIn: "<t:1800086400:F>",
		},
		{
			name:        "redrawn",
			n:           withCounterpart(notification(giftexchange.KindRedrawn)),
			wantContent: "A participant left " + x + " " + g + " and the draw was redone.",
			wantTitle:   "Your new recipient",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
		{
			name:        "assignment changed",
			n:           withCounterpart(notification(giftexchange.KindAssignmentChanged)),
			wantContent: "A participant left " + x + " " + g + ". Your recipient changed.",
			wantTitle:   "Your new recipient",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
		{
			name: "round voided",
			n:    notification(giftexchange.KindRoundVoided),
			wantContent: "The draw for " + x + " " + g +
				" was undone: too few participants or no valid draw. It is open again.",
		},
		{
			name:        "draw failed, too few",
			n:           drawFailed,
			wantContent: "The scheduled draw for " + x + " " + g + " failed: too few participants.",
		},
		{
			name:        "draw failed, no valid draw",
			n:           noValidDraw,
			wantContent: "The scheduled draw for " + x + " " + g + " failed: no valid draw with the current exclusions.",
		},
		{
			name:        "draw soon",
			n:           notification(giftexchange.KindDrawSoon),
			wantContent: "Reminder: the draw for " + x + " " + g + " is <t:1800000000:R>.",
		},
		{
			name:        "reveal soon",
			n:           withCounterpart(notification(giftexchange.KindRevealSoon)),
			wantContent: "Reminder: " + x + " " + g + " is revealed <t:1800086400:R>.",
			wantTitle:   "Your recipient",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
		{
			name:        "revealed",
			n:           withCounterpart(notification(giftexchange.KindRevealed)),
			wantContent: x + " " + g + " is revealed.",
			wantTitle:   "Your giver",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
		{
			name:        "cancelled",
			n:           notification(giftexchange.KindCancelled),
			wantContent: x + " " + g + " was cancelled by the organizer.",
		},
		{
			name:        "question received",
			n:           question,
			wantContent: "Anonymous question from your giver in " + x + " " + g + ":\n> What size are you?",
		},
		{
			name: "answer received",
			n:    answer,
			wantContent: "Your recipient answered your question in " + x + " " + g +
				":\n> What size?\n> Medium",
		},
		{
			name:        "preferences updated",
			n:           prefsUpdated,
			wantContent: "Your recipient updated their gift preferences in " + x + " " + g + ".",
			wantTitle:   "Your recipient",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
		{
			name:        "preferences cleared",
			n:           prefsCleared,
			wantContent: "Your recipient updated their gift preferences in " + x + " " + g + ".",
			wantTitle:   "Your recipient",
			wantDesc:    "<@" + fakeCounterpart + ">",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderNotification(tt.n, fakeGuildName)
			if err != nil {
				t.Fatalf("RenderNotification: %v", err)
			}

			hasEmbed := tt.wantTitle != ""
			if hasEmbed {
				assertEmbed(t, got, tt.wantTitle, tt.wantDesc, tt.wantRevealIn)
				if got.Content != tt.wantContent {
					t.Errorf("content = %q, want exactly %q", got.Content, tt.wantContent)
				}
			} else {
				wantContent := tt.wantContent + "\n-# " + footerLine
				if got.Content != wantContent {
					t.Errorf("content = %q, want %q", got.Content, wantContent)
				}
				if len(got.Embeds) != 0 {
					t.Errorf("embeds = %d, want none", len(got.Embeds))
				}
			}
		})
	}
}

func assertEmbed(t *testing.T, got discord.MessageCreate, title, desc, reveal string) {
	t.Helper()

	if len(got.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(got.Embeds))
	}
	embed := got.Embeds[0]
	if embed.Title != title || embed.Description != desc {
		t.Errorf("embed = %q: %q, want %q: %q", embed.Title, embed.Description, title, desc)
	}
	if embed.Footer == nil || embed.Footer.Text != footerLine {
		t.Errorf("footer = %+v, want %q", embed.Footer, footerLine)
	}
	if reveal != "" {
		hasReveal := len(embed.Fields) == 1 && embed.Fields[0].Name == "Reveal" && embed.Fields[0].Value == reveal
		if !hasReveal {
			t.Errorf("fields = %+v, want one Reveal field %q", embed.Fields, reveal)
		}
	}
}

func TestRenderDrawnShowsManualRevealWhenNoRevealTime(t *testing.T) {
	n := withCounterpart(notification(giftexchange.KindDrawn))
	n.Exchange.RevealAt = nil

	got, err := RenderNotification(n, fakeGuildName)
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	assertEmbed(t, got, "Your recipient", "<@"+fakeCounterpart+">", "not set (organizer runs it)")
}

func TestRenderRecipientNoticesIncludeNonEmptyPreferences(t *testing.T) {
	prefs := giftexchange.Preferences{
		Likes:    "Board games",
		Dislikes: "Licorice",
		Details:  "No allergies",
	}

	for _, kind := range []giftexchange.NotificationKind{
		giftexchange.KindDrawn,
		giftexchange.KindRedrawn,
		giftexchange.KindAssignmentChanged,
		giftexchange.KindPreferencesUpdated,
	} {
		n := withCounterpart(notification(kind))
		n.Preferences = prefs

		got, err := RenderNotification(n, fakeGuildName)
		if err != nil {
			t.Fatalf("RenderNotification(%s): %v", kind, err)
		}

		raw, err := json.Marshal(got.Embeds[0].Fields)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", kind, err)
		}
		for _, want := range []string{
			"Likes and hobbies", "Board games",
			"Dislikes / avoid", "Licorice",
			"Helpful details", "No allergies",
		} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s fields %s do not include %q", kind, raw, want)
			}
		}
	}
}

func TestRenderQuestionHasAnswerButtonAndNamesNoAsker(t *testing.T) {
	n := notification(giftexchange.KindQuestionReceived)
	n.Question = &giftexchange.NotifiedQuestion{ID: fakeQuestion, Text: "Hello"}

	got, err := RenderNotification(n, fakeGuildName)
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	wantID := "/gx/answer/01020304-0506-0708-090a-0b0c0d0e0f10"
	if !strings.Contains(string(raw), wantID) || !strings.Contains(string(raw), `"label":"Answer"`) {
		t.Errorf("message %s has no Answer button with custom ID %s", raw, wantID)
	}
	if strings.Contains(string(raw), "<@") {
		t.Errorf("question notice %s mentions someone", raw)
	}
}

func TestRenderNeverPingsAnyone(t *testing.T) {
	n := withCounterpart(notification(giftexchange.KindDrawn))

	got, err := RenderNotification(n, fakeGuildName)
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	raw, err := json.Marshal(got.AllowedMentions)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"parse":[],"roles":[],"users":[],"replied_user":false}`
	if string(raw) != want {
		t.Errorf("allowed_mentions = %s, want %s", raw, want)
	}
}

func TestRenderUsesUnknownServerWhenGuildNameIsEmpty(t *testing.T) {
	got, err := RenderNotification(notification(giftexchange.KindCancelled), "")
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	if !strings.Contains(got.Content, "(unknown server)") {
		t.Errorf("content = %q, want unknown server", got.Content)
	}

	gotES, err := RenderNotificationIn(LocaleES419, notification(giftexchange.KindCancelled), "")
	if err != nil {
		t.Fatalf("RenderNotificationIn(es-419): %v", err)
	}
	if !strings.Contains(gotES.Content, "(servidor desconocido)") {
		t.Errorf("es-419 content = %q, want servidor desconocido", gotES.Content)
	}

	gotFallback, err := RenderNotificationIn("fr", notification(giftexchange.KindCancelled), "")
	if err != nil {
		t.Fatalf("RenderNotificationIn(fr): %v", err)
	}
	if gotFallback.Content != got.Content {
		t.Errorf("fallback content = %q, want %q", gotFallback.Content, got.Content)
	}
}

func TestRenderEscapesUserSuppliedText(t *testing.T) {
	hostile := []string{
		"@everyone",
		"<@100000000000000009>",
		"**bold** `code` ||spoiler||",
		"\u202eRTL\u202c \u200bzero-width",
		strings.Repeat("@", giftexchange.MaxNameRunes),
	}

	for _, name := range hostile {
		n := notification(giftexchange.KindCancelled)
		n.Exchange.Name = name

		got, err := RenderNotification(n, name)
		if err != nil {
			t.Fatalf("RenderNotification(%q): %v", name, err)
		}

		withoutEscaped := strings.ReplaceAll(got.Content, `\@`, "")
		withoutEscaped = strings.ReplaceAll(withoutEscaped, `\<`, "")
		if strings.Contains(withoutEscaped, "@") || strings.Contains(withoutEscaped, "<@") {
			t.Errorf("content %q for %q has an active mention", got.Content, name)
		}
		isUnescaped := strings.Contains(name, "**") && !strings.Contains(got.Content, `\*\*bold`)
		if isUnescaped {
			t.Errorf("content %q does not escape the markdown of %q", got.Content, name)
		}
	}
}

func TestRenderQuotedTextCannotMentionOrFormat(t *testing.T) {
	n := notification(giftexchange.KindQuestionReceived)
	n.Question = &giftexchange.NotifiedQuestion{ID: fakeQuestion, Text: "@everyone <@100000000000000009>\n# big"}

	got, err := RenderNotification(n, fakeGuildName)
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	want := "> \\@everyone \\<\\@100000000000000009\\>\n> \\# big"
	if !strings.Contains(got.Content, want) {
		t.Errorf("content = %q, want it to contain %q", got.Content, want)
	}
}

func TestRenderLongQuestionAndAnswerFitDiscordLimit(t *testing.T) {
	long := strings.Repeat("@\n", giftexchange.MaxQuestionRunes/2)
	n := notification(giftexchange.KindAnswerReceived)
	n.Exchange.Name = strings.Repeat("*", giftexchange.MaxNameRunes)
	n.Question = &giftexchange.NotifiedQuestion{ID: fakeQuestion, Text: "QQQ" + long, Answer: "AAA" + long}

	got, err := RenderNotification(n, strings.Repeat("<", 100))
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	if runes := utf8.RuneCountInString(got.Content); runes > 2000 {
		t.Errorf("content has %d runes, Discord allows 2000", runes)
	}
	for _, marker := range []string{"QQQ", "AAA"} {
		if !strings.Contains(got.Content, marker) {
			t.Errorf("content %q dropped the text starting with %s", got.Content, marker)
		}
	}
}

func TestRenderOnlyMentionsTheCounterpartFromTheirOwnIdentity(t *testing.T) {
	n := withCounterpart(notification(giftexchange.KindDrawn))
	n.Counterpart.Identities = append([]giftexchange.ExternalIdentity{{Provider: "other", Subject: "7"}},
		n.Counterpart.Identities...)

	got, err := RenderNotification(n, fakeGuildName)
	if err != nil {
		t.Fatalf("RenderNotification: %v", err)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), fakeAddressee) {
		t.Errorf("message %s names the addressee", raw)
	}
	if got.Embeds[0].Description != "<@"+fakeCounterpart+">" {
		t.Errorf("description = %q, want the counterpart mention", got.Embeds[0].Description)
	}
}

func TestRenderRejectsWhatItCannotBuild(t *testing.T) {
	noDiscord := withCounterpart(notification(giftexchange.KindDrawn))
	noDiscord.Counterpart.Identities = nil
	badSubject := withCounterpart(notification(giftexchange.KindDrawn))
	badSubject.Counterpart.Identities[0].Subject = "1> @everyone"
	noDrawAt := notification(giftexchange.KindDrawSoon)
	noDrawAt.Exchange.DrawAt = nil
	noRevealAt := withCounterpart(notification(giftexchange.KindRevealSoon))
	noRevealAt.Exchange.RevealAt = nil
	noReason := notification(giftexchange.KindDrawFailed)
	unknownReason := notification(giftexchange.KindDrawFailed)
	unknownReason.Reason = "other"

	tests := []struct {
		name     string
		n        giftexchange.Notification
		wantKind string
	}{
		{"unknown kind", notification("mystery"), "mystery"},
		{"pair kind without counterpart", notification(giftexchange.KindDrawn), "drawn"},
		{"counterpart without discord identity", noDiscord, "drawn"},
		{"counterpart with a non-numeric subject", badSubject, "drawn"},
		{"draw soon without draw time", noDrawAt, "draw_soon"},
		{"reveal soon without reveal time", noRevealAt, "reveal_soon"},
		{"draw failed without reason", noReason, "draw_failed"},
		{"draw failed with unknown reason", unknownReason, "draw_failed"},
		{"question without question", notification(giftexchange.KindQuestionReceived), "question_received"},
		{"answer without question", notification(giftexchange.KindAnswerReceived), "answer_received"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RenderNotification(tt.n, fakeGuildName)
			if !errors.Is(err, ErrCannotRender) {
				t.Fatalf("error = %v, want ErrCannotRender", err)
			}
			if !strings.Contains(err.Error(), tt.wantKind) {
				t.Errorf("error %q does not name the kind %q", err, tt.wantKind)
			}
			if strings.Contains(err.Error(), fakeCounterpart) || strings.Contains(err.Error(), "@everyone") {
				t.Errorf("error %q leaks data", err)
			}
		})
	}
}
