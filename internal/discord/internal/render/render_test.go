package render

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	fakeUser    = "100000000000000001"
	escapeChars = "\\*_~|>#`<@[]"
)

// abuseCases are names and texts a person could type to break out of the text around them.
var abuseCases = []string{
	"",
	"Winter exchange",
	"@everyone",
	"@here",
	"<@100000000000000001>",
	"<@!100000000000000001>",
	"<@&100000000000000002>",
	"<#100000000000000003>",
	"<t:1:R>",
	"**bold** __under__ ~~strike~~ ||spoiler|| `code` ```block```",
	"# heading\n> quote\n- list",
	"[click](https://example.com)",
	"back\\slash \\* already escaped",
	"right-to-left \u202eevil\u202c and zero-width \u200b\u200d joiners",
	"e\u0301\u0301\u0301 combining marks",
	"emoji \U0001F381 and \U0001F468\u200D\U0001F469\u200D\U0001F467",
	"bad \xff\xfe bytes",
	strings.Repeat("@everyone <@100000000000000001> ", 4),
	strings.Repeat("\u05d0", 100),
}

// escapedPair matches a backslash and the control character it protects.
var escapedPair = regexp.MustCompile(`\\[\\*_~|>#` + "`" + `<@\[\]]`)

// assertEscaped checks that Text keeps the output valid UTF-8 and that, once every protected
// pair is set aside, no control character is left.
func assertEscaped(t *testing.T, input string) {
	t.Helper()

	got := Text(input)
	if !utf8.ValidString(got) {
		t.Fatalf("Text(%q) = %q is not valid UTF-8", input, got)
	}
	if rest := escapedPair.ReplaceAllString(got, ""); strings.ContainsAny(rest, escapeChars) {
		t.Fatalf("Text(%q) = %q has an unescaped control character", input, got)
	}
}

func TestTextEscapesAbuseCases(t *testing.T) {
	for _, input := range abuseCases {
		assertEscaped(t, input)
	}
}

func TestTextTurnsMentionsAndEveryoneIntoLiteralText(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"user mention", "<@100000000000000001>", `\<\@100000000000000001\>`},
		{"everyone", "@everyone", `\@everyone`},
		{"markdown", "*a* _b_", `\*a\* \_b\_`},
		{"backslash", `a\b`, `a\\b`},
		{"plain text stays", "Winter exchange", "Winter exchange"},
		{"masked link", "[click](https://example.com)", `\[click\](https://example.com)`},
		{"timestamp", "<t:1:R>", `\<t:1:R\>`},
		{"spoiler and code", "||spoiler|| `code`", `\|\|spoiler\|\| ` + "\\`code\\`"},
		{"invalid bytes", "bad \xff bytes", "bad � bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.input); got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func FuzzEscapeText(f *testing.F) {
	for _, seed := range abuseCases {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		assertEscaped(t, input)
	})
}

func TestBoldEscapesInsideTheMarkers(t *testing.T) {
	if got, want := Bold("**x**"), `**\*\*x\*\***`; got != want {
		t.Errorf("Bold = %q, want %q", got, want)
	}
}

func TestQuotePrefixesEveryLineAndEscapes(t *testing.T) {
	got := Quote("one\n@everyone\n> two")
	want := "> one\n> \\@everyone\n> \\> two"
	if got != want {
		t.Errorf("Quote = %q, want %q", got, want)
	}
}

func TestMention(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    string
		wantErr bool
	}{
		{"snowflake", fakeUser, "<@100000000000000001>", false},
		{"empty", "", "", true},
		{"letters", "abc", "", true},
		{"markup", "1> @everyone <@2", "", true},
		{"sign", "+12", "", true},
		{"21 digits", "123456789012345678901", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Mention(tt.id)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Mention(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Mention(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func FuzzMention(f *testing.F) {
	seeds := []string{
		fakeUser, "", "abc", "1> @everyone <@2", "+12", "123456789012345678901",
		"0", "\u0661\u0662", "1\u200b2", "\n1", "18446744073709551616", "<@1>",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, id string) {
		got, err := Mention(id)
		if err != nil {
			if got != "" {
				t.Errorf("Mention(%q) = %q with error %v", id, got, err)
			}
			return
		}
		if want := "<@" + id + ">"; got != want {
			t.Errorf("Mention(%q) = %q, want %q", id, got, want)
		}
		for _, r := range id {
			if r < '0' || r > '9' {
				t.Errorf("Mention accepted non-digit rune %U in %q", r, id)
			}
		}
	})
}

func TestTimestamps(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	if got, want := TimestampFull(at), "<t:1800000000:F>"; got != want {
		t.Errorf("TimestampFull = %q, want %q", got, want)
	}
	if got, want := TimestampRelative(at), "<t:1800000000:R>"; got != want {
		t.Errorf("TimestampRelative = %q, want %q", got, want)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		suffix   string
		want     string
		wantRune int
	}{
		{"fits", "abc", 3, "...", "abc", 3},
		{"cuts with suffix", "abcdef", 5, "...", "ab...", 5},
		{"counts runes not bytes", "\u00e1\u00e9\u00ed\u00f3\u00fa\u00fd", 4, "~", "\u00e1\u00e9\u00ed~", 4},
		{"suffix longer than limit is dropped", "abcdef", 2, "...", "ab", 2},
		{"zero limit", "abc", 0, "...", "", 0},
		{"negative limit", "abc", -1, "...", "", 0},
		{"empty", "", 5, "...", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.max, tt.suffix)
			if got != tt.want {
				t.Errorf("Truncate = %q, want %q", got, tt.want)
			}
			if utf8.RuneCountInString(got) != tt.wantRune {
				t.Errorf("Truncate has %d runes, want %d", utf8.RuneCountInString(got), tt.wantRune)
			}
		})
	}
}

func TestEmbedHelpers(t *testing.T) {
	embed := WithField(WithFooter(Embed("Title", "Body"), "Foot"), "Name", "Value")
	if embed.Title != "Title" || embed.Description != "Body" {
		t.Errorf("embed = %+v, want title and description set", embed)
	}
	if embed.Footer == nil || embed.Footer.Text != "Foot" {
		t.Errorf("footer = %+v, want Foot", embed.Footer)
	}
	if len(embed.Fields) != 1 || embed.Fields[0].Name != "Name" || embed.Fields[0].Value != "Value" {
		t.Errorf("fields = %+v, want one Name/Value", embed.Fields)
	}
}

func TestNoMentionsMarshalsEmptyArraysNotNull(t *testing.T) {
	mentions := NoMentions()

	raw, err := json.Marshal(mentions)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"parse":[],"roles":[],"users":[],"replied_user":false}`
	if string(raw) != want {
		t.Errorf("allowed_mentions = %s, want %s", raw, want)
	}
}
