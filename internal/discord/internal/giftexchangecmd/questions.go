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
	questionModalTitle  = "Answer the question"
	questionModalHint   = "Your answer"
	questionListTitle   = "Your questions"

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
	name, nameErr := h.questionExchangeName(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	_, askErr := h.svc.Ask(e.Ctx, req.Actor, id, text)
	if askErr != nil {
		return fmt.Errorf("ask question: %w", askErr)
	}

	reply := fmt.Sprintf(textAsked, render.Bold(name))
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &reply})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// questionExchangeName returns the name of the exchange. A typed exchange ID may name an
// exchange of another guild, which is not among the candidates; the caller's own list has it.
func (h *Handlers) questionExchangeName(
	ctx context.Context, req *router.Request, id giftexchange.ExchangeID, candidates []giftexchange.ExchangeSummary,
) (string, error) {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return candidate.Name, nil
		}
	}

	listed, err := h.svc.ExchangesFor(ctx, req.Actor, nil)
	if err != nil {
		return "", fmt.Errorf("list exchanges: %w", err)
	}
	for _, summary := range listed {
		if summary.ID == id {
			return summary.Name, nil
		}
	}

	return "", fmt.Errorf("exchange not listed for the caller: %w", giftexchange.ErrNotFound)
}

// answerButton handles the Answer button of a question DM. It opens the answer modal as the
// first response, without reading anything: the modal shows nothing about the question.
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

	input := discord.NewParagraphTextInput(questionAnswerInput)
	input.MinLength = intPtr(1)
	input.MaxLength = giftexchange.MaxQuestionRunes
	input.Required = true
	input.Placeholder = questionModalHint

	modalID := formatCustomID(customID{Action: actionAnswerModal, Question: parsed.Question})
	modal := discord.ModalCreate{
		CustomID: modalID,
		Title:    questionModalTitle,
	}.AddLabel(questionModalHint, input)
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
	embeds := []discord.Embed{render.Embed("", textYourAnswer+"\n"+render.Quote(shown))}
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

	embed := render.Embed(questionListTitle, "")
	embed = render.WithField(embed, textQuestionsAskedField, questionField(questionAskedEntries(asked)))
	embed = render.WithField(embed, textQuestionsReceivedField, questionField(questionReceivedEntries(received)))

	embeds := []discord.Embed{embed}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func questionAskedEntries(asked []giftexchange.AskedQuestion) []string {
	entries := make([]string, 0, len(asked))
	for _, question := range asked {
		entries = append(entries, questionEntry(question.Text, question.Answer, question.ClosedAt != nil))
	}

	return entries
}

func questionReceivedEntries(received []giftexchange.ReceivedQuestion) []string {
	entries := make([]string, 0, len(received))
	for _, question := range received {
		entries = append(entries, questionEntry(question.Text, question.Answer, question.ClosedAt != nil))
	}

	return entries
}

// questionEntry renders one question: its text cut and escaped, the closed marker, and the
// answer on quoted lines when there is one.
func questionEntry(text, answer string, isClosed bool) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(render.Text(render.Truncate(text, questionEntryRunes, truncationMark)))
	if isClosed {
		b.WriteString(textClosedMarker)
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
func questionField(entries []string) string {
	if len(entries) == 0 {
		return textNoneYet
	}

	kept := make([]string, 0, len(entries))
	used := 0
	for i, entry := range entries {
		cost := utf8.RuneCountInString(entry) + 1
		remaining := len(entries) - i - 1
		reserve := 0
		if remaining > 0 {
			reserve = utf8.RuneCountInString(fmt.Sprintf(textAndMore, remaining)) + 1
		}

		fits := used+cost+reserve <= questionFieldRunes
		if !fits && i > 0 {
			kept = append(kept, fmt.Sprintf(textAndMore, len(entries)-i))
			break
		}

		kept = append(kept, entry)
		used += cost
	}

	field := strings.Join(kept, "\n")
	return field
}
