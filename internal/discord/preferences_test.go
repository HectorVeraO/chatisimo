//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	prefTextSaved   = "Your gift preferences for **Winter** were saved."
	prefTextCleared = "Your gift preferences for **Winter** were cleared."
)

type modalInput struct {
	label     string
	customID  string
	style     int
	maxLength int
	required  bool
	value     string
}

func parseModalInputs(t *testing.T, m *fakediscord.Modal) []modalInput {
	t.Helper()
	if m == nil {
		t.Fatal("modal is nil")
	}

	inputs := make([]modalInput, 0, len(m.Components))
	for _, raw := range m.Components {
		var c struct {
			Type      int    `json:"type"`
			Label     string `json:"label"`
			Component struct {
				Type      int    `json:"type"`
				CustomID  string `json:"custom_id"`
				Style     int    `json:"style"`
				MaxLength int    `json:"max_length"`
				Required  bool   `json:"required"`
				Value     string `json:"value"`
			} `json:"component"`
			CustomID  string `json:"custom_id"`
			Style     int    `json:"style"`
			MaxLength int    `json:"max_length"`
			Required  bool   `json:"required"`
			Value     string `json:"value"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("unmarshal modal component %s: %v", string(raw), err)
		}
		if c.Component.CustomID != "" {
			inputs = append(inputs, modalInput{
				label:     c.Label,
				customID:  c.Component.CustomID,
				style:     c.Component.Style,
				maxLength: c.Component.MaxLength,
				required:  c.Component.Required,
				value:     c.Component.Value,
			})
		} else {
			inputs = append(inputs, modalInput{
				label:     c.Label,
				customID:  c.CustomID,
				style:     c.Style,
				maxLength: c.MaxLength,
				required:  c.Required,
				value:     c.Value,
			})
		}
	}
	return inputs
}

type buttonInfo struct {
	customID string
	label    string
}

func recordButtons(t *testing.T, r fakediscord.Record) []buttonInfo {
	t.Helper()

	var buttons []buttonInfo
	for _, raw := range r.Components {
		var row struct {
			Components []struct {
				CustomID string `json:"custom_id"`
				Label    string `json:"label"`
			} `json:"components"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decode component: %v", err)
		}
		for _, c := range row.Components {
			buttons = append(buttons, buttonInfo{customID: c.CustomID, label: c.Label})
		}
	}
	return buttons
}

func messageOfRecord(t *testing.T, s *scene, user string, record fakediscord.Record) fakediscord.Message {
	t.Helper()

	for _, m := range s.fake.Visible(s.users[user], record.ChannelID) {
		if m.ID == record.MessageID {
			return m
		}
	}
	t.Fatalf("message %s not visible to %s", record.MessageID, user)
	return fakediscord.Message{}
}

func submitModal(t *testing.T, s *scene, user string, modal fakediscord.Modal, fields map[string]string) fakediscord.Record {
	t.Helper()

	res, err := s.fake.SubmitModal(t.Context(), s.users[user], modal, fields)
	if err != nil {
		t.Fatalf("SubmitModal(%s): %v", modal.CustomID, err)
	}
	return s.outcome(t, res)
}

func clickEdit(t *testing.T, s *scene, user string, record fakediscord.Record, id giftexchange.ExchangeID) fakediscord.Result {
	t.Helper()

	msg := messageOfRecord(t, s, user, record)
	customID := "/gx/preferences-edit/" + id.String()
	res, err := s.fake.Click(t.Context(), s.users[user], msg, customID)
	if err != nil {
		t.Fatalf("Click(%s): %v", customID, err)
	}
	return res
}

