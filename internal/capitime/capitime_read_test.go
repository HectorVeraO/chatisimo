package capitime_test

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

const evaluatedLogMessage = "capi time message evaluated"

func newJSONLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// lastDecision returns the decision and media fields of the newest decision log line.
func (h *readHarness) lastDecision(t *testing.T) (string, string) {
	t.Helper()

	entries := h.logs.entries(t)
	if len(entries) == 0 {
		t.Fatal("the decision log is empty")
	}

	last := entries[len(entries)-1]
	if last["msg"] != evaluatedLogMessage || last["level"] != "INFO" {
		t.Fatalf("log line = %v, want an INFO %q line", last, evaluatedLogMessage)
	}

	decision, _ := last["decision"].(string)
	media, _ := last["media"].(string)

	return decision, media
}

func TestHandleMessageMapsTheModelAnswerToAClaim(t *testing.T) {
	loc := capitime.Santiago()
	// The harness clock: Sunday 2026-11-01 21:00 in Santiago.
	now := time.Date(2026, 11, 1, 21, 0, 0, 0, loc)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 11, day, hour, minute, 0, 0, loc)
	}

	cases := []struct {
		name          string
		answer        string
		wantClaimedAt time.Time
		wantImmediate bool
		wantSummary   string
	}{
		{"arrival alone is immediate", `{"arrival":true}`, now, true, "ahora"},
		{"empty fields are immediate", `{"arrival":true,"day":"","clock":"","minutes":0}`, now, true, "ahora"},
		{"null fields are immediate", `{"arrival":true,"day":null,"clock":null,"minutes":null}`, now, true, "ahora"},
		{"today without a time is immediate", `{"arrival":true,"day":"hoy","clock":"","minutes":0}`, now, true, "ahora"},
		{"a clock time later today", `{"arrival":true,"day":"","clock":"22:30","minutes":0}`, at(1, 22, 30), false, "22:30"},
		{"today with a clock time", `{"arrival":true,"day":"hoy","clock":"23:00","minutes":0}`, at(1, 23, 0), false, "23:00"},
		{"a clock time just passed stays today", `{"arrival":true,"day":"","clock":"20:50","minutes":0}`, at(1, 20, 50), false, "20:50"},
		{"a clock time fifteen minutes past stays today", `{"arrival":true,"day":"","clock":"20:45","minutes":0}`, at(1, 20, 45), false, "20:45"},
		{"a clock time sixteen minutes past is tomorrow", `{"arrival":true,"day":"","clock":"20:44","minutes":0}`, at(2, 20, 44), false, "20:44"},
		{"a clock time long passed is tomorrow", `{"arrival":true,"day":"","clock":"20:30","minutes":0}`, at(2, 20, 30), false, "20:30"},
		{"a one-digit hour", `{"arrival":true,"day":"","clock":"9:05","minutes":0}`, at(2, 9, 5), false, "09:05"},
		{"midnight", `{"arrival":true,"day":"","clock":"00:00","minutes":0}`, at(2, 0, 0), false, "00:00"},
		{"one minute", `{"arrival":true,"day":"","clock":"","minutes":1}`, now.Add(time.Minute), false, "en 1 min"},
		{"fifteen minutes", `{"arrival":true,"day":"","clock":"","minutes":15}`, at(1, 21, 15), false, "en 15 min"},
		{"the vague later default", `{"arrival":true,"day":"hoy","clock":"","minutes":30}`, at(1, 21, 30), false, "en 30 min"},
		{"the most minutes accepted", `{"arrival":true,"day":"","clock":"","minutes":720}`, at(2, 9, 0), false, "en 720 min"},
		{"tomorrow without a time", `{"arrival":true,"day":"manana","clock":"","minutes":0}`, at(2, 12, 0), false, "manana"},
		{"tomorrow with the afternoon default", `{"arrival":true,"day":"manana","clock":"18:00","minutes":0}`, at(2, 18, 0), false, "manana"},
		{"tomorrow spelled with its accent", `{"arrival":true,"day":"Mañana","clock":"10:00","minutes":0}`, at(2, 10, 0), false, "manana"},
		{"tomorrow wins over minutes", `{"arrival":true,"day":"manana","clock":"","minutes":30}`, at(2, 12, 0), false, "manana"},
		{"a clock time wins over minutes", `{"arrival":true,"day":"","clock":"22:00","minutes":30}`, at(1, 22, 0), false, "22:00"},
		{"a weekday wins over minutes", `{"arrival":true,"day":"viernes","clock":"","minutes":45}`, at(6, 20, 0), false, "el viernes"},
		{"a day and a clock time win over minutes", `{"arrival":true,"day":"lunes","clock":"19:15","minutes":600}`, at(2, 19, 15), false, "el lunes"},
		{"a weekday without a time", `{"arrival":true,"day":"viernes","clock":"","minutes":0}`, at(6, 20, 0), false, "el viernes"},
		{"a weekday with a clock time", `{"arrival":true,"day":"lunes","clock":"21:30","minutes":0}`, at(2, 21, 30), false, "el lunes"},
		{"the same weekday is a week ahead", `{"arrival":true,"day":"domingo","clock":"","minutes":0}`, at(8, 20, 0), false, "el domingo"},
		{"an accented weekday", `{"arrival":true,"day":" Miércoles ","clock":"","minutes":0}`, at(4, 20, 0), false, "el miercoles"},
		{"saturday", `{"arrival":true,"day":"sábado","clock":"23:59","minutes":0}`, at(7, 23, 59), false, "el sabado"},
		{"tuesday", `{"arrival":true,"day":"martes"}`, at(3, 20, 0), false, "el martes"},
		{"thursday", `{"arrival":true,"day":"jueves"}`, at(5, 20, 0), false, "el jueves"},
		{"a fenced answer", "```json\n{\"arrival\":true,\"clock\":\"22:30\"}\n```", at(1, 22, 30), false, "22:30"},
		{"unknown extra fields are ignored", `{"arrival":true,"minutes":10,"reason":"x"}`, at(1, 21, 10), false, "en 10 min"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, readHarnessOptions{answer: tc.answer})

			reply := h.handle(t, targetMessage(messageText))
			if !reply.Handled {
				t.Fatalf("reply = %+v, want handled", reply)
			}

			pred := reply.Prediction
			if !pred.ClaimedAt.Equal(tc.wantClaimedAt) {
				t.Errorf("ClaimedAt = %v, want %v", pred.ClaimedAt, tc.wantClaimedAt)
			}
			if pred.Immediate != tc.wantImmediate || pred.ClaimSummary != tc.wantSummary {
				t.Errorf("Immediate = %v, ClaimSummary = %q, want %v and %q",
					pred.Immediate, pred.ClaimSummary, tc.wantImmediate, tc.wantSummary)
			}
			if want := tc.wantClaimedAt.Add(60 * time.Minute); !pred.PredictedAt.Equal(want) {
				t.Errorf("PredictedAt = %v, want the claim plus the 60-minute delay %v", pred.PredictedAt, want)
			}
			if decision, _ := h.lastDecision(t); decision != "model_claim" {
				t.Errorf("decision = %q, want model_claim", decision)
			}
		})
	}
}

