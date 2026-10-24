package fakediscord_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

type visibilityWorld struct {
	srv                 *fakediscord.Server
	alice, bruno, carol fakediscord.User
	guild               fakediscord.Guild
}

func newVisibilityWorld(t *testing.T) visibilityWorld {
	t.Helper()

	srv := newFake(t, fakediscord.Options{})
	w := visibilityWorld{
		srv:   srv,
		alice: srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"}),
		bruno: srv.AddUser(fakediscord.User{ID: brunoID, Name: "Bruno"}),
		carol: srv.AddUser(fakediscord.User{ID: carolID, Name: "Carol"}),
	}
	w.guild = srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, w.alice, w.bruno)

	return w
}

func openDM(t *testing.T, srv *fakediscord.Server, userID string) string {
	t.Helper()

	got := call(t, srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(srv), `{"recipient_id":"`+userID+`"}`)
	if got.status != http.StatusOK {
		t.Fatalf("create DM: status %d, body %s", got.status, got.body)
	}
	id, _ := got.json(t)["id"].(string)

	return id
}

func TestGuildMessageIsVisibleToMembersOnly(t *testing.T) {
	w := newVisibilityWorld(t)

	got := postChannelMessage(t, w.srv, w.guild.ChannelID, "hello winter", "", "")

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}
	rec := lastRecord(t, w.srv)
	if !slices.Equal(rec.VisibleTo, []string{aliceID, brunoID}) {
		t.Errorf("visible_to = %v, want both members", rec.VisibleTo)
	}
	if rec.Source != "channel" || rec.GuildID == nil || *rec.GuildID != winterGuildID {
		t.Errorf("source/guild = %q/%v, want channel in the guild", rec.Source, rec.GuildID)
	}
	for _, member := range []fakediscord.User{w.alice, w.bruno} {
		visible := w.srv.Visible(member, w.guild.ChannelID)
		if len(visible) != 1 || visible[0].Content != "hello winter" {
			t.Errorf("%s sees %+v, want the message", member.Name, visible)
		}
	}
	if seen := w.srv.Visible(w.carol, w.guild.ChannelID); len(seen) != 0 {
		t.Errorf("a non-member sees %+v through Visible", seen)
	}
	if len(w.srv.DMs(w.alice)) != 0 {
		t.Errorf("a guild message appeared in Alice's DMs")
	}
}

func TestGuildMessageVisibilityIsFixedWhenItIsSent(t *testing.T) {
	w := newVisibilityWorld(t)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "before carol", "", "")

	w.srv.AddMember(w.guild, w.carol)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "after carol", "", "")

	records := w.srv.Outbound()
	if !slices.Equal(records[0].VisibleTo, []string{aliceID, brunoID}) {
		t.Errorf("first visible_to = %v, want the two original members", records[0].VisibleTo)
	}
	if !slices.Equal(records[1].VisibleTo, []string{aliceID, brunoID, carolID}) {
		t.Errorf("second visible_to = %v, want all three members", records[1].VisibleTo)
	}
}

func TestDMIsVisibleToTheRecipientOnly(t *testing.T) {
	w := newVisibilityWorld(t)
	dm := openDM(t, w.srv, aliceID)

	got := call(t, w.srv, http.MethodPost, "/api/v10/channels/"+dm+"/messages", botAuth(w.srv), `{"content":"secret for Alice"}`)

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}
	rec := lastRecord(t, w.srv)
	if !slices.Equal(rec.VisibleTo, []string{aliceID}) {
		t.Errorf("visible_to = %v, want only Alice", rec.VisibleTo)
	}
	if rec.Source != "dm" || rec.GuildID != nil {
		t.Errorf("source/guild = %q/%v, want dm with no guild", rec.Source, rec.GuildID)
	}
	if dms := w.srv.DMs(w.alice); len(dms) != 1 || dms[0].Content != "secret for Alice" {
		t.Errorf("Alice's DMs = %+v", dms)
	}
	if seen := w.srv.Visible(w.alice, dm); len(seen) != 1 {
		t.Errorf("Alice sees %d messages in her DM, want 1", len(seen))
	}
	for _, other := range []fakediscord.User{w.bruno, w.carol} {
		if seen := w.srv.Visible(other, dm); len(seen) != 0 {
			t.Errorf("%s sees %+v in Alice's DM", other.Name, seen)
		}
		if dms := w.srv.DMs(other); len(dms) != 0 {
			t.Errorf("%s has DMs %+v", other.Name, dms)
		}
	}
}

func TestVisibleIsEmptyForUnknownChannels(t *testing.T) {
	w := newVisibilityWorld(t)

	if seen := w.srv.Visible(w.alice, missingID); len(seen) != 0 {
		t.Errorf("Visible on an unknown channel = %+v", seen)
	}
}

func TestOutboundReturnsACopy(t *testing.T) {
	w := newVisibilityWorld(t)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "hi", "", "")

	first := w.srv.Outbound()
	first[0].VisibleTo[0] = "tampered"
	first[0].Content = "tampered"

	again := w.srv.Outbound()
	if again[0].VisibleTo[0] != aliceID || again[0].Content != "hi" {
		t.Errorf("mutating a returned record changed the fake: %+v", again[0])
	}
}

func TestRegisteringAKnownUserWithoutANameKeepsTheName(t *testing.T) {
	w := newVisibilityWorld(t)

	again := w.srv.AddUser(fakediscord.User{ID: aliceID})

	if again.Name != w.alice.Name {
		t.Errorf("name = %q, want %q", again.Name, w.alice.Name)
	}
}

func TestAddMemberRejectsAnUnknownGuild(t *testing.T) {
	w := newVisibilityWorld(t)

	defer func() {
		if recover() == nil {
			t.Error("AddMember did not panic for an unknown guild")
		}
	}()
	w.srv.AddMember(fakediscord.Guild{ID: missingID}, w.alice)
}