func TestPreferencesSetUpdateViaCommandAndEditButtonAndClear(t *testing.T) {
	s := newScene(t)
	s.startRunner(t, s.world)
	id := s.createExchange(t, s.a, questionExchange)
	for _, user := range []string{s.a, s.b, s.c} {
		s.joinAs(t, user, id)
	}

	// 1. Initial /ge preferences call opens a 3-field paragraph modal with empty initial values.
	initialSlash := slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil))
	if initialSlash.Modal == nil {
		t.Fatalf("initial /ge preferences opened no modal: %+v", initialSlash)
	}
	if initialSlash.Modal.Title != "Gift preferences" {
		t.Errorf("modal title = %q, want %q", initialSlash.Modal.Title, "Gift preferences")
	}
	if initialSlash.Modal.CustomID != "/gx/preferences-modal/"+id.String() {
		t.Errorf("modal custom ID = %q, want %q", initialSlash.Modal.CustomID, "/gx/preferences-modal/"+id.String())
	}

	inputs := parseModalInputs(t, initialSlash.Modal)
	if len(inputs) != 3 {
		t.Fatalf("modal has %d inputs, want 3", len(inputs))
	}
	wantInitialInputs := []modalInput{
		{label: "Likes and hobbies", customID: "likes", style: 2, maxLength: 300, required: false, value: ""},
		{label: "Dislikes / avoid", customID: "dislikes", style: 2, maxLength: 300, required: false, value: ""},
		{label: "Helpful details", customID: "details", style: 2, maxLength: 300, required: false, value: ""},
	}
	for i, want := range wantInitialInputs {
		if inputs[i] != want {
			t.Errorf("input[%d] = %+v, want %+v", i, inputs[i], want)
		}
	}

	// Submit initial preference: "tea" in likes.
	savedRec := submitModal(t, s, s.a, *initialSlash.Modal, map[string]string{"likes": "tea"})
	if savedRec.Content != prefTextSaved || !savedRec.Ephemeral {
		t.Errorf("saved reply = %q (ephemeral=%v), want %q", savedRec.Content, savedRec.Ephemeral, prefTextSaved)
	}
	fields := questionFields(t, savedRec)
	if fields["Likes and hobbies"] != "tea" {
		t.Errorf("saved Likes = %q, want %q", fields["Likes and hobbies"], "tea")
	}
	buttons := recordButtons(t, savedRec)
	wantEditID := "/gx/preferences-edit/" + id.String()
	if len(buttons) != 1 || buttons[0].customID != wantEditID || buttons[0].label != "Edit" {
		t.Errorf("saved reply buttons = %+v, want single Edit button with custom id %s", buttons, wantEditID)
	}

	// 2. Second /ge preferences call opens modal pre-filled with Likes == "tea".
	secondSlash := slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil))
	if secondSlash.Modal == nil {
		t.Fatalf("second /ge preferences opened no modal: %+v", secondSlash)
	}
	inputs2 := parseModalInputs(t, secondSlash.Modal)
	if inputs2[0].value != "tea" || inputs2[1].value != "" || inputs2[2].value != "" {
		t.Errorf("second modal pre-filled values = %+v, want Likes == tea", inputs2)
	}

	// Append "\ncoffee" ("tea\ncoffee") and add Dislikes: "peanuts".
	savedRec2 := submitModal(t, s, s.a, *secondSlash.Modal, map[string]string{
		"likes":    "tea\ncoffee",
		"dislikes": "peanuts",
	})
	fields2 := questionFields(t, savedRec2)
	if fields2["Likes and hobbies"] != "tea\ncoffee" || fields2["Dislikes / avoid"] != "peanuts" {
		t.Errorf("second saved embed fields = %+v", fields2)
	}

	// Draw the exchange and verify the giver's draw DM and /ge recipient show both lines.
	drawErr := s.svc.Draw(t.Context(), s.people[s.a].actor, s.group, id)
	if drawErr != nil {
		t.Fatalf("Draw: %v", drawErr)
	}
	waitFor(t, "the three draw DMs", func() bool {
		return finishedCount(t, id, "delivered") == 3
	})

	giver := giverOfRecipient(t, s, id, s.a)
	drawDMs := s.fake.DMs(s.users[giver])
	if len(drawDMs) != 1 {
		t.Fatalf("giver has %d draw DMs, want 1", len(drawDMs))
	}
	drawFields := questionFields(t, fakediscord.Record{Embeds: drawDMs[0].Embeds})
	for fieldName, want := range map[string]string{
		"Likes and hobbies": "tea\ncoffee",
		"Dislikes / avoid":  "peanuts",
	} {
		if drawFields[fieldName] != want {
			t.Errorf("giver draw DM %s = %q, want %q", fieldName, drawFields[fieldName], want)
		}
	}

	recipRec := s.outcome(t, slashOrFail(t, s.harness, giver, s.ge("recipient", true, nil)))
	recipFields := questionFields(t, recipRec)
	if recipFields["Likes and hobbies"] != "tea\ncoffee" || recipFields["Dislikes / avoid"] != "peanuts" {
		t.Errorf("/ge recipient fields = %+v, want multiline likes and dislikes", recipFields)
	}

	// 3. Click the Edit button on saved reply in drawn exchange.
	outboundBefore := len(s.fake.Outbound())
	editRes := clickEdit(t, s, s.a, savedRec2, id)
	if editRes.Modal == nil {
		t.Fatalf("clicking Edit opened no modal: %+v", editRes)
	}
	clickInputs := parseModalInputs(t, editRes.Modal)
	if clickInputs[0].value != "tea\ncoffee" || clickInputs[1].value != "peanuts" || clickInputs[2].value != "" {
		t.Errorf("Edit modal pre-filled values = %+v, want tea\\ncoffee and peanuts", clickInputs)
	}

	// Add Details: "size M" while preserving likes and dislikes.
	savedRec3 := submitModal(t, s, s.a, *editRes.Modal, map[string]string{
		"likes":    "tea\ncoffee",
		"dislikes": "peanuts",
		"details":  "size M",
	})
	fields3 := questionFields(t, savedRec3)
	if fields3["Likes and hobbies"] != "tea\ncoffee" || fields3["Dislikes / avoid"] != "peanuts" || fields3["Helpful details"] != "size M" {
		t.Errorf("updated fields after Edit = %+v", fields3)
	}

	ctx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()
	updateDM, err := s.fake.WaitDM(ctx, s.users[giver], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences") &&
			strings.Contains(questionTextOf(m), "size M")
	})
	if err != nil {
		t.Fatalf("WaitDM for updated preferences: %v", err)
	}
	updateFields := questionFields(t, fakediscord.Record{Embeds: updateDM.Embeds})
	for fieldName, want := range map[string]string{
		"Likes and hobbies": "tea\ncoffee",
		"Dislikes / avoid":  "peanuts",
		"Helpful details":   "size M",
	} {
		if updateFields[fieldName] != want {
			t.Errorf("update DM %s = %q, want %q", fieldName, updateFields[fieldName], want)
		}
	}

	// Ensure the recipient never sees the giver's identity in any reply after outboundBefore.
	giverName := s.users[giver].Name
	for _, record := range s.fake.Outbound()[outboundBefore:] {
		if !slices.Contains(record.VisibleTo, s.a) {
			continue
		}
		text := record.Content
		for _, embed := range record.Embeds {
			text += string(embed)
		}
		if strings.Contains(text, giver) || strings.Contains(text, giverName) {
			t.Errorf("record %d visible to recipient leaks giver: %s", record.Seq, text)
		}
	}

	// 4. Submitting all-empty fields in the modal clears preferences and notifies the giver.
	emptySlash := slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil))
	clearedModalRec := submitModal(t, s, s.a, *emptySlash.Modal, map[string]string{
		"likes":    "   ",
		"dislikes": "",
		"details":  "",
	})
	if clearedModalRec.Content != prefTextCleared || len(clearedModalRec.Embeds) != 0 {
		t.Errorf("modal clear reply = %q (embeds=%d), want %q with no embeds",
			clearedModalRec.Content, len(clearedModalRec.Embeds), prefTextCleared)
	}

	clearDM, err := s.fake.WaitDM(ctx, s.users[giver], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences") &&
			strings.Contains(questionTextOf(m), "None yet.")
	})
	if err != nil {
		t.Fatalf("WaitDM for cleared preferences via modal: %v", err)
	}
	if !strings.Contains(questionTextOf(clearDM), "Gift preferences") {
		t.Errorf("clear DM = %q, want Gift preferences field", questionTextOf(clearDM))
	}

	// 5. Reset preferences and verify /ge preferences clear:true clears and notifies giver.
	reSlash := slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil))
	_ = submitModal(t, s, s.a, *reSlash.Modal, map[string]string{"likes": "books"})

	clearSlashRec := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"clear": true,
	})))
	if clearSlashRec.Content != prefTextCleared || len(clearSlashRec.Embeds) != 0 {
		t.Errorf("clear:true reply = %q (embeds=%d), want %q with no embeds",
			clearSlashRec.Content, len(clearSlashRec.Embeds), prefTextCleared)
	}

	clearDM2, err := s.fake.WaitDM(ctx, s.users[giver], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences") &&
			strings.Contains(questionTextOf(m), "None yet.")
	})
	if err != nil {
		t.Fatalf("WaitDM for cleared preferences via clear:true: %v", err)
	}
	if !strings.Contains(questionTextOf(clearDM2), "Gift preferences") {
		t.Errorf("clear DM2 = %q, want Gift preferences field", questionTextOf(clearDM2))
	}

	assertNoPings(t, s.fake)
}

