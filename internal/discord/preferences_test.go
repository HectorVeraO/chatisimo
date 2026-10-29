//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	prefTextNone    = "You have not set gift preferences for **Winter** yet. Use /ge preferences with likes, dislikes or details."
	prefTextSaved   = "Your gift preferences for **Winter** were saved."
	prefTextCleared = "Your gift preferences for **Winter** were cleared."
)

func TestPreferencesSetViewUpdateAndClearBeforeAndAfterDraw(t *testing.T) {
	s := newScene(t)
	s.startRunner(t, s.world)
	id := s.createExchange(t, s.a, questionExchange)
	for _, user := range []string{s.a, s.b, s.c} {
		s.joinAs(t, user, id)
	}

	// View when no preferences are set yet.
	viewEmpty := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil)))
	if viewEmpty.Content != prefTextNone || !viewEmpty.Ephemeral {
		t.Errorf("empty preferences reply = %q (ephemeral=%v), want %q", viewEmpty.Content, viewEmpty.Ephemeral, prefTextNone)
	}

	// Set likes and dislikes in open state.
	setOpen := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"likes":    "Pour-over coffee, sci-fi novels",
		"dislikes": "Scented candles",
	})))
	if setOpen.Content != prefTextSaved || !setOpen.Ephemeral {
		t.Errorf("set preferences reply = %q (ephemeral=%v), want %q", setOpen.Content, setOpen.Ephemeral, prefTextSaved)
	}
	fields := questionFields(t, setOpen)
	if fields["Likes and hobbies"] != "Pour-over coffee, sci-fi novels" ||
		fields["Dislikes / avoid"] != "Scented candles" {
		t.Errorf("saved embed fields = %+v", fields)
	}
	if _, hasDetails := fields["Helpful details"]; hasDetails {
		t.Errorf("unexpected Helpful details field in %+v", fields)
	}

	// Partial update in open state preserves existing fields and adds details.
	updateOpen := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", false, map[string]any{
		"details": "T-shirt size M",
	})))
	updatedFields := questionFields(t, updateOpen)
	if updatedFields["Likes and hobbies"] != "Pour-over coffee, sci-fi novels" ||
		updatedFields["Dislikes / avoid"] != "Scented candles" ||
		updatedFields["Helpful details"] != "T-shirt size M" {
		t.Errorf("partially updated fields = %+v", updatedFields)
	}

	// Viewing in open state returns all three fields.
	viewOpen := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, nil)))
	if got := questionFields(t, viewOpen); len(got) != 3 {
		t.Errorf("view fields = %+v, want 3 fields", got)
	}

	// Draw the exchange and verify the giver's draw DM and /ge recipient show the preferences.
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
	drawDMText := questionTextOf(drawDMs[0])
	for _, want := range []string{"Pour-over coffee, sci-fi novels", "Scented candles", "T-shirt size M"} {
		if !strings.Contains(drawDMText, want) {
			t.Errorf("giver draw DM = %q, want it to hold %q", drawDMText, want)
		}
	}

	recipRec := s.outcome(t, slashOrFail(t, s.harness, giver, s.ge("recipient", true, nil)))
	recipFields := questionFields(t, recipRec)
	if recipFields["Likes and hobbies"] != "Pour-over coffee, sci-fi novels" ||
		recipFields["Dislikes / avoid"] != "Scented candles" ||
		recipFields["Helpful details"] != "T-shirt size M" {
		t.Errorf("/ge recipient fields = %+v", recipFields)
	}

	// Updating preferences after the draw sends a DM to the giver without revealing the giver to the recipient.
	outboundBefore := len(s.fake.Outbound())
	updateDrawn := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"likes": "Board games and puzzles",
	})))
	if updateDrawn.Content != prefTextSaved {
		t.Errorf("update in drawn reply = %q, want %q", updateDrawn.Content, prefTextSaved)
	}

	ctx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()
	updateDM, err := s.fake.WaitDM(ctx, s.users[giver], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences")
	})
	if err != nil {
		t.Fatalf("WaitDM for updated preferences: %v", err)
	}
	if !strings.Contains(questionTextOf(updateDM), "Board games and puzzles") {
		t.Errorf("update DM = %q, want updated likes", questionTextOf(updateDM))
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

	// Clearing preferences after the draw clears the fields and notifies the giver.
	clearRec := s.outcome(t, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"clear": true,
	})))
	if clearRec.Content != prefTextCleared || len(clearRec.Embeds) != 0 {
		t.Errorf("clear reply = %q (embeds=%d), want %q with no embeds",
			clearRec.Content, len(clearRec.Embeds), prefTextCleared)
	}

	clearDM, err := s.fake.WaitDM(ctx, s.users[giver], func(m fakediscord.Message) bool {
		return strings.Contains(m.Content, "updated their gift preferences") &&
			strings.Contains(questionTextOf(m), "None yet.")
	})
	if err != nil {
		t.Fatalf("WaitDM for cleared preferences: %v", err)
	}
	if !strings.Contains(questionTextOf(clearDM), "Gift preferences") {
		t.Errorf("clear DM = %q, want Gift preferences field", questionTextOf(clearDM))
	}
	assertNoPings(t, s.fake)
}

func TestPreferencesEscapesMarkdownAndMentionsAndValidatesInput(t *testing.T) {
	q := newQuestionScene(t)
	recipient := q.recipientOf(t, q.a)

	hostileLikes := "@everyone <@100000000000000001> **bold** _italic_ `code`"
	slashIn := q.ge("preferences", true, map[string]any{
		"likes": hostileLikes,
	})
	slashIn.BypassValidation = true
	savedRec := q.outcome(t, slashOrFail(t, q.harness, recipient, slashIn))
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
	tooLongIn := q.ge("preferences", true, map[string]any{
		"likes": strings.Repeat("a", giftexchange.MaxPreferenceRunes+1),
	})
	tooLongIn.BypassValidation = true
	badRec := q.outcome(t, slashOrFail(t, q.harness, recipient, tooLongIn))
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

	slashIn := q.ge("preferences", true, map[string]any{
		"likes":    likes,
		"dislikes": dislikes,
		"details":  details,
	})
	slashIn.BypassValidation = true
	savedRec := q.outcome(t, slashOrFail(t, q.harness, recipient, slashIn))
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

func TestPreferencesDeniesNonParticipants(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, questionExchange)
	s.joinAs(t, s.b, id)

	noChoice := replyTo(t, s.harness, slashOrFail(t, s.harness, s.d, s.ge("preferences", true, nil)))
	if noChoice.content != questionTextNoExchange {
		t.Errorf("outsider without exchange option: reply = %q, want %q", noChoice.content, questionTextNoExchange)
	}

	namedOutsider := replyTo(t, s.harness, slashOrFail(t, s.harness, s.d, s.ge("preferences", true, map[string]any{
		"likes":    "Coffee",
		"exchange": id.String(),
	})))
	if namedOutsider.content != textNotFound {
		t.Errorf("outsider naming exchange: reply = %q, want %q", namedOutsider.content, textNotFound)
	}

	orgNotJoined := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("preferences", true, map[string]any{
		"likes":    "Coffee",
		"exchange": id.String(),
	})))
	if orgNotJoined.content != drawTextNotMember {
		t.Errorf("organizer who never joined: reply = %q, want %q", orgNotJoined.content, drawTextNotMember)
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
