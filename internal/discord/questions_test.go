//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	questionTea        = "Do you like tea?"
	questionAnswerTea  = "Yes, green tea."
	questionExchange   = "Winter"
	questionCallbackUp = 6
	questionCallbackMo = 9

	questionTextAsked      = "Your question was sent anonymously to your recipient in **Winter**."
	questionTextOff        = "The organizer turned off questions for this exchange."
	questionTextLimit      = "You reached the limit of 20 questions per day for this exchange."
	questionTextClosed     = "This question is closed and can no longer be answered."
	questionTextAnswered   = "You already answered this question."
	questionTextNotSaved   = "Not saved:"
	questionTextNoExchange = "You have no exchange where you can do this."
)

var questionIDPattern = regexp.MustCompile(`/gx/answer/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// questionScene is a drawn exchange of three users (a, b and c) whose notices are delivered.
type questionScene struct {
	*scene
	exchange giftexchange.ExchangeID
}

func newQuestionScene(t *testing.T) *questionScene {
	t.Helper()

	s := newScene(t)
	s.startRunner(t, s.world)
	id := s.drawnExchange(t, questionExchange)
	waitFor(t, "the three draw DMs", func() bool {
		return finishedCount(t, id, "delivered") == 3
	})

	return &questionScene{scene: s, exchange: id}
}

// recipientOf returns the snowflake of the giver's recipient.
func (q *questionScene) recipientOf(t *testing.T, giver string) string {
	t.Helper()

	assignment, err := q.svc.MyAssignment(t.Context(), q.people[giver].actor, q.exchange)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	for _, identity := range assignment.Recipient.Identities {
		return identity.Subject
	}

	t.Fatal("the recipient has no identity")
	return ""
}

func (q *questionScene) askSlash(inGuild bool, text string) fakediscord.Slash {
	in := q.ge("ask", inGuild, map[string]any{"question": text})
	in.BypassValidation = true
	return in
}

func (q *questionScene) ask(t *testing.T, giver string, inGuild bool, text string) fakediscord.Result {
	t.Helper()

	result := slashOrFail(t, q.harness, giver, q.askSlash(inGuild, text))
	q.outcome(t, result)
	return result
}

// questionDM waits for the question DM of user whose content holds needle.
func (q *questionScene) questionDM(t *testing.T, user, needle string) fakediscord.Message {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()

	message, err := q.fake.WaitDM(ctx, q.users[user], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, needle) && len(m.Components) > 0
	})
	if err != nil {
		t.Fatalf("WaitDM for %q: %v", needle, err)
	}

	return message
}

// dmByID returns the current version of a DM.
func (q *questionScene) dmByID(t *testing.T, user, id string) fakediscord.Message {
	t.Helper()

	for _, message := range q.fake.DMs(q.users[user]) {
		if message.ID == id {
			return message
		}
	}

	t.Fatalf("DM %s of the user is gone", id)
	return fakediscord.Message{}
}

func questionIDOf(t *testing.T, message fakediscord.Message) string {
	t.Helper()

	var all strings.Builder
	for _, component := range message.Components {
		all.Write(component)
	}
	match := questionIDPattern.FindStringSubmatch(all.String())
	if match == nil {
		t.Fatalf("message has no Answer button: %s", all.String())
	}

	return match[1]
}

// answerModal presses the Answer button of the DM and returns the modal it opens.
func (q *questionScene) answerModal(t *testing.T, user string, dm fakediscord.Message) fakediscord.Result {
	t.Helper()

	customID := "/gx/answer/" + questionIDOf(t, dm)
	result, err := q.fake.ClickForged(t.Context(), q.users[user], dm, customID)
	if err != nil {
		t.Fatalf("click Answer: %v", err)
	}
	if result.CallbackType != questionCallbackMo || result.Modal == nil {
		t.Fatalf("the Answer button answered type %d, want a modal", result.CallbackType)
	}

	return result
}

func (q *questionScene) submit(t *testing.T, user string, modal fakediscord.Modal, text string) fakediscord.Result {
	t.Helper()

	result, err := q.fake.SubmitModalForged(t.Context(), q.users[user], modal, map[string]string{"answer": text})
	if err != nil {
		t.Fatalf("submit modal: %v", err)
	}

	return result
}

// outcome waits for the edit or the followup the bot sent for the interaction.
func (q *questionScene) outcome(t *testing.T, result fakediscord.Result) fakediscord.Record {
	t.Helper()

	var found fakediscord.Record
	waitFor(t, "the edit or followup of the interaction", func() bool {
		for _, record := range q.fake.Outbound() {
			isOurs := record.InteractionID != nil && *record.InteractionID == result.InteractionID
			isReply := record.Source == "edit" || record.Source == "followup"
			if isOurs && isReply {
				found = record
				return true
			}
		}

		return false
	})

	return found
}

func questionEmbeds(t *testing.T, record fakediscord.Record) []discord.Embed {
	t.Helper()

	var embeds []discord.Embed
	for _, raw := range record.Embeds {
		var embed discord.Embed
		err := json.Unmarshal(raw, &embed)
		if err != nil {
			t.Fatalf("decode embed: %v", err)
		}
		embeds = append(embeds, embed)
	}

	return embeds
}

// questionFields returns the fields of the one embed of a record by name.
func questionFields(t *testing.T, record fakediscord.Record) map[string]string {
	t.Helper()

	embeds := questionEmbeds(t, record)
	if len(embeds) != 1 {
		t.Fatalf("record has %d embeds, want 1", len(embeds))
	}
	fields := map[string]string{}
	for _, field := range embeds[0].Fields {
		fields[field.Name] = field.Value
	}

	return fields
}

// questionTextOf is everything a person reading a message sees as text.
func questionTextOf(message fakediscord.Message) string {
	text := message.Content
	for _, embed := range message.Embeds {
		text += string(embed)
	}

	return text
}

func (q *questionScene) questionsReceived(t *testing.T, user string) []giftexchange.ReceivedQuestion {
	t.Helper()

	received, err := q.svc.QuestionsReceived(t.Context(), q.people[user].actor, q.exchange)
	if err != nil {
		t.Fatalf("QuestionsReceived: %v", err)
	}

	return received
}

// assertNoAsker requires every message visible to recipient after the first n records to
// hold neither the asker's snowflake nor the asker's name.
func (q *questionScene) assertNoAsker(t *testing.T, since int, asker, recipient string) {
	t.Helper()

	name := q.users[asker].Name
	for _, record := range q.fake.Outbound()[since:] {
		if !slices.Contains(record.VisibleTo, recipient) {
			continue
		}

		text := record.Content
		for _, embed := range record.Embeds {
			text += string(embed)
		}
		for _, component := range record.Components {
			text += string(component)
		}
		if strings.Contains(text, asker) || strings.Contains(text, name) || slices.Contains(record.MentionsRendered, asker) {
			t.Errorf("record %d visible to the recipient names the asker: %s", record.Seq, text)
		}
	}
}

// assertLogsClean requires the captured logs to hold no typed text and no snowflake.
func (q *questionScene) assertLogsClean(t *testing.T, typed ...string) {
	t.Helper()

	logs := q.logs.String()
	for _, needle := range append([]string{questionExchange}, typed...) {
		if needle != "" && strings.Contains(logs, needle) {
			t.Errorf("logs hold %q", needle)
		}
	}
	if found := snowflakePattern.FindString(logs); found != "" {
		t.Errorf("logs hold a snowflake: %s", found)
	}
}

func TestQuestionAskInAGuildSendsTheQuestionAnonymously(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	before := len(q.fake.Outbound())

	result := q.ask(t, q.a, true, questionTea)

	got := replyTo(t, q.harness, result)
	if got.content != questionTextAsked || got.ephemeralTo != q.a {
		t.Errorf("reply = %+v, want the Asked text visible only to the asker", got)
	}
	if strings.Contains(got.content, recipient) || strings.Contains(got.content, q.users[recipient].Name) {
		t.Errorf("the reply names the recipient: %q", got.content)
	}
	dm := q.questionDM(t, recipient, questionTea)
	if questionIDOf(t, dm) == "" {
		t.Error("the DM has no Answer button")
	}
	q.assertNoAsker(t, before, q.a, recipient)
	assertNoPings(t, q.fake)
	q.assertLogsClean(t, questionTea)
}

func TestQuestionAskInTheBotDMWorksToo(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.b)

	result := q.ask(t, q.b, false, questionTea)

	if got := replyTo(t, q.harness, result); got.content != questionTextAsked {
		t.Errorf("reply = %q, want %q", got.content, questionTextAsked)
	}
	q.questionDM(t, recipient, questionTea)
}

func TestQuestionAnswerEditsTheDMAndTellsTheGiver(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)

	opened := q.answerModal(t, recipient, dm)
	input := string(opened.Modal.Components[0])
	for _, want := range []string{`"custom_id":"answer"`, `"max_length":1000`, `"min_length":1`, `"style":2`} {
		if !strings.Contains(input, want) {
			t.Errorf("modal input lacks %s: %s", want, input)
		}
	}
	if opened.Modal.CustomID != "/gx/answer-modal/"+questionIDOf(t, dm) {
		t.Errorf("modal custom ID = %q", opened.Modal.CustomID)
	}
	submitted := q.submit(t, recipient, *opened.Modal, questionAnswerTea)

	if submitted.CallbackType != questionCallbackUp {
		t.Errorf("submit answered type %d, want 6", submitted.CallbackType)
	}
	edit := q.outcome(t, submitted)
	if edit.Source != "edit" {
		t.Fatalf("outcome source = %q, want edit", edit.Source)
	}
	after := q.dmByID(t, recipient, dm.ID)
	text := questionTextOf(after)
	if !strings.Contains(text, questionTea) || !strings.Contains(text, "Your answer:") || !strings.Contains(text, questionAnswerTea) {
		t.Errorf("edited DM = %q, want the question and the answer", text)
	}
	if len(after.Components) != 0 {
		t.Errorf("the edited DM keeps its components: %v", after.Components)
	}

	giverDM, err := q.fake.WaitDM(t.Context(), q.users[q.a], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "answered your question") && strings.Contains(m.Content, questionAnswerTea)
	})
	if err != nil {
		t.Fatalf("giver DM: %v", err)
	}
	if strings.Contains(giverDM.Content, recipient) {
		t.Errorf("the answer DM names the recipient: %q", giverDM.Content)
	}
	assertNoPings(t, q.fake)
	q.assertLogsClean(t, questionTea, questionAnswerTea)
}

func TestQuestionAnsweringTwiceLeavesTheDMAloneAndSaysSo(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	opened := q.answerModal(t, recipient, dm)
	first := q.submit(t, recipient, *opened.Modal, questionAnswerTea)
	q.outcome(t, first)
	answered := q.dmByID(t, recipient, dm.ID)
	waitFor(t, "the answer DM", func() bool {
		return len(q.fake.DMs(q.users[q.a])) == 2
	})
	dmsBefore := len(q.fake.DMs(q.users[q.a]))

	reopened := q.answerModal(t, recipient, dm)
	second := q.submit(t, recipient, *reopened.Modal, "A different answer")

	followup := q.outcome(t, second)
	if followup.Source != "followup" || followup.Content != questionTextAnswered {
		t.Errorf("second outcome = %s %q, want a followup with %q", followup.Source, followup.Content, questionTextAnswered)
	}
	if !followup.Ephemeral || !slices.Equal(followup.VisibleTo, []string{recipient}) {
		t.Errorf("followup visible to %v, want only the recipient", followup.VisibleTo)
	}
	if after := q.dmByID(t, recipient, dm.ID); questionTextOf(after) != questionTextOf(answered) || len(after.Components) != 0 {
		t.Errorf("the DM changed: %q", questionTextOf(after))
	}
	if got := len(q.fake.DMs(q.users[q.a])); got != dmsBefore {
		t.Errorf("giver has %d DMs, want %d", got, dmsBefore)
	}
	received := q.questionsReceived(t, recipient)
	if len(received) != 1 || received[0].Answer != questionAnswerTea {
		t.Errorf("received = %+v, want the first answer kept", received)
	}
}

func TestQuestionAnsweringAfterTheRevealSaysClosed(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	opened := q.answerModal(t, recipient, dm)
	revealErr := q.svc.Reveal(t.Context(), q.people[q.a].actor, q.group, q.exchange)
	if revealErr != nil {
		t.Fatalf("Reveal: %v", revealErr)
	}
	before := q.dmByID(t, recipient, dm.ID)

	result := q.submit(t, recipient, *opened.Modal, questionAnswerTea)

	followup := q.outcome(t, result)
	if followup.Source != "followup" || followup.Content != questionTextClosed {
		t.Errorf("outcome = %s %q, want a followup with %q", followup.Source, followup.Content, questionTextClosed)
	}
	after := q.dmByID(t, recipient, dm.ID)
	if questionTextOf(after) != questionTextOf(before) || len(after.Components) != len(before.Components) {
		t.Error("the question DM changed")
	}
}

func TestQuestionsListBothSidesWithoutNamingTheAsker(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	q.ask(t, q.a, true, "What color?")
	dm := q.questionDM(t, recipient, questionTea)
	opened := q.answerModal(t, recipient, dm)
	q.outcome(t, q.submit(t, recipient, *opened.Modal, questionAnswerTea))

	asker := slashOrFail(t, q.harness, q.a, q.ge("questions", true, nil))
	asked := q.outcome(t, asker)
	receiver := slashOrFail(t, q.harness, recipient, q.ge("questions", false, nil))
	received := q.outcome(t, receiver)

	askedFields := questionFields(t, asked)
	for _, want := range []string{questionTea, "What color?", questionAnswerTea} {
		if !strings.Contains(askedFields["Questions you asked"], want) {
			t.Errorf("asked field lacks %q: %q", want, askedFields["Questions you asked"])
		}
	}
	if askedFields["Questions you received"] != "None yet." {
		t.Errorf("asker received field = %q", askedFields["Questions you received"])
	}
	receivedFields := questionFields(t, received)
	if !strings.Contains(receivedFields["Questions you received"], questionTea) ||
		!strings.Contains(receivedFields["Questions you received"], questionAnswerTea) {
		t.Errorf("received field = %q", receivedFields["Questions you received"])
	}
	if receivedFields["Questions you asked"] != "None yet." {
		t.Errorf("recipient asked field = %q", receivedFields["Questions you asked"])
	}
	for _, record := range []fakediscord.Record{asked, received} {
		if !record.Ephemeral {
			t.Error("the questions reply is not ephemeral")
		}
	}
	text := string(received.Embeds[0])
	if strings.Contains(text, q.a) || strings.Contains(text, q.users[q.a].Name) {
		t.Errorf("the recipient's listing names the asker: %s", text)
	}
}

func TestQuestionsListEscapesTruncatesAndCapsTheField(t *testing.T) {
	long := strings.Repeat("x", 500)
	q2 := newQuestionScene(t)
	for i := range 30 {
		q2.ask(t, q2.a, true, fmt.Sprintf("Question number %02d %s", i, strings.Repeat("y", 150)))
	}
	result := slashOrFail(t, q2.harness, q2.a, q2.ge("questions", true, nil))
	record := q2.outcome(t, result)

	field := questionFields(t, record)["Questions you asked"]
	if n := len([]rune(field)); n > 1000 {
		t.Errorf("field has %d runes, want at most 1000", n)
	}
	// The daily limit lets 20 of the 30 asks through.
	askedTotal := 20
	lines := strings.Split(field, "\n")
	keptEntries := len(lines) - 1
	wantLast := fmt.Sprintf("and %d more", askedTotal-keptEntries)
	if keptEntries == 0 || keptEntries >= askedTotal || lines[keptEntries] != wantLast {
		t.Errorf("field = %q, want %d entries and the last line %q", field, keptEntries, wantLast)
	}

	q3 := newQuestionScene(t)
	q3.ask(t, q3.a, true, "**bold** @everyone "+long)
	listing := q3.outcome(t, slashOrFail(t, q3.harness, q3.a, q3.ge("questions", true, nil)))
	value := questionFields(t, listing)["Questions you asked"]
	if !strings.Contains(value, `\*\*bold\*\* \@everyone`) || strings.Contains(value, long) || !strings.Contains(value, "...") {
		t.Errorf("entry = %q, want escaped markdown and a cut text", value)
	}
}

func TestQuestionsMarkClosedAfterTheReveal(t *testing.T) {
	q := newQuestionScene(t)
	q.ask(t, q.a, true, questionTea)
	revealErr := q.svc.Reveal(t.Context(), q.people[q.a].actor, q.group, q.exchange)
	if revealErr != nil {
		t.Fatalf("Reveal: %v", revealErr)
	}

	asked, err := q.svc.QuestionsAsked(t.Context(), q.people[q.a].actor, q.exchange)
	if err != nil || len(asked) != 1 || asked[0].ClosedAt == nil {
		t.Fatalf("asked = %+v, err %v; want one closed question", asked, err)
	}

	// The verb lists drawn exchanges only, so the closed marker is reached through an ID.
	result := slashOrFail(t, q.harness, q.a, q.ge("questions", true, map[string]any{"exchange": q.exchange.String()}))
	field := questionFields(t, q.outcome(t, result))["Questions you asked"]
	if !strings.Contains(field, questionTea+" (closed)") {
		t.Errorf("field = %q, want the closed marker", field)
	}
}

func TestQuestionRateLimitRefusesThe21stUntilADayPasses(t *testing.T) {
	q := newQuestionScene(t)
	for i := range 20 {
		got := replyTo(t, q.harness, q.ask(t, q.a, true, fmt.Sprintf("Question %d?", i)))
		if got.content != questionTextAsked {
			t.Fatalf("question %d: reply = %q", i, got.content)
		}
	}

	refused := replyTo(t, q.harness, q.ask(t, q.a, true, "One too many?"))

	if refused.content != questionTextLimit {
		t.Errorf("21st reply = %q, want %q", refused.content, questionTextLimit)
	}
	q.clock.Advance(23*time.Hour + 59*time.Minute)
	stillRefused := replyTo(t, q.harness, q.ask(t, q.a, true, "Almost a day?"))
	if stillRefused.content != questionTextLimit {
		t.Errorf("reply before 24 hours = %q, want %q", stillRefused.content, questionTextLimit)
	}
	q.clock.Advance(2 * time.Minute)
	allowed := replyTo(t, q.harness, q.ask(t, q.a, true, "A new day?"))
	if allowed.content != questionTextAsked {
		t.Errorf("reply after 24 hours = %q, want %q", allowed.content, questionTextAsked)
	}
}

func TestQuestionAskWithQuestionsTurnedOffSaysSo(t *testing.T) {
	q := newQuestionScene(t)
	_, err := q.svc.SetRule(t.Context(), q.people[q.a].actor, q.group, q.exchange,
		giftexchange.RuleChange{Name: giftexchange.RuleQuestionsEnabled, Raw: "off"})
	if err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	recipient := q.recipientOf(t, q.a)
	dms := len(q.fake.DMs(q.users[recipient]))

	got := replyTo(t, q.harness, q.ask(t, q.a, true, questionTea))

	if got.content != questionTextOff {
		t.Errorf("reply = %q, want %q", got.content, questionTextOff)
	}
	if len(q.fake.DMs(q.users[recipient])) != dms || len(q.questionsReceived(t, recipient)) != 0 {
		t.Error("a question was created")
	}
}

func TestQuestionAnOutsiderCannotAskOrList(t *testing.T) {
	q := newQuestionScene(t)
	outbound := len(q.fake.Outbound())
	sent := func() int {
		count := 0
		for _, record := range q.fake.Outbound()[outbound:] {
			if record.Source == "dm" {
				count++
			}
		}

		return count
	}

	byOption := q.ge("ask", true, map[string]any{"question": questionTea, "exchange": q.exchange.String()})
	byOption.BypassValidation = true
	cases := map[string]struct {
		in   fakediscord.Slash
		want string
	}{
		"ask without an exchange":   {q.askSlash(true, questionTea), questionTextNoExchange},
		"ask naming the exchange":   {byOption, textNotFound},
		"questions without choice":  {q.ge("questions", true, nil), questionTextNoExchange},
		"questions naming exchange": {q.ge("questions", true, map[string]any{"exchange": q.exchange.String()}), textNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := replyTo(t, q.harness, slashOrFail(t, q.harness, q.d, tc.in))

			if got.content != tc.want {
				t.Errorf("reply = %q, want %q", got.content, tc.want)
			}
		})
	}
	for _, user := range []string{q.a, q.b, q.c} {
		if received := q.questionsReceived(t, user); len(received) != 0 {
			t.Errorf("a participant received %+v", received)
		}
	}
	if sent() != 0 {
		t.Errorf("%d DMs were sent", sent())
	}
}

func TestQuestionAskNamesAnExchangeOfAnotherGuildOnlyForItsParticipants(t *testing.T) {
	q := newQuestionScene(t)
	other := q.otherGuildWith(t, q.a, q.d)
	named := map[string]any{"question": questionTea, "exchange": q.exchange.String()}
	inOther := func() fakediscord.Slash {
		in := fakediscord.Slash{Path: "/ge/ask", Guild: &other, Options: named}
		in.BypassValidation = true
		return in
	}
	recipient := q.recipientOf(t, q.a)
	dmsBefore := len(q.fake.DMs(q.users[recipient]))

	outsider := replyTo(t, q.harness, slashOrFail(t, q.harness, q.d, inOther()))
	if outsider.content != textNotFound {
		t.Errorf("outsider reply = %q, want %q", outsider.content, textNotFound)
	}
	if len(q.fake.DMs(q.users[recipient])) != dmsBefore || len(q.questionsReceived(t, recipient)) != 0 {
		t.Fatal("a question was created for an outsider")
	}

	participant := replyTo(t, q.harness, slashOrFail(t, q.harness, q.a, inOther()))
	if participant.content != questionTextAsked {
		t.Errorf("participant reply = %q, want %q", participant.content, questionTextAsked)
	}
	if received := q.questionsReceived(t, recipient); len(received) != 1 {
		t.Errorf("recipient holds %d questions, want 1", len(received))
	}
}

func TestQuestionForgedAnswersToAnotherPersonsQuestionChangeNothing(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	id := questionIDOf(t, dm)
	outbound := len(q.fake.Outbound())

	button, err := q.fake.ClickForged(t.Context(), q.users[q.a], dm, "/gx/answer/"+id)
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}
	if button.CallbackType != questionCallbackMo {
		t.Fatalf("forged button answered type %d, want the modal", button.CallbackType)
	}
	if strings.Contains(string(button.Modal.Components[0])+button.Modal.Title, questionTea) {
		t.Error("the modal shows the question to a stranger")
	}
	forged := q.submit(t, q.a, fakediscord.Modal{CustomID: "/gx/answer-modal/" + id}, "I am not the recipient")

	followup := q.outcome(t, forged)
	if followup.Content != textNotFound {
		t.Errorf("reply = %q, want %q", followup.Content, textNotFound)
	}
	received := q.questionsReceived(t, recipient)
	if len(received) != 1 || received[0].Answer != "" {
		t.Errorf("received = %+v, want the question untouched", received)
	}
	for _, record := range q.fake.Outbound()[outbound:] {
		if record.Source == "dm" || (record.Source == "edit" && record.MessageID == dm.ID) {
			t.Errorf("unexpected outbound record %d (%s)", record.Seq, record.Source)
		}
	}
	if after := q.dmByID(t, recipient, dm.ID); len(after.Components) == 0 {
		t.Error("the recipient's DM lost its button")
	}
}

func TestQuestionAnswerRefusesForgedCustomIDs(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	id := questionIDOf(t, dm)

	for _, customID := range []string{
		"/gx/answer/not-a-uuid",
		"/gx/answer/" + strings.ToUpper(id),
		"/gx/answer/" + id + "/extra",
		"/gx/answer/" + q.exchange.String() + "/x",
	} {
		result, err := q.fake.ClickForged(t.Context(), q.users[recipient], dm, customID)
		if err != nil {
			t.Fatalf("ClickForged %q: %v", customID, err)
		}

		if got := replyTo(t, q.harness, result); got.content != textNotFound {
			t.Errorf("custom ID %q: reply = %q, want %q", customID, got.content, textNotFound)
		}
	}
	if received := q.questionsReceived(t, recipient); received[0].Answer != "" {
		t.Error("a forged custom ID answered the question")
	}
}

func TestQuestionTextsOfEveryShapeReachTheRecipientAsLiteralText(t *testing.T) {
	long := strings.Repeat("a", 1000)
	cases := map[string]struct {
		text    string
		wantDM  string
		wantErr bool
	}{
		"one rune":             {text: "?", wantDM: "?"},
		"the maximum":          {text: long, wantDM: long},
		"right to left":        {text: "שלום עולם?", wantDM: "שלום"},
		"combining marks":      {text: "Café ä?", wantDM: "Café"},
		"emoji":                {text: "\U0001F381 gift?", wantDM: "\U0001F381 gift?"},
		"everyone":             {text: "@everyone @here hi", wantDM: `\@everyone \@here`},
		"user mention":         {text: "<@100000000000000001> hi", wantDM: `\<\@100000000000000001\>`},
		"channel mention":      {text: "<#100000000000000002> hi", wantDM: `\<\#100000000000000002\>`},
		"markdown":             {text: "**bold** _it_ ~~s~~ `c` [l](http://example.com)", wantDM: `\*\*bold\*\* \_it\_`},
		"multiple lines":       {text: "one\ntwo", wantDM: "> one\n> two"},
		"empty":                {text: "", wantErr: true},
		"one past the maximum": {text: long + "a", wantErr: true},
		"zero width only":      {text: questionZW + questionZW, wantErr: true},
		"spaces only":          {text: "   ", wantErr: true},
	}
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dmsBefore := len(q.fake.DMs(q.users[recipient]))
			got := replyTo(t, q.harness, q.ask(t, q.a, true, tc.text))

			if tc.wantErr {
				if !strings.HasPrefix(got.content, questionTextNotSaved) {
					t.Errorf("reply = %q, want a Not saved refusal", got.content)
				}
				return
			}
			if got.content != questionTextAsked {
				t.Fatalf("reply = %q, want %q", got.content, questionTextAsked)
			}
			waitFor(t, "the DM of this question", func() bool {
				return len(q.fake.DMs(q.users[recipient])) > dmsBefore
			})
			dms := q.fake.DMs(q.users[recipient])
			dm := dms[len(dms)-1]
			if !strings.Contains(dm.Content, tc.wantDM) {
				t.Errorf("DM = %q, want it to hold %q", dm.Content, tc.wantDM)
			}
		})
	}
	assertNoPings(t, q.fake)
	if received := q.questionsReceived(t, recipient); len(received) != 10 {
		t.Errorf("recipient holds %d questions, want the 10 valid ones", len(received))
	}
}