func TestPreferencesEscapesMarkdownAndMentionsAndValidatesInput(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)

	hostileLikes := "@everyone <@100000000000000001> **bold** _italic_ `code`"
	modalRes := slashOrFail(t, q.harness, recipient, q.ge("preferences", true, nil))
	if modalRes.Modal == nil {
		t.Fatalf("slash preferences opened no modal: %+v", modalRes)
	}

	submitRes, err := q.fake.SubmitModal(t.Context(), q.users[recipient], *modalRes.Modal, map[string]string{
		"likes": hostileLikes,
	})
	if err != nil {
		t.Fatalf("SubmitModal: %v", err)
	}
	savedRec := q.outcome(t, submitRes)
	if savedRec.Content != prefTextSaved {
		t.Fatalf("saved reply = %q, want %q", savedRec.Content, prefTextSaved)
	}
	fields := questionFields(t, savedRec)
	if !strings.Contains(fields["Likes and hobbies"], `\@everyone \<\@100000000000000001\> \*\*bold\*\*`) {
		t.Errorf("escaped Likes field = %q", fields["Likes and hobbies"])
	}

	recipRec := q.outcome(t, slashOrFail(t, q.harness, q.a, q.ge("recipient", true, nil)))
	recipFields := questionFields(t, recipRec)
	if !strings.Contains(recipFields["Likes and hobbies"], `\@everyone \<\@100000000000000001\> \*\*bold\*\*`) {
		t.Errorf("/ge recipient Likes field = %q", recipFields["Likes and hobbies"])
	}

	ctx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()
	dm, err := q.fake.WaitDM(ctx, q.users[q.a], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences")
	})
	if err != nil {
		t.Fatalf("WaitDM: %v", err)
	}
	if !strings.Contains(questionTextOf(dm), `\\@everyone \\\u003c\\@100000000000000001\\\u003e \\*\\*bold\\*\\*`) {
		t.Errorf("DM embed = %q, want escaped mentions and markdown", questionTextOf(dm))
	}

	// Overlong preference input is rejected with Not saved and does not overwrite stored preferences.
	tooLongRes, err := q.fake.SubmitModalForged(t.Context(), q.users[recipient], *modalRes.Modal, map[string]string{
		"likes": strings.Repeat("a", giftexchange.MaxPreferenceRunes+1),
	})
	if err != nil {
		t.Fatalf("SubmitModalForged: %v", err)
	}
	badRec := q.outcome(t, tooLongRes)
	if !strings.HasPrefix(badRec.Content, questionTextNotSaved) {
		t.Errorf("overlong preferences reply = %q, want Not saved prefix", badRec.Content)
	}

	assertNoPings(t, q.fake)
	q.assertLogsClean(t, hostileLikes, "everyone")
}