func TestHandleMessageStaysSilentOnAnAnswerOutsideTheContract(t *testing.T) {
	cases := []struct {
		name         string
		answer       string
		wantDecision string
	}{
		{"no arrival", `{"arrival":false}`, "model_no_claim"},
		{"no arrival with every field", noArrivalJSON, "model_no_claim"},
		{"no arrival wins over leftover fields", `{"arrival":false,"day":"viernes","clock":"22:00","minutes":5}`, "model_no_claim"},
		{"plain text", "the author is on the way", "model_unparseable"},
		{"empty answer", "   ", "model_unparseable"},
		{"JSON array", `[{"arrival":true}]`, "model_unparseable"},
		{"JSON null", `null`, "model_unparseable"},
		{"object without arrival", `{"day":"hoy","clock":"22:00"}`, "model_unparseable"},
		{"arrival as text", `{"arrival":"true"}`, "model_unparseable"},
		{"arrival as null", `{"arrival":null,"clock":"22:00"}`, "model_unparseable"},
		{"truncated object", `{"arrival":true,"day":"ho`, "model_unparseable"},
		{"trailing text after the object", `{"arrival":true} seguro`, "model_unparseable"},
		{"two objects", `{"arrival":true}{"arrival":true}`, "model_unparseable"},
		{"day outside the set", `{"arrival":true,"day":"pasado manana"}`, "model_unparseable"},
		{"day in another language", `{"arrival":true,"day":"friday"}`, "model_unparseable"},
		{"day as a date", `{"arrival":true,"day":"2026-11-06"}`, "model_unparseable"},
		{"day as a number", `{"arrival":true,"day":5}`, "model_unparseable"},
		{"hour past 23", `{"arrival":true,"clock":"24:00"}`, "model_unparseable"},
		{"minute past 59", `{"arrival":true,"clock":"22:60"}`, "model_unparseable"},
		{"twelve-hour clock", `{"arrival":true,"clock":"10pm"}`, "model_unparseable"},
		{"clock inside a phrase", `{"arrival":true,"clock":"a las 22:30"}`, "model_unparseable"},
		{"clock with seconds", `{"arrival":true,"clock":"22:30:00"}`, "model_unparseable"},
		{"clock as a number", `{"arrival":true,"clock":2230}`, "model_unparseable"},
		{"negative minutes", `{"arrival":true,"minutes":-5}`, "model_unparseable"},
		{"minutes past the limit", `{"arrival":true,"minutes":721}`, "model_unparseable"},
		{"fractional minutes", `{"arrival":true,"minutes":1.5}`, "model_unparseable"},
		{"minutes as text", `{"arrival":true,"minutes":"30"}`, "model_unparseable"},
		{"minutes too large for an integer", `{"arrival":true,"minutes":99999999999999999999}`, "model_unparseable"},
		{"a valid clock beside an unknown day", `{"arrival":true,"day":"algun dia","clock":"22:00"}`, "model_unparseable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, readHarnessOptions{answer: tc.answer})

			if reply := h.handle(t, targetMessage(messageText)); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.onlyCall(t)
			if decision, _ := h.lastDecision(t); decision != tc.wantDecision {
				t.Errorf("decision = %q, want %q", decision, tc.wantDecision)
			}
		})
	}
}

