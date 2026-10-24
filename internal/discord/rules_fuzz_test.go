//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const rulesEmbedFieldMaxRunes = 1024

func FuzzRulesPickValue(f *testing.F) {
	seeds := []string{
		"", "   ", "draw_at", "reminder_lead", "exclusions", "min_participants", "no_such_rule",
		"DRAW_AT", "draw_at\x00", "\u202edraw_at", "draw\u200b_at", "<@100000000000000001>",
		"@everyone", "**draw_at**", strings.Repeat("a", 4096), "ÿþ", "🎁", "../draw_at",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		s := newScene(t)
		id := rulesOpenExchange(t, s, "Winter")
		view := rulesShow(t, s, s.a, id)

		result, err := s.fake.SelectForged(t.Context(), s.users[s.a], view.message, "/gx/rule-pick/"+id.String(), value)
		if err != nil {
			t.Fatalf("SelectForged(%q): %v", value, err)
		}

		if result.Modal != nil {
			return
		}

		got := rulesReplyOf(t, s, result)
		if len(got.visibleTo) != 1 || got.visibleTo[0] != s.a {
			t.Fatalf("%q: reply visible to %v, want only the caller", value, got.visibleTo)
		}
		if strings.Contains(got.content, "@everyone") {
			t.Fatalf("%q: the reply echoes a mention: %q", value, got.content)
		}
	})
}

func FuzzRulesModalInput(f *testing.F) {
	seeds := []string{
		"", "   ", "in 3d", "in 9999d", "tomorrow", "2026-12-20 18:00", "2030-01-02T03:04:05Z",
		"\u202ein 3d", "in\u200b 3d", "<@100000000000000001> @everyone", "**in 3d**",
		strings.Repeat("9", 64), strings.Repeat("9", 65), "\xff\xfe", "\U0001F381", "0", "-1d", "36h",
	}
	for _, seed := range seeds {
		f.Add(seed, true)
		f.Add(seed, false)
	}

	f.Fuzz(func(t *testing.T, typed string, isTime bool) {
		rule := "reminder_lead"
		if isTime {
			rule = "draw_at"
		}
		s := newScene(t)
		id := rulesOpenExchange(t, s, "Winter")
		view := rulesShow(t, s, s.a, id)
		picked := rulesPick(t, s, s.a, view, id, rule)
		if picked.Modal == nil {
			t.Fatalf("picking %s opened no modal", rule)
		}

		submitted, err := s.fake.SubmitModalForged(t.Context(), s.users[s.a], *picked.Modal, map[string]string{rulesInputID: typed})
		if err != nil {
			t.Fatalf("SubmitModalForged(%q): %v", typed, err)
		}

		got := rulesReplyOf(t, s, submitted)
		if len(got.visibleTo) != 1 || got.visibleTo[0] != s.a {
			t.Fatalf("%q: reply visible to %v, want only the caller", typed, got.visibleTo)
		}
		for _, field := range got.embed.Fields {
			if utf8.RuneCountInString(field.Value) > rulesEmbedFieldMaxRunes {
				t.Fatalf("%q: field %q has %d runes", typed, field.Name, utf8.RuneCountInString(field.Value))
			}
		}
		if strings.Contains(got.content, "@everyone") {
			t.Fatalf("%q: the reply echoes a mention: %q", typed, got.content)
		}
	})
}