func TestQuestionAnswersOfEveryShapeAreCheckedAndShownLiterally(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)

	for name, text := range map[string]string{
		"too long":      strings.Repeat("a", 1001),
		"zero width":    questionZW + questionZW + questionZW,
		"empty":         "",
		"only newlines": "\n\n",
		"control rune":  "leakmarker\x07text",
		"bidi override": "abc" + questionRLO + "def",
	} {
		reopened := q.answerModal(t, recipient, dm)
		result := q.submit(t, recipient, *reopened.Modal, text)

		record := q.outcome(t, result)
		if record.Source != "followup" || !strings.HasPrefix(record.Content, questionTextNotSaved) {
			t.Errorf("%s: outcome = %s %q, want a Not saved followup", name, record.Source, record.Content)
		}
		if after := q.dmByID(t, recipient, dm.ID); len(after.Components) == 0 {
			t.Errorf("%s: the DM lost its button", name)
		}
	}
	if received := q.questionsReceived(t, recipient); received[0].Answer != "" {
		t.Fatalf("an invalid answer was stored: %q", received[0].Answer)
	}

	literal := "@everyone <@100000000000000001> **b** שלום \U0001F381"
	last := q.answerModal(t, recipient, dm)
	result := q.submit(t, recipient, *last.Modal, literal)
	q.outcome(t, result)

	text := questionTextOf(q.dmByID(t, recipient, dm.ID))
	// The embed is read as JSON, so each backslash of the escaped text shows doubled and "<" as \u003c.
	if !strings.Contains(text, `\\@everyone \\\u003c\\@100000000000000001\\\u003e \\*\\*b\\*\\*`) {
		t.Errorf("edited DM = %q, want escaped mentions", text)
	}
	assertNoPings(t, q.fake)
	q.assertLogsClean(t, "everyone", "leakmarker", "שלום")
}

