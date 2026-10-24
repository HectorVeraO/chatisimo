// Package render builds the pieces of Discord messages that carry user-supplied text:
// escaping, mentions, timestamps, truncation, quotes and embeds. Every string that a person
// typed goes through Text before it is shown back.
package render

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// maxSnowflakeDigits is the length of the largest 64-bit unsigned decimal.
const maxSnowflakeDigits = 20

// ErrInvalidSnowflake is returned for an ID that is not a decimal Discord snowflake.
var ErrInvalidSnowflake = errors.New("render: invalid snowflake")

// escapedRunes are the characters Text puts a backslash in front of: Discord's markdown
// controls, the angle bracket that opens mentions and channel links, the at sign that opens
// @everyone and @here, and the brackets of masked links.
const escapedRunes = "\\*_~|>#`<@[]"

// Text escapes s so Discord shows it literally: no markdown, no mention, no link. Invalid
// UTF-8 bytes become U+FFFD.
func Text(s string) string {
	valid := strings.ToValidUTF8(s, string(utf8.RuneError))

	var b strings.Builder
	b.Grow(len(valid))
	for _, r := range valid {
		if strings.ContainsRune(escapedRunes, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}

	escaped := b.String()
	return escaped
}

// Bold returns s escaped and set in bold.
func Bold(s string) string {
	bold := "**" + Text(s) + "**"
	return bold
}

// Quote escapes s and turns every line of it into a block quote line.
func Quote(s string) string {
	lines := strings.Split(Text(s), "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}

	quoted := strings.Join(lines, "\n")
	return quoted
}

// Mention returns the user mention of the decimal snowflake id.
func Mention(id string) (string, error) {
	isSnowflake := isDecimalID(id)
	if !isSnowflake {
		return "", ErrInvalidSnowflake
	}

	mention := "<@" + id + ">"
	return mention, nil
}

func isDecimalID(id string) bool {
	hasValidLength := len(id) > 0 && len(id) <= maxSnowflakeDigits
	if !hasValidLength {
		return false
	}

	_, err := strconv.ParseUint(id, 10, 64)
	isNumber := err == nil
	return isNumber
}

// TimestampFull returns Discord's full date-time markup for t.
func TimestampFull(t time.Time) string {
	markup := fmt.Sprintf("<t:%d:F>", t.Unix())
	return markup
}

// TimestampRelative returns Discord's relative-time markup for t ("in 2 hours").
func TimestampRelative(t time.Time) string {
	markup := fmt.Sprintf("<t:%d:R>", t.Unix())
	return markup
}

// Truncate shortens s to at most maxRunes runes. When it cuts, the result ends in suffix and
// still fits in maxRunes. A suffix longer than maxRunes is dropped.
func Truncate(s string, maxRunes int, suffix string) string {
	if maxRunes < 0 {
		maxRunes = 0
	}

	valid := strings.ToValidUTF8(s, string(utf8.RuneError))
	if utf8.RuneCountInString(valid) <= maxRunes {
		return valid
	}

	room := maxRunes - utf8.RuneCountInString(suffix)
	if room < 0 {
		suffix = ""
		room = maxRunes
	}

	runes := []rune(valid)
	cut := string(runes[:room]) + suffix
	return cut
}

// Embed returns an embed with a title and a description.
func Embed(title, description string) discord.Embed {
	embed := discord.Embed{Title: title, Description: description}
	return embed
}

// WithFooter returns e with the footer text set.
func WithFooter(e discord.Embed, text string) discord.Embed {
	e.Footer = &discord.EmbedFooter{Text: text}
	return e
}

// WithField returns e with one more field.
func WithField(e discord.Embed, name, value string) discord.Embed {
	fields := make([]discord.EmbedField, 0, len(e.Fields)+1)
	fields = append(fields, e.Fields...)
	fields = append(fields, discord.EmbedField{Name: name, Value: value})
	e.Fields = fields
	return e
}

// NoMentions returns the allowed_mentions value that pings nobody. Its slices are empty, not
// nil, because a nil slice marshals as null and Discord then falls back to its defaults.
func NoMentions() discord.AllowedMentions {
	none := discord.AllowedMentions{
		Parse:       []discord.AllowedMentionType{},
		Roles:       []snowflake.ID{},
		Users:       []snowflake.ID{},
		RepliedUser: false,
	}
	return none
}
