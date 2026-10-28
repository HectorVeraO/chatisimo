package giftexchangecmd

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	questionOptionName = "question"
	// questionAnswerInput is the custom ID of the text input of the answer modal.
	questionAnswerInput = "answer"

	// questionEntryRunes is how much of each question or answer the listing shows.
	questionEntryRunes = 200
	// questionFieldRunes is below Discord's limit of 1024 characters on an embed field.
	questionFieldRunes = 1000
)

func (h *Handlers) registerQuestions(r *router.Router) {
	h.verb(r, "ask", h.askCommand, h.autocompleteFor("ask"))
	h.verb(r, "questions", h.questionsCommand, h.autocompleteFor("questions"))
	r.Component(customIDPattern(actionAnswer), h.answerButton)
	r.Modal(customIDPattern(actionAnswerModal), h.answerModal)
}

// askCommand handles /ge ask. The reply is ephemeral and never names the recipient.
func (h *Handlers) askCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: ask ran without a request")
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	rawExchange, _ := data.OptString(exchangeOptionName)
	text, _ := data.OptString(questionOptionName)

	candidates, err := h.candidates(e.Ctx, req, filterFor("ask"))
	if err != nil {
		return err
	}
	id, err := resolveExchange(rawExchange, candidates)
	if err != nil {
		return err
	}
	req.SetExchange(id)

	// The name is read before the question is committed, so a failed lookup cannot
	// turn the reply into an error after the question is queued.
	summary, nameErr := h.questionExchangeSummary(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	_, askErr := h.svc.Ask(e.Ctx, req.Actor, id, text)
	if askErr != nil {
		return fmt.Errorf("ask question: %w", askErr)
	}

	c := h.catalogFor(req)
	reply := c.Format(textAsked, render.Bold(summary.Name))
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &reply})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// questionExchangeSummary returns the summary of the exchange and records its guild locale on req.
// A typed exchange ID may name an exchange of another guild, which is not among the
// candidates; the caller's own list has it.
func (h *Handlers) questionExchangeSummary(
	ctx context.Context, req *router.Request, id giftexchange.ExchangeID, candidates []giftexchange.ExchangeSummary,
) (giftexchange.ExchangeSummary, error) {
	for _, candidate := range candidates {
		if candidate.ID == id {
			h.setGroupLocale(req, candidate.Group.Subject)
			return candidate, nil
		}
	}

	listed, err := h.svc.ExchangesFor(ctx, req.Actor, nil)
	if err != nil {
		return giftexchange.ExchangeSummary{}, fmt.Errorf("list exchanges: %w", err)
	}
	for _, summary := range listed {
		if summary.ID == id {
			h.setGroupLocale(req, summary.Group.Subject)
			return summary, nil
		}
	}

	if req.Group != nil {
		view, viewErr := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
		if viewErr == nil {
			h.setGroupLocale(req, view.Group.Subject)
		}
	}

	return giftexchange.ExchangeSummary{}, fmt.Errorf("exchange not listed for the caller: %w", giftexchange.ErrNotFound)
}

func (h *Handlers) questionCatalog(
	ctx context.Context, req *router.Request, questionID giftexchange.QuestionID,
) Catalog {
	listed, err := h.svc.ExchangesFor(ctx, req.Actor, req.Group)
	if err != nil {
		return h.catalogFor(req)
	}
	for _, summary := range listed {
		received, recvErr := h.svc.QuestionsReceived(ctx, req.Actor, summary.ID)
		if recvErr != nil {
			continue
		}
		for _, q := range received {
			if q.ID == questionID {
				h.setGroupLocale(req, summary.Group.Subject)
				return h.catalogFor(req)
			}
		}
	}
	if len(listed) > 0 {
		h.setGroupLocale(req, listed[0].Group.Subject)
	}
	return h.catalogFor(req)
}