func TestPreferencesPreservesCustomEmojisAndEscapesAdjacentMarkup(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)

	likes := "Cats <:CatHmph:123456789012345678> <@100000000000000001> *bold*"
	dislikes := "Loud noises <a:cat_dance:123456789012345678> _italic_"
	details := "Size M <:CatHmph:123456789012345678>"

	modalRes := slashOrFail(t, q.harness, recipient, q.ge("preferences", true, nil))
	if modalRes.Modal == nil {
		t.Fatalf("slash preferences opened no modal: %+v", modalRes)
	}

	submitRes, err := q.fake.SubmitModal(t.Context(), q.users[recipient], *modalRes.Modal, map[string]string{
		"likes":    likes,
		"dislikes": dislikes,
		"details":  details,
	})
	if err != nil {
		t.Fatalf("SubmitModal: %v", err)
	}
	savedRec := q.outcome(t, submitRes)
	if savedRec.Content != prefTextSaved {
		t.Fatalf("saved reply = %q, want %q", savedRec.Content, prefTextSaved)
	}
	fields := questionFields(t, savedRec)
	wantLikes := `Cats <:CatHmph:123456789012345678> \<\@100000000000000001\> \*bold\*`
	wantDislikes := `Loud noises <a:cat_dance:123456789012345678> \_italic\_`
	wantDetails := `Size M <:CatHmph:123456789012345678>`
	if fields["Likes and hobbies"] != wantLikes {
		t.Errorf("saved Likes field = %q, want %q", fields["Likes and hobbies"], wantLikes)
	}
	if fields["Dislikes / avoid"] != wantDislikes {
		t.Errorf("saved Dislikes field = %q, want %q", fields["Dislikes / avoid"], wantDislikes)
	}
	if fields["Helpful details"] != wantDetails {
		t.Errorf("saved Details field = %q, want %q", fields["Helpful details"], wantDetails)
	}

	recipRec := q.outcome(t, slashOrFail(t, q.harness, q.a, q.ge("recipient", true, nil)))
	recipFields := questionFields(t, recipRec)
	if recipFields["Likes and hobbies"] != wantLikes {
		t.Errorf("/ge recipient Likes field = %q, want %q", recipFields["Likes and hobbies"], wantLikes)
	}
	if recipFields["Dislikes / avoid"] != wantDislikes {
		t.Errorf("/ge recipient Dislikes field = %q, want %q", recipFields["Dislikes / avoid"], wantDislikes)
	}
	if recipFields["Helpful details"] != wantDetails {
		t.Errorf("/ge recipient Details field = %q, want %q", recipFields["Helpful details"], wantDetails)
	}

	ctx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()
	dm, err := q.fake.WaitDM(ctx, q.users[q.a], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences")
	})
	if err != nil {
		t.Fatalf("WaitDM: %v", err)
	}
	dmEmbedFields := questionFields(t, fakediscord.Record{Embeds: dm.Embeds})
	if dmEmbedFields["Likes and hobbies"] != wantLikes {
		t.Errorf("DM Likes field = %q, want %q", dmEmbedFields["Likes and hobbies"], wantLikes)
	}
	if dmEmbedFields["Dislikes / avoid"] != wantDislikes {
		t.Errorf("DM Dislikes field = %q, want %q", dmEmbedFields["Dislikes / avoid"], wantDislikes)
	}
	if dmEmbedFields["Helpful details"] != wantDetails {
		t.Errorf("DM Details field = %q, want %q", dmEmbedFields["Helpful details"], wantDetails)
	}

	assertNoPings(t, q.fake)
}

