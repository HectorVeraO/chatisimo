package discord_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const rulesEmbedFieldMaxRunes = 1024

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