func TestHandleMessageTellsTheModelTheCurrentInstantInSantiago(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"monday afternoon", time.Date(2026, 11, 2, 17, 24, 0, 0, capitime.Santiago()), "ahora: lunes 2026-11-02 17:24"},
		{"wednesday morning", time.Date(2026, 11, 4, 9, 5, 0, 0, capitime.Santiago()), "ahora: miercoles 2026-11-04 09:05"},
		{"saturday midnight", time.Date(2026, 11, 7, 0, 0, 0, 0, capitime.Santiago()), "ahora: sabado 2026-11-07 00:00"},
		{"a UTC instant on the previous Santiago day", time.Date(2026, 11, 6, 2, 30, 0, 0, time.UTC), "ahora: jueves 2026-11-05 23:30"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})
			h.clock.Advance(tc.now.Sub(h.clock.Now()))

			h.handle(t, targetMessage(messageText))

			if call := h.onlyCall(t); call.Text != tc.want+"\nmensaje: "+messageText {
				t.Errorf("request text = %q, want it to start with %q", call.Text, tc.want)
			}
		})
	}
}

func TestHandleMessageCommentWithTheCurrentTimeGetsNoReply(t *testing.T) {
	const comment = "Dia lunes 17:24 y se cayo el servidor"

	h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})
	monday := time.Date(2026, 11, 2, 17, 24, 0, 0, capitime.Santiago())
	h.clock.Advance(monday.Sub(h.clock.Now()))

	if reply := h.handle(t, targetMessage(comment)); reply.Handled {
		t.Fatalf("reply = %+v, want silence for a comment that only mentions the current day and time", reply)
	}

	call := h.onlyCall(t)
	if call.Text != "ahora: lunes 2026-11-02 17:24\nmensaje: "+comment {
		t.Errorf("request text = %q, want the comment next to the current instant", call.Text)
	}
	if !strings.Contains(call.Prompt, "solo menciona la fecha o la hora actual") {
		t.Errorf("system instruction %q does not rule out a mention of the current time", call.Prompt)
	}
}

