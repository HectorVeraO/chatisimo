package config_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

func TestParseInput(t *testing.T) {
	t.Parallel()

	maxValue := strings.Repeat("a", config.MaxInputBytes)

	cases := []struct {
		name   string
		raw    string
		secret bool
		want   string
		reject bool
	}{
		{name: "accepts a plain value", raw: "value", want: "value"},
		{name: "strips one trailing newline", raw: "value\n", want: "value"},
		{name: "strips one trailing CRLF", raw: "value\r\n", want: "value"},
		{name: "rejects a second newline", raw: "a\n\n", reject: true},
		{name: "rejects a second CRLF", raw: "a\r\n\r\n", reject: true},
		{name: "rejects a lone carriage return", raw: "a\r", reject: true},
		{name: "rejects empty", raw: "", reject: true},
		{name: "rejects only a newline", raw: "\n", reject: true},
		{name: "rejects invalid UTF-8", raw: "a\xff\xfeb", reject: true},
		{name: "rejects NUL", raw: "a\x00b", reject: true},
		{name: "rejects ESC", raw: "a\x1bb", reject: true},
		{name: "rejects tab", raw: "a\tb", reject: true},
		{name: "rejects a C1 control", raw: "a\u0085b", reject: true},
		{name: "plain value may hold spaces", raw: "a b c", want: "a b c"},
		{name: "secret rejects a space", raw: "a b", secret: true, reject: true},
		{name: "secret rejects a no-break space", raw: "a\u00a0b", secret: true, reject: true},
		{name: "secret accepts a compact value", raw: "tok.en-1_2", secret: true, want: "tok.en-1_2"},
		{name: "accepts combining marks, RTL and emoji", raw: "e\u0301 \u05d0\u200b\U0001F600", want: "e\u0301 \u05d0\u200b\U0001F600"},
		{name: "accepts mention syntax as text", raw: "<@123456789012345678>", want: "<@123456789012345678>"},
		{name: "accepts exactly MaxInputBytes", raw: maxValue, want: maxValue},
		{name: "accepts MaxInputBytes plus newline", raw: maxValue + "\n", want: maxValue},
		{name: "rejects one byte past MaxInputBytes", raw: maxValue + "a", reject: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.ParseInput([]byte(tc.raw), tc.secret)

			if tc.reject {
				if err == nil {
					t.Fatalf("accepted %q", got)
				}

				if !errors.Is(err, config.ErrInvalidValue) {
					t.Errorf("error does not wrap ErrInvalidValue: %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("rejected: %v", err)
			}

			if got != tc.want {
				t.Error("value differs from the want")
			}
		})
	}
}

func TestParseInputErrorsNeverEchoInput(t *testing.T) {
	t.Parallel()

	_, err := config.ParseInput([]byte("leaky-marker\x00 value"), true)
	if err == nil {
		t.Fatal("accepted a bad value")
	}

	if strings.Contains(err.Error(), "leaky-marker") {
		t.Errorf("error echoes the input: %v", err)
	}
}

func FuzzParseInput(f *testing.F) {
	seeds := []string{
		"", "\n", "\r\n", "a\n\n", "token\n", "a b", "a\tb", "a\x00b", "\xff\xfe",
		strings.Repeat("x", config.MaxInputBytes), strings.Repeat("x", config.MaxInputBytes+5),
		"e\u0301\u0301\u0301", "\u202eevil", "a\u200bb", "\U0001F600\U0001F468\u200d\U0001F469",
		"<@123456789012345678>", "@everyone", "<#123>", "\x1b[31mred",
	}

	for _, seed := range seeds {
		f.Add([]byte(seed), true)
		f.Add([]byte(seed), false)
	}

	f.Fuzz(func(t *testing.T, raw []byte, secret bool) {
		got, err := config.ParseInput(raw, secret)
		if err != nil {
			return
		}

		if got == "" {
			t.Fatal("accepted an empty value")
		}

		if !utf8.ValidString(got) {
			t.Fatal("accepted invalid UTF-8")
		}

		if !strings.HasPrefix(string(raw), got) {
			t.Fatal("output is not a prefix of the input")
		}

		for _, r := range got {
			if unicode.IsControl(r) {
				t.Fatalf("accepted control character %U", r)
			}

			if secret && unicode.IsSpace(r) {
				t.Fatalf("secret accepted white space %U", r)
			}
		}
	})
}

func TestParseMultilineInput(t *testing.T) {
	t.Parallel()

	maxValue := strings.Repeat("a", config.MaxInputBytes)

	cases := []struct {
		name   string
		raw    string
		want   string
		reject bool
	}{
		{name: "accepts a single-line value", raw: "single line\n", want: "single line"},
		{name: "preserves interior LF newlines", raw: "line 1\nline 2\nline 3\n", want: "line 1\nline 2\nline 3"},
		{name: "normalizes interior CRLF to LF and strips trailing CRLF", raw: "line 1\r\nline 2\r\nline 3\r\n", want: "line 1\nline 2\nline 3"},
		{name: "preserves interior blank line", raw: "paragraph 1\n\nparagraph 2\n", want: "paragraph 1\n\nparagraph 2"},
		{name: "accepts UTF-8 multiline text", raw: "l\u00ednea 1\nl\u00ednea 2\n", want: "l\u00ednea 1\nl\u00ednea 2"},
		{name: "rejects empty", raw: "", reject: true},
		{name: "rejects only a newline", raw: "\n", reject: true},
		{name: "rejects only two newlines", raw: "\n\n", reject: true},
		{name: "rejects whitespace and newlines only", raw: "  \n  \r\n", reject: true},
		{name: "rejects a lone carriage return", raw: "line 1\rline 2\n", reject: true},
		{name: "rejects a trailing lone carriage return before CRLF", raw: "line 1\r\r\n", reject: true},
		{name: "rejects invalid UTF-8", raw: "line 1\n\xff\xfe\n", reject: true},
		{name: "rejects NUL", raw: "line 1\n\x00line 2", reject: true},
		{name: "rejects ESC", raw: "line 1\n\x1bline 2", reject: true},
		{name: "rejects tab", raw: "line 1\n\tline 2", reject: true},
		{name: "rejects C1 control", raw: "line 1\n\u0085line 2", reject: true},
		{name: "accepts MaxInputBytes plus newline", raw: maxValue + "\n", want: maxValue},
		{name: "rejects one byte past MaxInputBytes", raw: maxValue + "a", reject: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.ParseMultilineInput([]byte(tc.raw))

			if tc.reject {
				if err == nil {
					t.Fatalf("accepted %q", got)
				}

				if !errors.Is(err, config.ErrInvalidValue) {
					t.Errorf("error does not wrap ErrInvalidValue: %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("rejected: %v", err)
			}

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("secret rejects newline even when multiline is true", func(t *testing.T) {
		t.Parallel()

		_, err := config.ParseKeyInput([]byte("secret1\nsecret2\n"), true, true)
		if !errors.Is(err, config.ErrInvalidValue) {
			t.Fatalf("ParseKeyInput(secret=true, multiline=true) = %v, want ErrInvalidValue", err)
		}
	})
}

func FuzzParseMultilineInput(f *testing.F) {
	seeds := []string{
		"", "\n", "\r\n", "\n\n", "  \n  ", "line 1\nline 2\n", "line 1\r\nline 2\r\n",
		"line 1\rline 2", "a\tb", "a\x00b", "\xff\xfe",
		strings.Repeat("x", config.MaxInputBytes), strings.Repeat("x", config.MaxInputBytes+5),
		"e\u0301\u0301\u0301\nl\u00ednea", "\u202eevil", "a\u200bb", "\U0001F600\U0001F468\u200d\U0001F469",
		"<@123456789012345678>", "@everyone", "<#123>", "\x1b[31mred",
	}

	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := config.ParseMultilineInput(raw)
		if err != nil {
			return
		}

		if strings.TrimSpace(got) == "" {
			t.Fatal("accepted an empty or blank value")
		}

		if len(got) > config.MaxInputBytes {
			t.Fatalf("accepted value of %d bytes > MaxInputBytes", len(got))
		}

		if !utf8.ValidString(got) {
			t.Fatal("accepted invalid UTF-8")
		}

		if strings.Contains(got, "\r") {
			t.Fatal("accepted a carriage return")
		}

		for _, r := range got {
			if r != '\n' && unicode.IsControl(r) {
				t.Fatalf("accepted control character %U", r)
			}
		}
	})
}