// answerButton handles the Answer button of a question DM. It opens the answer modal as the
// first response: the modal shows nothing about the question.
func (h *Handlers) answerButton(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: answer button ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionAnswer {
		return errBadCustomID
	}

	c := h.questionCatalog(e.Ctx, req, parsed.Question)
	hint := c.Get(textQuestionModalHint)
	input := discord.NewParagraphTextInput(questionAnswerInput)
	input.MinLength = intPtr(1)
	input.MaxLength = giftexchange.MaxQuestionRunes
	input.Required = true
	input.Placeholder = hint

	modalID := formatCustomID(customID{Action: actionAnswerModal, Question: parsed.Question})
	modal := discord.ModalCreate{
		CustomID: modalID,
		Title:    c.Get(textQuestionModalTitle),
	}.AddLabel(hint, input)
	modalErr := req.Modal(e.Ctx, modal)
	if modalErr != nil {
		return modalErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// answerModal handles the submit of the answer modal. It acknowledges with a deferred update
// before calling the module and edits the question DM only after the answer was saved, so a
// failure reaches the caller as an ephemeral followup and leaves the DM as it was.
func (h *Handlers) answerModal(e *handler.ModalEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: answer modal ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID)
	if err != nil {
		return err
	}
	if parsed.Action != actionAnswerModal {
		return errBadCustomID
	}

	c := h.questionCatalog(e.Ctx, req, parsed.Question)

	deferErr := req.DeferUpdate(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	text := e.Data.Text(questionAnswerInput)
	answerErr := h.svc.Answer(e.Ctx, req.Actor, parsed.Question, text)
	if answerErr != nil {
		return fmt.Errorf("answer question: %w", answerErr)
	}

	// The answer goes in an embed, whose room (4096) holds any answer (at most 1000 runes,
	// at most doubled by escaping), so the question text of the DM is never cut for it.
	shown := render.Truncate(strings.TrimSpace(text), giftexchange.MaxQuestionRunes, truncationMark)
	embeds := []discord.Embed{render.Embed("", c.Get(textYourAnswer)+"\n"+render.Quote(shown))}
	components := []discord.LayoutComponent{}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds, Components: &components})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// questionsCommand handles /ge questions: the caller's asked and received questions of one
// exchange, in an ephemeral embed with two fields.
func (h *Handlers) questionsCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: questions ran without a request")
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "questions", raw)
	if err != nil {
		return err
	}

	asked, err := h.svc.QuestionsAsked(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("list asked questions: %w", err)
	}
	received, err := h.svc.QuestionsReceived(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("list received questions: %w", err)
	}

	c := h.catalogFor(req)
	embed := render.Embed(c.Get(textQuestionListTitle), "")
	embed = render.WithField(embed, c.Get(textQuestionsAskedField), questionField(c, questionAskedEntries(c, asked)))
	embed = render.WithField(embed, c.Get(textQuestionsReceivedField), questionField(c, questionReceivedEntries(c, received)))

	embeds := []discord.Embed{embed}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func questionAskedEntries(c Catalog, asked []giftexchange.AskedQuestion) []string {
	entries := make([]string, 0, len(asked))
	for _, question := range asked {
		entries = append(entries, questionEntry(c, question.Text, question.Answer, question.ClosedAt != nil))
	}

	return entries
}

func questionReceivedEntries(c Catalog, received []giftexchange.ReceivedQuestion) []string {
	entries := make([]string, 0, len(received))
	for _, question := range received {
		entries = append(entries, questionEntry(c, question.Text, question.Answer, question.ClosedAt != nil))
	}

	return entries
}

// questionEntry renders one question: its text cut and escaped, the closed marker, and the
// answer on quoted lines when there is one.
func questionEntry(c Catalog, text, answer string, isClosed bool) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(render.Text(render.Truncate(text, questionEntryRunes, truncationMark)))
	if isClosed {
		b.WriteString(c.Get(textClosedMarker))
	}
	if answer != "" {
		cut := render.Truncate(answer, questionEntryRunes, truncationMark)
		b.WriteString("\n")
		b.WriteString(render.Quote(cut))
	}

	entry := b.String()
	return entry
}

// questionField joins entries into the value of one embed field. It keeps whole entries while
// they fit with the closing "and n more" line, and ends with that line for the rest. An empty
// list reads "None yet.". The first entry always fits: it is at most two cut and escaped texts.
func questionField(c Catalog, entries []string) string {
	if len(entries) == 0 {
		return c.Get(textNoneYet)
	}

	kept := make([]string, 0, len(entries))
	used := 0
	for i, entry := range entries {
		cost := utf8.RuneCountInString(entry) + 1
		remaining := len(entries) - i - 1
		reserve := 0
		if remaining > 0 {
			reserve = utf8.RuneCountInString(c.Format(textAndMore, remaining)) + 1
		}

		fits := used+cost+reserve <= questionFieldRunes
		if !fits && i > 0 {
			kept = append(kept, c.Format(textAndMore, len(entries)-i))
			break
		}

		kept = append(kept, entry)
		used += cost
	}

	field := strings.Join(kept, "\n")
	return field
}