func TestHandleMessageCapsModelReadsPerMinute(t *testing.T) {
	const readCap = 6

	t.Run("the read past the cap is silent and costs no download", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		msg := imageMessage(messageText, attachmentRef())

		for i := range readCap {
			if reply := h.handle(t, msg); !reply.Handled {
				t.Fatalf("reply %d = %+v, want handled below the cap", i+1, reply)
			}
		}

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply past the cap = %+v, want silence", reply)
		}
		if calls := len(h.reader.Calls()); calls != readCap {
			t.Errorf("reader calls = %d, want %d", calls, readCap)
		}
		if fetches := len(h.fetcher.Calls()); fetches != readCap {
			t.Errorf("fetches = %d, want %d: a capped message must not be downloaded", fetches, readCap)
		}
		if decision, media := h.lastDecision(t); decision != "rate_limited" || media != "image" {
			t.Errorf("decision = %q media = %q, want rate_limited with the attached image", decision, media)
		}
	})

	t.Run("the window slides with the clock", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})
		msg := targetMessage(messageText)
		read := func(times int) {
			for range times {
				h.handle(t, msg)
			}
		}

		read(3)
		h.clock.Advance(40 * time.Second)
		read(3)
		h.clock.Advance(10 * time.Second)
		read(1)
		if calls := len(h.reader.Calls()); calls != readCap {
			t.Fatalf("reader calls = %d, want %d: the seventh read inside one minute is capped", calls, readCap)
		}

		// One minute after the first three reads, exactly those three leave the window.
		h.clock.Advance(10 * time.Second)
		read(4)
		if calls := len(h.reader.Calls()); calls != readCap+3 {
			t.Errorf("reader calls = %d, want %d once the oldest three reads left the window", calls, readCap+3)
		}
	})

	t.Run("a message with nothing left to read does not count", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})
		h.fetcher.Fail(stickerURL)

		for range readCap * 2 {
			h.handle(t, imageMessage("", stickerRef("VOY")))
		}
		for range readCap {
			h.handle(t, targetMessage(messageText))
		}
		if calls := len(h.reader.Calls()); calls != readCap {
			t.Errorf("reader calls = %d, want %d", calls, readCap)
		}
	})

	t.Run("each guild has its own cap", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})

		target := capiUserID
		_, err := h.svc.UpdateGuildConfig(t.Context(), capitime.GuildScope{GuildID: guildTwoID, CanManageGuild: true},
			capitime.GuildConfigUpdate{TargetUserID: &target})
		if err != nil {
			t.Fatalf("UpdateGuildConfig: %v", err)
		}

		for range readCap + 1 {
			h.handle(t, targetMessage(messageText))
		}

		other := targetMessage(messageText)
		other.GuildID = guildTwoID
		h.handle(t, other)

		if calls := len(h.reader.Calls()); calls != readCap+1 {
			t.Errorf("reader calls = %d, want %d: the second guild is not capped by the first", calls, readCap+1)
		}
	})
}

