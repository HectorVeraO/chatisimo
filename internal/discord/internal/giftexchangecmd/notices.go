package giftexchangecmd

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	// maxContentRunes is Discord's limit for the content of a message.
	maxContentRunes = 2000
	// truncationMark ends a quoted text that was cut to fit the message.
	truncationMark = "..."
)

// ErrCannotRender is returned for a notification that cannot become a message: an unknown
// kind, or a kind missing the data it needs.
var ErrCannotRender = errors.New("giftexchangecmd: cannot render notification")

// RenderNotification builds the direct message for n in English (LocaleENUS). guildName is the
// name of the server the exchange belongs to; an empty name renders as "unknown server". It is
// pure: the pair mention comes only from n.Counterpart and the question text only from
// n.Question.
func RenderNotification(n giftexchange.Notification, guildName string) (discord.MessageCreate, error) {
	return RenderNotificationIn(LocaleENUS, n, guildName)
}

// RenderNotificationIn builds the direct message for n in the catalog for loc, falling back to
// English when loc is empty or unsupported.
func RenderNotificationIn(loc Locale, n giftexchange.Notification, guildName string) (discord.MessageCreate, error) {
	c := CatalogFor(loc)
	exchange := render.Bold(n.Exchange.Name)
	guild := render.Text(guildName)
	if guildName == "" {
		guild = c.Get(textUnknownServer)
	}

	body, err := buildBody(c, n, exchange, guild)
	if err != nil {
		return discord.MessageCreate{}, fmt.Errorf("%w: kind %q: %w", ErrCannotRender, n.Kind, err)
	}

	message := body.message(c)
	return message, nil
}

// noticeBody is what a kind contributes to its message.
type noticeBody struct {
	content    string
	embed      *discord.Embed
	components []discord.LayoutComponent
}

// message assembles the final message: the footer goes on the embed when there is one and
// at the end of the content otherwise, and nobody is pinged.
func (b noticeBody) message(c Catalog) discord.MessageCreate {
	footer := c.Get(textDMFooter)
	content := b.content
	var embeds []discord.Embed
	if b.embed != nil {
		embeds = []discord.Embed{render.WithFooter(*b.embed, footer)}
	} else {
		content += "\n-# " + footer
	}

	mentions := render.NoMentions()
	message := discord.MessageCreate{
		Content:         content,
		Embeds:          embeds,
		Components:      b.components,
		AllowedMentions: &mentions,
	}
	return message
}

func buildBody(c Catalog, n giftexchange.Notification, exchange, guild string) (noticeBody, error) {
	switch n.Kind {
	case giftexchange.KindDrawn:
		return pairBody(
			c,
			n,
			c.Format(textDMDrawn, exchange, guild),
			c.Get(textTitleRecipient),
			withDrawn,
		)
	case giftexchange.KindRedrawn:
		return pairBody(
			c,
			n,
			c.Format(textDMRedrawn, exchange, guild),
			c.Get(textTitleNewRecipient),
			withRecipientPreferences,
		)
	case giftexchange.KindAssignmentChanged:
		return pairBody(
			c,
			n,
			c.Format(textDMChanged, exchange, guild),
			c.Get(textTitleNewRecipient),
			withRecipientPreferences,
		)
	case giftexchange.KindPreferencesUpdated:
		return pairBody(
			c,
			n,
			c.Format(textDMPreferencesUpdated, exchange, guild),
			c.Get(textTitleRecipient),
			withUpdatedPreferences,
		)
	case giftexchange.KindRevealSoon:
		reveal, err := relativeTime(n.Exchange.RevealAt, "reveal time")
		if err != nil {
			return noticeBody{}, err
		}
		return pairBody(
			c,
			n,
			c.Format(textDMRevealSoon, exchange, guild, reveal),
			c.Get(textTitleRecipient),
			nil,
		)
	case giftexchange.KindRevealed:
		return pairBody(
			c,
			n,
			c.Format(textDMRevealed, exchange, guild),
			c.Get(textTitleGiver),
			nil,
		)
	case giftexchange.KindRoundVoided:
		return noticeBody{content: c.Format(textDMVoided, exchange, guild)}, nil
	case giftexchange.KindDrawFailed:
		return drawFailedBody(c, n, exchange, guild)
	case giftexchange.KindDrawSoon:
		draw, err := relativeTime(n.Exchange.DrawAt, "draw time")
		if err != nil {
			return noticeBody{}, err
		}
		return noticeBody{content: c.Format(textDMDrawSoon, exchange, guild, draw)}, nil
	case giftexchange.KindCancelled:
		return noticeBody{content: c.Format(textDMCancelled, exchange, guild)}, nil
	case giftexchange.KindQuestionReceived:
		return questionBody(c, n, exchange, guild)
	case giftexchange.KindAnswerReceived:
		return answerBody(c, n, exchange, guild)
	default:
		return noticeBody{}, errors.New("unknown kind")
	}
}