func TestPreferencesDeniesNonParticipantsAndInvalidStates(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, questionExchange)
	s.joinAs(t, s.b, id)

	noChoice := replyTo(t, s.harness, slashOrFail(t, s.harness, s.d, s.ge("preferences", true, nil)))
	if noChoice.content != questionTextNoExchange {
		t.Errorf("outsider without exchange option: reply = %q, want %q", noChoice.content, questionTextNoExchange)
	}

	namedOutsider := replyTo(t, s.harness, slashOrFail(t, s.harness, s.d, s.ge("preferences", true, map[string]any{
		"exchange": id.String(),
	})))
	if namedOutsider.content != textNotFound {
		t.Errorf("outsider naming exchange: reply = %q, want %q", namedOutsider.content, textNotFound)
	}

	orgNotJoined := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"exchange": id.String(),
	})))
	if orgNotJoined.content != drawTextNotMember {
		t.Errorf("organizer who never joined: reply = %q, want %q", orgNotJoined.content, drawTextNotMember)
	}

	// Cancelled exchange is rejected without opening a modal.
	idCancel := s.createExchange(t, s.a, "Cancelled")
	s.joinAs(t, s.a, idCancel)
	s.joinAs(t, s.b, idCancel)
	if err := s.svc.Cancel(t.Context(), s.people[s.a].actor, s.group, idCancel); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	cancelledReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"exchange": idCancel.String(),
	})))
	if cancelledReply.content != drawTextStateCancelled {
		t.Errorf("cancelled preferences reply = %q, want %q", cancelledReply.content, drawTextStateCancelled)
	}
	cancelledDMReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("preferences", false, map[string]any{
		"exchange": idCancel.String(),
	})))
	if cancelledDMReply.content != drawTextStateCancelled {
		t.Errorf("cancelled DM preferences reply = %q, want %q", cancelledDMReply.content, drawTextStateCancelled)
	}
	otherGuild := s.otherGuildWith(t, s.a)
	cancelledOtherGuildReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, fakediscord.Slash{
		Path:    "/ge/preferences",
		Guild:   &otherGuild,
		Options: map[string]any{"exchange": idCancel.String()},
	}))
	if cancelledOtherGuildReply.content != drawTextStateCancelled {
		t.Errorf("cancelled other-guild preferences reply = %q, want %q", cancelledOtherGuildReply.content, drawTextStateCancelled)
	}

	// Revealed exchange is rejected without opening a modal.
	idReveal := s.createExchange(t, s.a, "Revealed")
	for _, user := range []string{s.a, s.b, s.c} {
		s.joinAs(t, user, idReveal)
	}
	if err := s.svc.Draw(t.Context(), s.people[s.a].actor, s.group, idReveal); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if err := s.svc.Reveal(t.Context(), s.people[s.a].actor, s.group, idReveal); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	revealedReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"exchange": idReveal.String(),
	})))
	if revealedReply.content != drawTextStateRevealed {
		t.Errorf("revealed preferences reply = %q, want %q", revealedReply.content, drawTextStateRevealed)
	}

	// Forged modal submission by outsider is rejected.
	forgedModal := fakediscord.Modal{CustomID: "/gx/preferences-modal/" + id.String()}
	forgedRes, err := s.fake.SubmitModalForged(t.Context(), s.users[s.d], forgedModal, map[string]string{
		"likes": "tea",
	})
	if err != nil {
		t.Fatalf("SubmitModalForged: %v", err)
	}
	forgedRec := s.outcome(t, forgedRes)
	if forgedRec.Content != textNotFound && forgedRec.Content != drawTextNotMember {
		t.Errorf("forged outsider modal reply = %q, want not found or not member", forgedRec.Content)
	}

	// Forged modal submission with bidi / control chars is rejected with Not saved.
	bidiRes, err := s.fake.SubmitModalForged(t.Context(), s.users[s.b], forgedModal, map[string]string{
		"likes": "tea\u202ecoffee",
	})
	if err != nil {
		t.Fatalf("SubmitModalForged(bidi): %v", err)
	}
	bidiRec := s.outcome(t, bidiRes)
	if !strings.HasPrefix(bidiRec.Content, questionTextNotSaved) {
		t.Errorf("bidi modal reply = %q, want prefix %q", bidiRec.Content, questionTextNotSaved)
	}
}