func TestHandleMessageLogsWhichStepDecidedWithoutMessageData(t *testing.T) {
	const (
		privateText    = "terminando la pega privada y me sumo"
		privateSticker = "STICKERPRIVADO"
	)

	both := func(content string) capitime.MessageInput {
		msg := imageMessage(content, stickerRef(privateSticker))
		msg.Audio = []capitime.AudioRef{voiceRef()}

		return msg
	}

	cases := []struct {
		name         string
		opts         readHarnessOptions
		prepare      func(h *readHarness)
		msg          capitime.MessageInput
		wantDecision string
		wantMedia    string
	}{
		{name: "model claim on text", msg: targetMessage(privateText), wantDecision: "model_claim", wantMedia: "none"},
		{name: "model claim on text, image and audio", msg: both(privateText), wantDecision: "model_claim", wantMedia: "both"},
		{
			name: "model says no on an image", opts: readHarnessOptions{answer: noArrivalJSON},
			msg: imageMessage("", stickerRef(privateSticker)), wantDecision: "model_no_claim", wantMedia: "image",
		},
		{
			name:    "model fails on an audio",
			prepare: func(h *readHarness) { h.reader.SetError(errors.New("synthetic failure " + privateText)) },
			msg:     audioMessage("", voiceRef()), wantDecision: "model_failed", wantMedia: "audio",
		},
		{
			name: "model answers outside the contract", opts: readHarnessOptions{answer: privateText},
			msg: targetMessage(privateText), wantDecision: "model_unparseable", wantMedia: "none",
		},
		{
			name:    "media that fails to download is not counted as read",
			prepare: func(h *readHarness) { h.fetcher.Fail(stickerURL) },
			msg:     both(privateText), wantDecision: "model_claim", wantMedia: "audio",
		},
		{
			name:    "nothing left to read",
			prepare: func(h *readHarness) { h.fetcher.Fail(stickerURL, voiceURL) },
			msg:     both(""), wantDecision: "no_content", wantMedia: "none",
		},
		{
			name: "only unusable media", msg: imageMessage("", capitime.ImageRef{URL: stickerURL, MIMEType: "application/json"}),
			wantDecision: "no_content", wantMedia: "none",
		},
		{
			name: "no reader configured", opts: readHarnessOptions{noReader: true},
			msg: both(privateText), wantDecision: "no_reader", wantMedia: "both",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, tc.opts)
			if tc.prepare != nil {
				tc.prepare(h)
			}

			h.handle(t, tc.msg)

			entries := h.logs.entries(t)
			if len(entries) != 1 {
				t.Fatalf("log lines = %v, want exactly one per evaluated message", entries)
			}
			if decision, media := h.lastDecision(t); decision != tc.wantDecision || media != tc.wantMedia {
				t.Errorf("decision = %q media = %q, want %q and %q", decision, media, tc.wantDecision, tc.wantMedia)
			}
			for key := range entries[0] {
				isExpectedKey := key == "time" || key == "level" || key == "msg" || key == "decision" ||
					key == "media" || key == "context"
				if !isExpectedKey {
					t.Errorf("log line carries the unexpected field %q: %v", key, entries[0])
				}
			}

			logged := h.logs.String()
			leaks := []string{
				privateText, "pega privada", privateSticker, "llego", "22:30", guildOneID, chanGeneralID, capiUserID,
				msgOneID, stickerURL, voiceURL, "discordapp",
			}
			for _, leaked := range leaks {
				if strings.Contains(logged, leaked) {
					t.Errorf("log %q leaked %q", logged, leaked)
				}
			}
		})
	}
}