// withReveal adds the Reveal field of a drawn notice.
func withReveal(c Catalog, embed discord.Embed, n giftexchange.Notification) discord.Embed {
	value := c.Get(textManual)
	if n.Exchange.RevealAt != nil {
		value = render.TimestampFull(*n.Exchange.RevealAt)
	}

	withField := render.WithField(embed, c.Get(textFieldReveal), value)
	return withField
}

func withDrawn(c Catalog, embed discord.Embed, n giftexchange.Notification) discord.Embed {
	withRevealField := withReveal(c, embed, n)
	return withPreferences(c, withRevealField, n.Preferences)
}

func withRecipientPreferences(c Catalog, embed discord.Embed, n giftexchange.Notification) discord.Embed {
	return withPreferences(c, embed, n.Preferences)
}

func withUpdatedPreferences(c Catalog, embed discord.Embed, n giftexchange.Notification) discord.Embed {
	if n.Preferences.IsEmpty() {
		return render.WithField(embed, c.Get(textFieldPreferences), c.Get(textNoneYet))
	}
	return withPreferences(c, embed, n.Preferences)
}

// pairBody builds a notice whose embed names the addressee's counterpart.
func pairBody(
	c Catalog,
	n giftexchange.Notification,
	content, title string,
	decorate func(Catalog, discord.Embed, giftexchange.Notification) discord.Embed,
) (noticeBody, error) {
	if n.Counterpart == nil {
		return noticeBody{}, errors.New("missing counterpart")
	}

	mention, err := discordMention(*n.Counterpart)
	if err != nil {
		return noticeBody{}, err
	}

	embed := render.Embed(title, mention)
	if decorate != nil {
		embed = decorate(c, embed, n)
	}

	return noticeBody{content: content, embed: &embed}, nil
}

// discordMention returns the mention of the person's Discord identity.
func discordMention(p giftexchange.PersonRef) (string, error) {
	for _, identity := range p.Identities {
		if identity.Provider != giftexchange.ProviderDiscord {
			continue
		}

		mention, err := render.Mention(identity.Subject)
		if err != nil {
			return "", fmt.Errorf("discord identity: %w", err)
		}
		return mention, nil
	}

	return "", errors.New("no discord identity")
}

func relativeTime(t *time.Time, what string) (string, error) {
	if t == nil {
		return "", fmt.Errorf("missing %s", what)
	}

	markup := render.TimestampRelative(*t)
	return markup, nil
}

func drawFailedBody(c Catalog, n giftexchange.Notification, exchange, guild string) (noticeBody, error) {
	var reason string
	switch n.Reason {
	case giftexchange.ReasonTooFewParticipants:
		reason = c.Get(textReasonTooFew)
	case giftexchange.ReasonNoValidAssignment:
		reason = c.Get(textReasonNoValid)
	default:
		return noticeBody{}, errors.New("missing or unknown reason")
	}

	return noticeBody{content: c.Format(textDMDrawFailed, exchange, guild, reason)}, nil
}

// questionBody builds the notice of a received question. It names no asker.
func questionBody(c Catalog, n giftexchange.Notification, exchange, guild string) (noticeBody, error) {
	if n.Question == nil {
		return noticeBody{}, errors.New("missing question")
	}

	header := c.Format(textDMQuestion, exchange, guild)
	content := fitQuotes(c, header, n.Question.Text)
	answer := discord.NewActionRow(
		discord.NewPrimaryButton(c.Get(textButtonAnswer), customIDAnswerPrefix+n.Question.ID.String()),
	)

	return noticeBody{content: content, components: []discord.LayoutComponent{answer}}, nil
}

// answerBody builds the notice of an answered question: the question and its answer.
func answerBody(c Catalog, n giftexchange.Notification, exchange, guild string) (noticeBody, error) {
	if n.Question == nil {
		return noticeBody{}, errors.New("missing question")
	}

	header := c.Format(textDMAnswer, exchange, guild)
	content := fitQuotes(c, header, n.Question.Text, n.Question.Answer)
	return noticeBody{content: content}, nil
}

// fitQuotes joins header and the quoted texts, cutting each text to a smaller budget until
// the whole fits in a Discord message together with the footer line.
func fitQuotes(c Catalog, header string, texts ...string) string {
	// The rune limits tried, in order, for each quoted text.
	quoteBudgets := []int{1000, 500, 250, 120, 60}
	footerRunes := utf8.RuneCountInString("\n-# " + c.Get(textDMFooter))

	var content string
	for _, budget := range quoteBudgets {
		parts := []string{header}
		for _, text := range texts {
			parts = append(parts, render.Quote(render.Truncate(text, budget, truncationMark)))
		}

		content = strings.Join(parts, "\n")
		fits := utf8.RuneCountInString(content)+footerRunes <= maxContentRunes
		if fits {
			return content
		}
	}

	return content
}
