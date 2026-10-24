package fakediscord_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func TestClosedDMsAnswer50007AndAreRecorded(t *testing.T) {
	w := newVisibilityWorld(t)
	dm := openDM(t, w.srv, aliceID)
	dmPath := "/api/v10/channels/" + dm + "/messages"
	mustSetDMsOpen(t, w.srv, w.alice, false)

	got := call(t, w.srv, http.MethodPost, dmPath, botAuth(w.srv), `{"content":"hello Alice"}`)

	if got.status != http.StatusForbidden {
		t.Fatalf("status = %d, body %s, want 403", got.status, got.body)
	}
	body := got.json(t)
	if body["code"] != float64(50007) || body["message"] != "Cannot send messages to this user" {
		t.Errorf("body = %v, want 50007", body)
	}
	if dms := w.srv.DMs(w.alice); len(dms) != 0 {
		t.Errorf("a refused DM was stored: %+v", dms)
	}
	rec := lastRecord(t, w.srv)
	if rec.Status != http.StatusForbidden || rec.Source != "dm" || rec.Content != "hello Alice" || rec.MessageID != "" {
		t.Errorf("record = %+v, want the refused attempt with status 403", rec)
	}
	if !slices.Equal(rec.VisibleTo, []string{aliceID}) {
		t.Errorf("visible_to = %v, want only Alice", rec.VisibleTo)
	}

	t.Run("other users keep receiving DMs", func(t *testing.T) {
		brunoDM := openDM(t, w.srv, brunoID)

		got := call(t, w.srv, http.MethodPost, "/api/v10/channels/"+brunoDM+"/messages", botAuth(w.srv), `{"content":"hello Bruno"}`)

		if got.status != http.StatusOK {
			t.Errorf("status = %d, want 200", got.status)
		}
	})

	t.Run("opening the DMs again lets messages through", func(t *testing.T) {
		mustSetDMsOpen(t, w.srv, w.alice, true)

		got := call(t, w.srv, http.MethodPost, dmPath, botAuth(w.srv), `{"content":"hello again"}`)

		if got.status != http.StatusOK {
			t.Errorf("status = %d, want 200", got.status)
		}
	})
}