func TestPreferencesSpanishLocale(t *testing.T) {
	s := newScene(t)
	if err := s.fake.SetGuildLocale(s.guild, "es-419"); err != nil {
		t.Fatalf("SetGuildLocale(es-419): %v", err)
	}

	id := s.createExchange(t, s.a, "Navidad")
	s.joinAs(t, s.a, id)

	modalRes := slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil))
	if modalRes.Modal == nil {
		t.Fatalf("slash preferences opened no modal: %+v", modalRes)
	}
	if modalRes.Modal.Title != "Preferencias de regalo" {
		t.Errorf("modal title = %q, want %q", modalRes.Modal.Title, "Preferencias de regalo")
	}

	inputs := parseModalInputs(t, modalRes.Modal)
	if len(inputs) != 3 {
		t.Fatalf("modal has %d inputs, want 3", len(inputs))
	}
	wantLabels := []string{"Gustos y pasatiempos", "No me gusta / evitar", "Detalles útiles"}
	for i, want := range wantLabels {
		if inputs[i].label != want {
			t.Errorf("input[%d] label = %q, want %q", i, inputs[i].label, want)
		}
	}

	savedRes, err := s.fake.SubmitModal(t.Context(), s.users[s.a], *modalRes.Modal, map[string]string{"likes": "Café"})
	if err != nil {
		t.Fatalf("SubmitModal: %v", err)
	}
	savedRec := s.outcome(t, savedRes)
	if !strings.Contains(savedRec.Content, "Se guardaron tus preferencias de regalo") {
		t.Errorf("saved reply = %q, want Spanish confirmation", savedRec.Content)
	}
	buttons := recordButtons(t, savedRec)
	if len(buttons) != 1 || buttons[0].label != "Editar" {
		t.Errorf("buttons = %+v, want single Editar button", buttons)
	}
}

func giverOfRecipient(t *testing.T, s *scene, id giftexchange.ExchangeID, recipient string) string {
	t.Helper()

	for _, candidate := range []string{s.a, s.b, s.c} {
		assign, err := s.svc.MyAssignment(t.Context(), s.people[candidate].actor, id)
		if err != nil {
			t.Fatalf("MyAssignment(%s): %v", candidate, err)
		}
		for _, ident := range assign.Recipient.Identities {
			if ident.Subject == recipient {
				return candidate
			}
		}
	}
	t.Fatalf("no giver found for recipient %s", recipient)
	return ""
}

func (s *scene) outcome(t *testing.T, result fakediscord.Result) fakediscord.Record {
	t.Helper()

	var found fakediscord.Record
	waitFor(t, "the edit or followup of the interaction", func() bool {
		for _, record := range s.fake.Outbound() {
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