func FuzzHandleMessageModelAnswer(f *testing.F) {
	seeds := []string{
		arrivalJSON,
		noArrivalJSON,
		`{"arrival":true}`,
		`{"arrival":true,"day":"viernes","clock":"22:30","minutes":0}`,
		`{"arrival":true,"day":"manana","clock":"","minutes":0}`,
		`{"arrival":true,"day":"hoy","clock":"00:00","minutes":0}`,
		`{"arrival":true,"day":"","clock":"","minutes":720}`,
		`{"arrival":true,"day":"","clock":"","minutes":721}`,
		`{"arrival":true,"day":"","clock":"","minutes":-1}`,
		`{"arrival":true,"day":"","clock":"99:99","minutes":0}`,
		`{"arrival":true,"day":"domingo\u202e","clock":"22:30"}`,
		`{"arrival":true,"day":"<@100000000000000001> @everyone","clock":"22:30"}`,
		`{"arrival":true,"clock":"22:30\n@everyone"}`,
		`{"arrival":true,"minutes":9223372036854775807}`,
		`{"arrival":true,"minutes":1e400}`,
		`{"arrival":"yes"}`,
		`{"arrival":true,"day":{"nested":[1,2,3]}}`,
		"```json\n{\"arrival\":true}\n```",
		"```",
		`null`,
		`[]`,
		`{`,
		``,
		"   \n\t",
		strings.Repeat(`{"arrival":true}`, 300),
		strings.Repeat("a", 5000),
		"{\"arrival\":true,\"day\":\"" + strings.Repeat("\u05d0\u0301\u200b", 200) + "\"}",
		"bad\xff\xfeutf8",
		"{\"arrival\":true,\"clock\":\"22:30\x00\"}",
		`{"arrival":true,"clock":"２２:３０"}`,
		"\U0001F600",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	reader := &fakeReader{}
	svc := newFuzzService(f, clk, reader, &mediaFetcher{failFor: map[string]bool{}})

	f.Fuzz(func(t *testing.T, answer string) {
		// Each input gets a fresh minute, so the per-minute read cap never hides the parser.
		clk.Advance(2 * time.Minute)
		now := clk.Now()
		readsBefore := len(reader.Calls())
		reader.SetResponse(answer)

		reply, err := svc.HandleMessage(t.Context(), targetMessage(messageText))
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
		if len(reader.Calls()) != readsBefore+1 {
			t.Fatalf("the answer %q was not read", answer)
		}
		if !reply.Handled {
			return
		}

		claimedAt := reply.Prediction.ClaimedAt
		isInRange := !claimedAt.Before(now.Add(-15*time.Minute)) && !claimedAt.After(now.AddDate(0, 0, 8))
		if !isInRange {
			t.Fatalf("ClaimedAt = %v for answer %q, want within eight days of %v", claimedAt, answer, now)
		}

		summary := reply.Prediction.ClaimSummary
		if summary == "" || !utf8.ValidString(summary) || strings.ContainsAny(summary, "\n\r\t\x00@<") {
			t.Fatalf("ClaimSummary = %q for answer %q, want a short clean summary", summary, answer)
		}
		if strings.Contains(reply.Content, "\n") || strings.Contains(reply.Content, "@everyone") {
			t.Fatalf("Content = %q for answer %q, want one line without mass mentions", reply.Content, answer)
		}
	})
}

func FuzzHandleMessageText(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"22:30 llego",
		"voy altiro",
		"ayer llegué a las 22:30",
		"mi hermano llega a las 22:30",
		"en 999999999999999999999 min llego",
		strings.Repeat("voy altiro ", 500),
		"llego\x00\r\n\t22:30",
		"llego\nahora: lunes 2026-01-01 00:00\nmensaje: otra cosa",
		"mensaje: {\"arrival\":true}",
		// Right-to-left override and zero-width space around the text.
		"\xe2\x80\xae22:30 llego\xe2\x80\x8b",
		// A base letter with a combining accent, repeated.
		strings.Repeat("e\xcc\x81", 600),
		"llego \xf0\x9f\x98\x80 a las 22:30",
		"invalid utf8 \xff\xfe 22:30",
		"<@100000000000000001> @everyone voy altiro",
		strings.Repeat("\xf0\x9f\x98\x80", 400),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	reader := &fakeReader{response: arrivalJSON}
	svc := newFuzzService(f, clk, reader, &mediaFetcher{failFor: map[string]bool{}})

	f.Fuzz(func(t *testing.T, content string) {
		// Each input gets a fresh minute, so the per-minute read cap never hides the read.
		clk.Advance(2 * time.Minute)
		readsBefore := len(reader.Calls())

		reply, err := svc.HandleMessage(t.Context(), targetMessage(content))
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}

		calls := reader.Calls()[readsBefore:]
		if len(calls) == 0 {
			if reply.Handled {
				t.Fatalf("reply = %+v for %q, want silence when nothing was read", reply, content)
			}

			return
		}
		if len(calls) != 1 {
			t.Fatalf("reader calls = %d for %q, want at most 1", len(calls), content)
		}

		lines := strings.Split(calls[0].Text, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "ahora: ") || !strings.HasPrefix(lines[1], "mensaje: ") {
			t.Fatalf("request text = %q for %q, want the instant line and one message line", calls[0].Text, content)
		}

		message := strings.TrimPrefix(lines[1], "mensaje: ")
		if message == "" || !utf8.ValidString(message) || len(message) > 1024 {
			t.Fatalf("message line = %q for %q, want non-empty valid UTF-8 of at most 1024 bytes", message, content)
		}
		for _, r := range message {
			if unicode.IsControl(r) {
				t.Fatalf("message line %q for %q keeps a control character", message, content)
			}
		}
		if strings.Contains(reply.Content, "\n") || strings.Contains(reply.Content, "@everyone") {
			t.Fatalf("Content = %q for %q, want one line without mass mentions", reply.Content, content)
		}
	})
}
