package render

import (
	"encoding/json"
	"regexp"
	"strconv"
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
	"<:CatHmph:123456789012345678>",
	"<a:party_cat_2:123456789012345678>",
	"**bold** <@12345> <:cat_smile:987654321098765432> _italics_",
	"<:a:123>",
	"<:" + strings.Repeat("a", 33) + ":123>",
	"<:ab:0>",
	"<:ab:00123>",
	"<:ab:99999999999999999999>",
	"<:<:ab:123>:123>",
	"<:ab:123",
	`\<:CatHmph:123456789012345678>`,
}

// escapedPair matches a backslash and the control character it protects.
var (
	escapedPair       = regexp.MustCompile(`\\[\\*_~|>#` + "`" + `<@\[\]]`)
	customEmojiFormat = regexp.MustCompile(`<a?:[A-Za-z0-9_]{2,32}:([1-9][0-9]{0,19})>`)
)

func stripValidCustomEmojis(s string) string {
	var b strings.Builder
	cursor := 0
	for cursor < len(s) {
		if s[cursor] == '\\' && cursor+1 < len(s) {
			b.WriteString(s[cursor : cursor+2])
			cursor += 2
			continue
		}
		if s[cursor] == '<' {
			loc := customEmojiFormat.FindStringSubmatchIndex(s[cursor:])
			if loc != nil && loc[0] == 0 {
				id := s[cursor+loc[2] : cursor+loc[3]]
				_, err := strconv.ParseUint(id, 10, 64)
				if err == nil {
					cursor += loc[1]
					continue
				}
			}
		}
		b.WriteByte(s[cursor])
		cursor++
	}
	return b.String()
}

// assertEscaped checks that Text keeps the output valid UTF-8 and that, once every valid
// custom emoji and protected pair are set aside, no control character is left.
func assertEscaped(t *testing.T, input string) {
	t.Helper()

	got := Text(input)
	if !utf8.ValidString(got) {
		t.Fatalf("Text(%q) = %q is not valid UTF-8", input, got)
	}
	withoutEmojis := stripValidCustomEmojis(got)
	if rest := escapedPair.ReplaceAllString(withoutEmojis, ""); strings.ContainsAny(rest, escapeChars) {
		t.Fatalf("Text(%q) = %q has an unescaped control character outside custom emojis", input, got)
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
		{"nickname mention", "<@!100000000000000001>", `\<\@!100000000000000001\>`},
		{"role mention", "<@&100000000000000002>", `\<\@&100000000000000002\>`},
		{"channel link", "<#100000000000000003>", `\<\#100000000000000003\>`},
		{"everyone", "@everyone", `\@everyone`},
		{"markdown", "*a* _b_", `\*a\* \_b\_`},
		{"backslash", `a\b`, `a\\b`},
		{"plain text stays", "Winter exchange", "Winter exchange"},
		{"masked link", "[click](https://example.com)", `\[click\](https://example.com)`},
		{"autolink", "<https://example.com>", `\<https://example.com\>`},
		{"timestamp", "<t:1:R>", `\<t:1:R\>`},
		{"full timestamp", "<t:1700000000:F>", `\<t:1700000000:F\>`},
		{"spoiler and code", "||spoiler|| `code`", `\|\|spoiler\|\| ` + "\\`code\\`"},
		{"invalid bytes", "bad \xff bytes", "bad " + string(utf8.RuneError) + " bytes"},
		{"static custom emoji", "<:CatHmph:123456789012345678>", "<:CatHmph:123456789012345678>"},
		{"animated custom emoji with underscores", "<a:party_cat_2:123456789012345678>", "<a:party_cat_2:123456789012345678>"},
		{"min length emoji name and id", "<:ab:1>", "<:ab:1>"},
		{"max length emoji name and max uint64 id", "<:" + strings.Repeat("A", 32) + ":18446744073709551615>", "<:" + strings.Repeat("A", 32) + ":18446744073709551615>"},
		{
			"adjacent markdown and mentions around custom emoji",
			"**bold** <@12345> <:cat_smile:987654321098765432> _italics_",
			`\*\*bold\*\* \<\@12345\> <:cat_smile:987654321098765432> \_italics\_`,
		},
		{
			"underscores adjacent to custom emoji",
			"_italic_ <:cat_hmph:123456789012345678> _more_",
			`\_italic\_ <:cat_hmph:123456789012345678> \_more\_`,
		},
		{"rejected 1-char emoji name", "<:a:123>", `\<:a:123\>`},
		{"rejected 33-char emoji name", "<:" + strings.Repeat("a", 33) + ":123>", `\<:` + strings.Repeat("a", 33) + `:123\>`},
		{"rejected zero emoji id", "<:ab:0>", `\<:ab:0\>`},
		{"rejected leading-zero emoji id", "<:ab:00123>", `\<:ab:00123\>`},
		{"rejected uint64 overflow emoji id", "<:ab:99999999999999999999>", `\<:ab:99999999999999999999\>`},
		{"rejected negative emoji id", "<:ab:-123>", `\<:ab:-123\>`},
		{"rejected non-digit emoji id", "<:ab:12a3>", `\<:ab:12a3\>`},
		{"rejected nested custom emoji", "<:<:ab:123>:123>", `\<:<:ab:123>:123\>`},
		{"rejected unclosed custom emoji", "<:ab:123", `\<:ab:123`},
		{"rejected non-ascii emoji name", "<:cat_\u00e9:123>", `\<:cat\_` + "\u00e9" + `:123\>`},
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

func FuzzText(f *testing.F) {
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
	if got, want := TimestampShortTime(at), "<t:1800000000:t>"; got != want {
		t.Errorf("TimestampShortTime = %q, want %q", got, want)
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