func TestClosedDMsDoNotBlockGuildMessagesOrInteractionReplies(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	mustSetDMsOpen(t, w.srv, w.alice, false)

	posted := postChannelMessage(t, w.srv, w.guild.ChannelID, "guild post", "", "")
	_, result := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"reply"}}`)

	if posted.status != http.StatusOK || result.Message == nil {
		t.Errorf("guild post = %d, reply = %+v, want both to work", posted.status, result.Message)
	}
}

func TestSameNonceWithEnforceNonceReturnsTheFirstMessage(t *testing.T) {
	w := newVisibilityWorld(t)
	dm := openDM(t, w.srv, aliceID)
	path := "/api/v10/channels/" + dm + "/messages"
	send := func(path, body string) reply {
		return call(t, w.srv, http.MethodPost, path, botAuth(w.srv), body)
	}

	first := send(path, `{"content":"once","nonce":"n-1","enforce_nonce":true}`)
	second := send(path, `{"content":"once","nonce":"n-1","enforce_nonce":true}`)

	if first.status != http.StatusOK || second.status != http.StatusOK {
		t.Fatalf("statuses = %d, %d, want 200 twice", first.status, second.status)
	}
	if first.json(t)["id"] != second.json(t)["id"] {
		t.Errorf("ids = %v, %v, want the same message", first.json(t)["id"], second.json(t)["id"])
	}
	if dms := w.srv.DMs(w.alice); len(dms) != 1 {
		t.Errorf("Alice has %d DMs, want 1", len(dms))
	}
	records := w.srv.Outbound()
	if len(records) != 2 || records[0].Deduplicated || !records[1].Deduplicated {
		t.Fatalf("records = %+v, want the second marked deduplicated", records)
	}
	if records[1].MessageID != records[0].MessageID || len(records[1].Pings) != 0 {
		t.Errorf("deduplicated record = %+v, want the first message and no pings", records[1])
	}

	t.Run("a different nonce makes a new message", func(t *testing.T) {
		got := send(path, `{"content":"twice","nonce":"n-2","enforce_nonce":true}`)

		if got.json(t)["id"] == first.json(t)["id"] {
			t.Error("a new nonce returned the old message")
		}
	})

	t.Run("a nonce without enforce_nonce is not deduplicated", func(t *testing.T) {
		a := send(path, `{"content":"loose","nonce":"n-3"}`)
		b := send(path, `{"content":"loose","nonce":"n-3"}`)

		if a.json(t)["id"] == b.json(t)["id"] {
			t.Error("a nonce was enforced without enforce_nonce")
		}
	})

	t.Run("the same nonce in another channel is separate", func(t *testing.T) {
		brunoDM := openDM(t, w.srv, brunoID)

		got := send("/api/v10/channels/"+brunoDM+"/messages", `{"content":"once","nonce":"n-1","enforce_nonce":true}`)

		if got.json(t)["id"] == first.json(t)["id"] {
			t.Error("a nonce leaked across channels")
		}
	})

	t.Run("a nonce over 25 characters is refused", func(t *testing.T) {
		got := send(path, `{"content":"x","nonce":"`+strings.Repeat("n", 26)+`","enforce_nonce":true}`)

		if got.status != http.StatusBadRequest || got.json(t)["code"] != float64(50035) {
			t.Errorf("status = %d body %s, want 400 50035", got.status, got.body)
		}
	})
}

func TestFailNextAnswersOneRateLimit(t *testing.T) {
	w := newVisibilityWorld(t)
	mustFailNext(t, w.srv, "POST /channels/{id}/messages", 429, 0)
	path := "/api/v10/channels/" + w.guild.ChannelID + "/messages"
	before, err := w.srv.PendingFaults("POST /channels/{id}/messages")
	if err != nil || before != 1 {
		t.Fatalf("pending faults before = %d, %v, want 1", before, err)
	}

	limited := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)
	retried := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)

	if limited.status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", limited.status)
	}
	if limited.header.Get("Retry-After") != "0.2" || limited.header.Get("X-RateLimit-Bucket") != "fake" {
		t.Errorf("headers = %v, want Retry-After 0.2 and bucket fake", limited.header)
	}
	body := limited.json(t)
	if body["message"] != "You are being rate limited." || body["retry_after"] != 0.2 || body["global"] != false {
		t.Errorf("body = %v, want the rate limit body", body)
	}
	if retried.status != http.StatusOK {
		t.Errorf("the request after the fault = %d, want 200", retried.status)
	}
	after, err := w.srv.PendingFaults("POST /channels/{id}/messages")
	if err != nil || after != 0 {
		t.Errorf("pending faults after = %d, %v, want 0", after, err)
	}
	if got := w.srv.Visible(w.alice, w.guild.ChannelID); len(got) != 1 {
		t.Errorf("Alice sees %d messages, want 1: the limited request stored nothing", len(got))
	}
}

func TestFailNextShapesTheErrorAndMatchesTheNextRequestOnly(t *testing.T) {
	w := newVisibilityWorld(t)
	path := "/api/v10/channels/" + w.guild.ChannelID + "/messages"

	t.Run("a status and a Discord code", func(t *testing.T) {
		mustFailNext(t, w.srv, "POST /channels/{id}/messages", 500, 1234)

		got := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)

		if got.status != 500 || got.json(t)["code"] != float64(1234) {
			t.Errorf("answer = %d %s, want 500 with code 1234", got.status, got.body)
		}
	})

	t.Run("parameter names and the API prefix do not matter", func(t *testing.T) {
		mustFailNext(t, w.srv, "POST /api/v10/channels/{channel}/messages", 503, 0)

		got := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)

		if got.status != 503 {
			t.Errorf("status = %d, want 503", got.status)
		}
	})

	t.Run("another route is not affected", func(t *testing.T) {
		mustFailNext(t, w.srv, "POST /users/@me/channels", 500, 0)

		message := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)
		dm := call(t, w.srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(w.srv), `{"recipient_id":"`+aliceID+`"}`)

		if message.status != http.StatusOK || dm.status != 500 {
			t.Errorf("message = %d, dm = %d, want 200 and 500", message.status, dm.status)
		}
	})

	t.Run("faults on one route queue in order", func(t *testing.T) {
		mustFailNext(t, w.srv, "GET /gateway", 500, 0)
		mustFailNext(t, w.srv, "GET /gateway", 502, 0)

		first := call(t, w.srv, http.MethodGet, "/api/v10/gateway", "", "")
		second := call(t, w.srv, http.MethodGet, "/api/v10/gateway", "", "")
		third := call(t, w.srv, http.MethodGet, "/api/v10/gateway", "", "")

		if first.status != 500 || second.status != 502 || third.status != 200 {
			t.Errorf("statuses = %d, %d, %d, want 500, 502, 200", first.status, second.status, third.status)
		}
	})

	t.Run("a request refused for its credentials does not use the fault up", func(t *testing.T) {
		mustFailNext(t, w.srv, "POST /channels/{id}/messages", 500, 0)

		unauthorized := call(t, w.srv, http.MethodPost, path, "", `{"content":"hi"}`)
		authorized := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)

		if unauthorized.status != http.StatusUnauthorized || authorized.status != 500 {
			t.Errorf("statuses = %d, %d, want 401 then 500", unauthorized.status, authorized.status)
		}
	})
}

func TestFailNextCoversTheInteractionRoutes(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	mustFailNext(t, w.srv, "POST /interactions/{id}/{token}/callback", 500, 0)

	failed := w.respond(t, sent, `{"type":4,"data":{"content":"pong"}}`)
	retried := w.respond(t, sent, `{"type":4,"data":{"content":"pong"}}`)

	if failed.status != 500 || retried.status != http.StatusNoContent {
		t.Errorf("statuses = %d, %d, want 500 then 204: the failed callback must not count as the answer", failed.status, retried.status)
	}
	finish(t, done)
}

func TestFailNextRefusesFaultsItCannotApply(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	tests := []struct {
		name   string
		route  string
		status int
		code   int
	}{
		{"an unknown route", "POST /nowhere", 500, 0},
		{"a success status", "POST /channels/{id}/messages", 200, 0},
		{"a status over 599", "POST /channels/{id}/messages", 700, 0},
		{"a negative code", "POST /channels/{id}/messages", 500, -1},
		{"a Gateway code outside 4000-4999", "GATEWAY identify", 0, 5000},
		{"an empty route", "", 500, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := srv.FailNext(tc.route, tc.status, tc.code)

			if !errors.Is(err, fakediscord.ErrInvalidFault) {
				t.Errorf("FailNext error = %v, want ErrInvalidFault", err)
			}
		})
	}
}

func TestSetDMsOpenRejectsAnUnknownUser(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	err := srv.SetDMsOpen(fakediscord.User{ID: "999999999999999999"}, false)

	if !errors.Is(err, fakediscord.ErrUnknownUser) {
		t.Errorf("SetDMsOpen error = %v, want ErrUnknownUser", err)
	}
}

func TestIdentifyFaultClosesTheNextIdentifyWith4004(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})
	mustFailNext(t, srv, "GATEWAY identify", 0, 4004)

	faulted := dialRaw(t, srv)
	faulted.send(t, 2, `{"token":"`+srv.Token()+`","intents":1}`)
	code := faulted.readClose(t)

	if code != 4004 {
		t.Errorf("close code = %d, want 4004 even with the right token", code)
	}
	if _, ok := srv.Intents(); ok {
		t.Error("a faulted Identify was recorded")
	}

	_, sessionID := identifyRaw(t, srv)
	if sessionID == "" {
		t.Error("the Identify after the fault did not succeed")
	}
}