func TestQuestionAnswerOfTheMaximumLengthIsSaved(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	longest := strings.Repeat("a", 1000)

	opened := q.answerModal(t, recipient, dm)
	q.outcome(t, q.submit(t, recipient, *opened.Modal, longest))

	received := q.questionsReceived(t, recipient)
	if received[0].Answer != longest {
		t.Errorf("stored answer has %d runes, want the full %d", len([]rune(received[0].Answer)), len(longest))
	}
}

func TestQuestionTheAskerStaysHiddenFromTheRecipientThroughEveryReply(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)
	before := len(q.fake.Outbound())

	q.ask(t, q.a, true, questionTea)
	dm := q.questionDM(t, recipient, questionTea)
	opened := q.answerModal(t, recipient, dm)
	q.outcome(t, q.submit(t, recipient, *opened.Modal, questionAnswerTea))
	q.outcome(t, slashOrFail(t, q.harness, recipient, q.ge("questions", false, nil)))
	again := q.answerModal(t, recipient, dm)
	q.outcome(t, q.submit(t, recipient, *again.Modal, "again"))

	q.assertNoAsker(t, before, q.a, recipient)
	q.assertLogsClean(t, questionTea, questionAnswerTea, "again")
	for _, line := range q.logRecords(t) {
		if msg, _ := line["msg"].(string); strings.Contains(msg, questionTea) {
			t.Errorf("a log message holds the question: %q", msg)
		}
	}
}

// Characters that linters flag as literals are built at run time.
var (
	questionZW  = string(rune(0x200b))
	questionRLO = string(rune(0x202e))
)
