package discord_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	appdiscord "github.com/HectorVeraO/chatisimo/internal/discord"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	routeCreateDM      = "POST /users/@me/channels"
	routeCreateMessage = "POST /channels/{id}/messages"
)

func TestNewNotifierReportsEveryInvalidField(t *testing.T) {
	tests := []struct {
		name     string
		settings appdiscord.Settings
		wantErrs []string
	}{
		{"valid", appdiscord.Settings{Token: config.NewSecret("x"), APIBaseURL: "https://example.com/api/v10"}, nil},
		{"empty token", appdiscord.Settings{APIBaseURL: "https://example.com/api/v10"}, []string{"token"}},
		{"empty URL", appdiscord.Settings{Token: config.NewSecret("x")}, []string{"URL"}},
		{"relative URL", appdiscord.Settings{Token: config.NewSecret("x"), APIBaseURL: "/api/v10"}, []string{"URL"}},
		{"non-http URL", appdiscord.Settings{Token: config.NewSecret("x"), APIBaseURL: "ftp://example.com"}, []string{"URL"}},
		{"URL without host", appdiscord.Settings{Token: config.NewSecret("x"), APIBaseURL: "https://"}, []string{"URL"}},
		{"everything wrong", appdiscord.Settings{}, []string{"token", "URL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier, err := appdiscord.NewNotifier(tt.settings, discardLogger())
			if len(tt.wantErrs) == 0 {
				if err != nil || notifier == nil {
					t.Fatalf("NewNotifier = %v, %v, want a notifier", notifier, err)
				}
				notifier.Close(context.Background())
				return
			}
			if err == nil {
				t.Fatal("NewNotifier succeeded, want an error")
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not report the %s problem", err, want)
				}
			}
		})
	}
}

func TestNewNotifierRejectsANilLogger(t *testing.T) {
	_, err := appdiscord.NewNotifier(appdiscord.Settings{Token: config.NewSecret("x"), APIBaseURL: "https://example.com"}, nil)
	if err == nil {
		t.Fatal("NewNotifier accepted a nil logger")
	}
}

func TestNotifySendsOneDMThatOnlyTheAddresseeSees(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice(userB)

	err := w.notifier.Notify(t.Context(), notice)
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if got := len(w.fake.DMs(w.users[userB])); got != 1 {
		t.Fatalf("addressee has %d DMs, want 1", got)
	}
	for _, other := range []string{userA, userC} {
		if got := len(w.fake.DMs(w.users[other])); got != 0 {
			t.Errorf("a bystander has %d DMs, want 0", got)
		}
	}
	dm := w.fake.DMs(w.users[userB])[0]
	if !strings.Contains(dm.Content, "was cancelled by the organizer") {
		t.Errorf("DM content = %q, want the cancelled text", dm.Content)
	}
	if !strings.Contains(dm.Content, "(unknown server)") {
		t.Errorf("DM content = %q, want the unknown server fallback before the Gateway fills the cache", dm.Content)
	}
}

func TestNotifyNamesTheGuildFromTheCache(t *testing.T) {
	w := newWorld(t)
	// The Gateway fills this cache in production; the test plays the Gateway.
	w.notifier.Caches().AddGuild(discord.Guild{ID: 200000000000000001, Name: guildName})

	err := w.notifier.Notify(t.Context(), cancelledNotice(userB))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	dm := w.fake.DMs(w.users[userB])[0]
	if !strings.Contains(dm.Content, "("+guildName+")") {
		t.Errorf("DM content = %q, want the guild name", dm.Content)
	}
}

func TestNotifyPingsNobody(t *testing.T) {
	w := newWorld(t)

	err := w.notifier.Notify(t.Context(), cancelledNotice(userB))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	assertNoPings(t, w.fake)
}

func assertNoPings(t *testing.T, fake *fakediscord.Server) {
	t.Helper()

	records := fake.Outbound()
	if len(records) == 0 {
		t.Fatal("the fake recorded no outbound message")
	}
	for _, record := range records {
		sent := record.AllowedMentionsSent
		hasEmptyArrays := sent != nil && sent.Parse != nil && sent.Roles != nil && sent.Users != nil &&
			len(sent.Parse) == 0 && len(sent.Roles) == 0 && len(sent.Users) == 0 && !sent.RepliedUser
		if !hasEmptyArrays {
			t.Errorf("record %d allowed_mentions_sent = %+v, want non-null empty arrays", record.Seq, sent)
		}
		if record.Pings == nil || len(record.Pings) != 0 {
			t.Errorf("record %d pings = %v, want an empty list", record.Seq, record.Pings)
		}
	}
}

func TestNotifyDeduplicatesARetryOfTheSameNotification(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice(userB)

	failErr := w.fake.FailNext(routeCreateMessage, 500, 0)
	if failErr != nil {
		t.Fatalf("FailNext: %v", failErr)
	}
	first := w.notifier.Notify(t.Context(), notice)
	if first == nil {
		t.Fatal("Notify succeeded while the fake failed the send")
	}
	for range 2 {
		retryErr := w.notifier.Notify(t.Context(), notice)
		if retryErr != nil {
			t.Fatalf("retry: %v", retryErr)
		}
	}

	if got := len(w.fake.DMs(w.users[userB])); got != 1 {
		t.Errorf("addressee has %d DMs after a failed send and two retries, want 1", got)
	}
	records := w.fake.Outbound()
	if last := records[len(records)-1]; !last.Deduplicated {
		t.Error("the last send was not deduplicated by its nonce")
	}
}

func TestNotifySendsDifferentNotificationsAsDifferentMessages(t *testing.T) {
	w := newWorld(t)

	for range 2 {
		err := w.notifier.Notify(t.Context(), cancelledNotice(userB))
		if err != nil {
			t.Fatalf("Notify: %v", err)
		}
	}

	if got := len(w.fake.DMs(w.users[userB])); got != 2 {
		t.Errorf("addressee has %d DMs for two notifications, want 2", got)
	}
}

// rateLimitingProxy forwards to the fake and answers the first message send with the 429 a
// real Discord sends: a whole-second Retry-After and a bucket header. The fake's own 429 has a
// fractional Retry-After, which disgo cannot parse.
func rateLimitingProxy(t *testing.T, w *world) (*httptest.Server, *atomic.Bool) {
	t.Helper()

	target, err := url.Parse(w.fake.URL())
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}
	forward := httputil.NewSingleHostReverseProxy(target)

	var limited atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		isSend := r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages")
		if isSend && limited.CompareAndSwap(false, true) {
			rw.Header().Set("Retry-After", "1")
			rw.Header().Set("X-RateLimit-Bucket", "test")
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusTooManyRequests)
			_, _ = rw.Write([]byte(`{"message":"You are being rate limited.","retry_after":1,"global":false}`))
			return
		}
		forward.ServeHTTP(rw, r)
	}))
	t.Cleanup(proxy.Close)

	return proxy, &limited
}

func TestNotifyDeliversOnceAfterARateLimit(t *testing.T) {
	w := newWorld(t)
	proxy, limited := rateLimitingProxy(t, w)
	notifier, err := appdiscord.NewNotifier(appdiscord.Settings{
		Token:      config.NewSecret(w.fake.Token()),
		APIBaseURL: proxy.URL + "/api/v10",
	}, discardLogger())
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}
	t.Cleanup(func() { notifier.Close(context.Background()) })

	err = notifier.Notify(t.Context(), cancelledNotice(userB))
	if err != nil {
		t.Fatalf("Notify after a 429: %v", err)
	}

	if !limited.Load() {
		t.Fatal("the proxy never served a 429, so the retry path was not exercised")
	}

	if got := len(w.fake.DMs(w.users[userB])); got != 1 {
		t.Errorf("addressee has %d DMs after a 429, want 1", got)
	}
}

func TestNotifyErrorsCarryOnlyIDsStatusAndCode(t *testing.T) {
	tests := []struct {
		name            string
		arrange         func(*testing.T, *world)
		to              string
		wantErrorSuffix string
		wantUndelivered bool
	}{
		{
			name:            "DMs closed",
			arrange:         func(t *testing.T, w *world) { mustSetDMsOpen(t, w, userB, false) },
			to:              userB,
			wantErrorSuffix: "discord status 403 code 50007",
			wantUndelivered: true,
		},
		{
			name:            "no mutual guild when opening the DM",
			arrange:         func(t *testing.T, w *world) { mustFail(t, w, routeCreateDM, 403, 50278) },
			to:              userB,
			wantErrorSuffix: "discord status 403 code 50278",
			wantUndelivered: true,
		},
		{
			name:            "no mutual guild when sending",
			arrange:         func(t *testing.T, w *world) { mustFail(t, w, routeCreateMessage, 403, 50278) },
			to:              userB,
			wantErrorSuffix: "discord status 403 code 50278",
			wantUndelivered: true,
		},
		{
			name:            "server error",
			arrange:         func(t *testing.T, w *world) { mustFail(t, w, routeCreateMessage, 500, 0) },
			to:              userB,
			wantErrorSuffix: "discord status 500 code 0",
		},
		{
			name:            "other client error is retried",
			arrange:         func(t *testing.T, w *world) { mustFail(t, w, routeCreateDM, 403, 50001) },
			to:              userB,
			wantErrorSuffix: "discord status 403 code 50001",
		},
		{
			name:            "unknown user",
			arrange:         func(*testing.T, *world) {},
			to:              userGap,
			wantErrorSuffix: "discord status 400 code 50035",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			tt.arrange(t, w)
			notice := cancelledNotice(tt.to)

			err := w.notifier.Notify(t.Context(), notice)

			if err == nil {
				t.Fatal("Notify succeeded, want an error")
			}
			want := "notify " + notice.ID.String() + " cancelled: " + tt.wantErrorSuffix
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if got := errors.Is(err, giftexchange.ErrUndeliverable); got != tt.wantUndelivered {
				t.Errorf("errors.Is(ErrUndeliverable) = %v, want %v", got, tt.wantUndelivered)
			}
			if errors.As(err, new(*rest.Error)) {
				t.Error("the returned error still holds a *rest.Error, which carries the request and its token")
			}
		})
	}
}

func TestNotifyOfAPersonWithoutADiscordIdentityIsUndeliverable(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice(userB)
	notice.To.Identities = nil

	err := w.notifier.Notify(t.Context(), notice)

	if !errors.Is(err, giftexchange.ErrUndeliverable) {
		t.Fatalf("error = %v, want ErrUndeliverable", err)
	}
	if len(w.fake.Outbound()) != 0 {
		t.Error("the notifier called Discord for a person with no Discord identity")
	}
}

func TestNotifyRejectsAnIdentityThatIsNotASnowflake(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice("not-a-snowflake")

	err := w.notifier.Notify(t.Context(), notice)

	if !errors.Is(err, giftexchange.ErrUndeliverable) {
		t.Fatalf("error = %v, want ErrUndeliverable", err)
	}
	if strings.Contains(err.Error(), "not-a-snowflake") {
		t.Errorf("error %q repeats the rejected identity", err)
	}
}

func FuzzNotifyIdentitySubject(f *testing.F) {
	seeds := []string{
		"",
		userB,
		"not-a-snowflake",
		"-1",
		"0",
		"18446744073709551616",
		strings.Repeat("9", 4096),
		"\u202eRTL\u200b",
		"e\u0301\u0301\u0301",
		"<@123456789012345678>",
		"@everyone",
		"🎁",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	w := newWorld(f)

	f.Fuzz(func(t *testing.T, subject string) {
		notice := cancelledNotice(subject)

		err := w.notifier.Notify(t.Context(), notice)

		_, parseErr := strconv.ParseUint(subject, 10, 64)
		isInvalidSubject := parseErr != nil
		if isInvalidSubject && !errors.Is(err, giftexchange.ErrUndeliverable) {
			t.Fatalf("subject %q: error = %v, want ErrUndeliverable", subject, err)
		}
		if isInvalidSubject && len(subject) > 8 && strings.Contains(err.Error(), subject) {
			t.Errorf("error %q repeats the identity subject", err)
		}
	})
}

func TestNotifyRejectsAZeroNotificationID(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice(userB)
	notice.ID = giftexchange.NotificationID{}

	err := w.notifier.Notify(t.Context(), notice)

	if err == nil {
		t.Fatal("Notify accepted a zero notification ID")
	}
	if len(w.fake.Outbound()) != 0 {
		t.Error("the notifier called Discord with a zero notification ID")
	}
}

func TestNotifyRejectsANotificationItCannotRender(t *testing.T) {
	w := newWorld(t)
	notice := cancelledNotice(userB)
	notice.Kind = giftexchange.KindDrawn

	err := w.notifier.Notify(t.Context(), notice)

	if err == nil {
		t.Fatal("Notify accepted a drawn notification with no counterpart")
	}
	if errors.Is(err, giftexchange.ErrUndeliverable) {
		t.Error("a rendering failure must be retried or alerted on, not cancelled as undeliverable")
	}
	if len(w.fake.Outbound()) != 0 {
		t.Error("the notifier called Discord for a notification it could not render")
	}
}

func TestNotifyTransportFailureNamesTheOperationButNotTheURL(t *testing.T) {
	w := newWorld(t)
	closeErr := w.fake.Close()
	if closeErr != nil {
		t.Fatalf("Close fake: %v", closeErr)
	}
	notice := cancelledNotice(userB)

	err := w.notifier.Notify(t.Context(), notice)

	if err == nil {
		t.Fatal("Notify succeeded against a closed server")
	}
	want := "notify " + notice.ID.String() + " cancelled: create dm failed"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if _, found := firstFound(err.Error(), "127.0.0.1", "http", "dial", userB, w.fake.Token()); found {
		t.Errorf("error %q leaks the URL, the snowflake or the token", err)
	}
	if errors.Is(err, giftexchange.ErrUndeliverable) {
		t.Error("a transport failure must be retried")
	}
}

func TestNotifyStopsWhenTheContextIsCancelled(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := w.notifier.Notify(ctx, cancelledNotice(userB))

	if err == nil {
		t.Fatal("Notify succeeded with a cancelled context")
	}
	if len(w.fake.DMs(w.users[userB])) != 0 {
		t.Error("a DM was delivered with a cancelled context")
	}
}

func TestNotifyRendersHostileNamesAsLiteralText(t *testing.T) {
	names := []string{
		"@everyone",
		"<@" + userGap + ">",
		"**bold** ||spoiler|| `code`",
		"\u202eRTL\u202c zero\u200bwidth",
		strings.Repeat("@everyone ", 10),
	}
	for _, name := range names {
		w := newWorld(t)
		notice := cancelledNotice(userB)
		notice.Exchange.Name = name

		err := w.notifier.Notify(t.Context(), notice)
		if err != nil {
			t.Fatalf("Notify(%q): %v", name, err)
		}

		assertNoPings(t, w.fake)
		for _, record := range w.fake.Outbound() {
			if len(record.MentionsRendered) != 0 {
				t.Errorf("name %q rendered mentions %v", name, record.MentionsRendered)
			}
		}
	}
}

func TestDrawDeliversOneRecipientDMToEachGiver(t *testing.T) {
	w := newWorld(t)
	s := newService(t, w)
	exchange := s.drawThree(t)

	s.startRunner(t, w)
	waitFor(t, "the drawn notices to finish", func() bool {
		return finishedCount(t, exchange, "delivered") == 3
	})

	recipients := map[string]bool{}
	for _, giver := range []string{userA, userB, userC} {
		dms := w.fake.DMs(w.users[giver])
		if len(dms) != 1 {
			t.Fatalf("giver has %d DMs, want 1", len(dms))
		}
		title, description := embedOf(t, dms[0])
		if title != "Your recipient" {
			t.Errorf("embed title = %q, want Your recipient", title)
		}
		self := "<@" + giver + ">"
		if description == self {
			t.Error("a giver was told their own name as the recipient")
		}
		recipients[description] = true
	}
	if len(recipients) != 3 {
		t.Errorf("the givers were told %d distinct recipients, want 3 (a full cycle)", len(recipients))
	}

	assertNoPings(t, w.fake)
	for _, record := range w.fake.Outbound() {
		if len(record.VisibleTo) != 1 {
			t.Errorf("DM %d is visible to %v, want its addressee only", record.Seq, record.VisibleTo)
		}
	}
}

func TestNotifyRendersPreferencesUpdatedDMWithEscapedFields(t *testing.T) {
	w := newWorld(t)
	counterpart := giftexchange.PersonRef{
		Identities: []giftexchange.ExternalIdentity{
			{Provider: giftexchange.ProviderDiscord, Subject: userC},
		},
	}
	notice := cancelledNotice(userB)
	notice.Kind = giftexchange.KindPreferencesUpdated
	notice.Counterpart = &counterpart
	notice.Preferences = giftexchange.Preferences{
		Likes:    "@everyone **coffee**",
		Dislikes: "Peanuts",
		Details:  "Size M",
	}

	err := w.notifier.Notify(t.Context(), notice)
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	dms := w.fake.DMs(w.users[userB])
	if len(dms) != 1 {
		t.Fatalf("addressee has %d DMs, want 1", len(dms))
	}
	if !strings.Contains(dms[0].Content, "updated their gift preferences") {
		t.Errorf("DM content = %q, want updated preferences text", dms[0].Content)
	}
	embedJSON := string(dms[0].Embeds[0])
	for _, want := range []string{`\\@everyone \\*\\*coffee\\*\\*`, "Peanuts", "Size M"} {
		if !strings.Contains(embedJSON, want) {
			t.Errorf("DM embed = %s, want %q", embedJSON, want)
		}
	}
	assertNoPings(t, w.fake)
}

var snowflakePattern = regexp.MustCompile(`[0-9]{17,20}`)

func TestFailedDeliveriesLeaveNoSnowflakeTokenOrMessageTextInJobsOrLogs(t *testing.T) {
	w := newWorld(t)
	s := newService(t, w)
	mustSetDMsOpen(t, w, userC, false)
	mustFail(t, w, routeCreateMessage, 500, 0)
	exchange := s.drawThree(t)

	s.startRunner(t, w)
	waitFor(t, "the first attempts to run", func() bool {
		return finishedCount(t, exchange, "delivered")+finishedCount(t, exchange, "undeliverable") >= 1 &&
			failedAttempts(t, s.queue) >= 1
	})
	s.clock.Advance(time.Hour)
	waitFor(t, "every notice to finish", func() bool {
		return finishedCount(t, exchange, "delivered") == 2 && finishedCount(t, exchange, "undeliverable") == 1
	})

	jobText := jobsText(t, s.queue)
	if !strings.Contains(jobText, "discord status 500 code 0") {
		t.Fatalf("the jobs table has no record of the failed attempt, so this check proves nothing: %s", jobText)
	}
	logText := w.logs.String()
	forbidden := []string{
		userA, userB, userC, guildID, w.fake.ApplicationID(), w.fake.Token(),
		exchangeName, guildName, "Your recipient", "The draw for", "was cancelled",
	}
	for _, source := range []struct{ name, text string }{{"river_job", jobText}, {"logs", logText}} {
		if found, isLeak := firstFound(source.text, forbidden...); isLeak {
			t.Errorf("%s contain %q", source.name, found)
		}
		if match := snowflakePattern.FindString(source.text); match != "" {
			t.Errorf("%s contain a snowflake-shaped number %q", source.name, match)
		}
	}
}

// failedAttempts counts the jobs of the queue that already recorded an error.
func failedAttempts(t *testing.T, queue string) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM river.river_job WHERE queue = $1 AND cardinality(errors) > 0", queue).Scan(&count)
	if err != nil {
		t.Fatalf("count failed jobs: %v", err)
	}

	return count
}

// jobsText returns the args and errors of every job of the queue, as the jobs table stores them.
func jobsText(t *testing.T, queue string) string {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT args::text, coalesce(errors::text, '') FROM river.river_job WHERE queue = $1", queue)
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}
	defer rows.Close()

	var text strings.Builder
	for rows.Next() {
		var args, errs string
		scanErr := rows.Scan(&args, &errs)
		if scanErr != nil {
			t.Fatalf("scan job: %v", scanErr)
		}
		text.WriteString(args)
		text.WriteString(errs)
	}
	if rows.Err() != nil {
		t.Fatalf("read jobs: %v", rows.Err())
	}

	return text.String()
}

func mustSetDMsOpen(t *testing.T, w *world, subject string, open bool) {
	t.Helper()

	err := w.fake.SetDMsOpen(w.users[subject], open)
	if err != nil {
		t.Fatalf("SetDMsOpen: %v", err)
	}
}

func mustFail(t *testing.T, w *world, route string, status, code int) {
	t.Helper()

	err := w.fake.FailNext(route, status, code)
	if err != nil {
		t.Fatalf("FailNext: %v", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sampleUCChannelAlertMatch(t testing.TB) uccalendar.Match {
	t.Helper()

	tz, err := time.LoadLocation(uccalendar.SantiagoTimezone)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	kickoff := time.Date(2026, time.November, 7, 18, 0, 0, 0, tz)
	m := uccalendar.Match{
		ID:         "401100",
		Opponent:   "Colo-Colo",
		Tournament: "Liga de Primera",
		Venue:      "Claro Arena",
		Condition:  uccalendar.VenueHome,
		TBD:        false,
		Date:       uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 7},
		Kickoff:    kickoff,
		BlockStart: kickoff.Add(-2 * time.Hour),
		BlockEnd:   kickoff.Add(3*time.Hour + 30*time.Minute),
	}

	return m
}

func TestSendUCAlertDeliversFridayAndMatchdayAlertsWithSilentMentionsAndCleanLogs(t *testing.T) {
	w := newWorld(t)
	match := sampleUCChannelAlertMatch(t)

	fridayAlert := uccalendar.Alert{
		Kind:           uccalendar.AlertFridayWeekend,
		FriendUserID:   userA,
		Banter:         "Preparen el finde sin contar con la franja",
		WeekendStart:   uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 6},
		WeekendEnd:     uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8},
		SaturdayStatus: uccalendar.StatusBlocked,
		SundayStatus:   uccalendar.StatusFree,
		Matches:        []uccalendar.Match{match},
	}
	err := w.notifier.SendUCAlert(t.Context(), w.guild.ChannelID, fridayAlert)
	if err != nil {
		t.Fatalf("SendUCAlert(friday): %v", err)
	}

	matchdayAlert := uccalendar.Alert{
		Kind:         uccalendar.AlertMatchdayMorning,
		FriendUserID: userA,
		Banter:       "Hoy desaparece por la Cato",
		Matches:      []uccalendar.Match{match},
	}
	err = w.notifier.SendUCAlert(t.Context(), w.guild.ChannelID, matchdayAlert)
	if err != nil {
		t.Fatalf("SendUCAlert(matchday): %v", err)
	}

	visible := w.fake.Visible(w.users[userB], w.guild.ChannelID)
	if len(visible) != 2 {
		t.Fatalf("guild channel has %d visible messages, want 2", len(visible))
	}
	if !strings.Contains(visible[0].Content, "Reporte de disponibilidad de <@"+userA+"> para el finde (07/11-08/11)") {
		t.Errorf("friday message = %q, want weekend availability header with silent mention", visible[0].Content)
	}
	if !strings.Contains(visible[0].Content, "s\u00e1bado 07/11 bloqueado de 16:00 a 21:30 hrs") {
		t.Errorf("friday message = %q, want blocked Saturday window", visible[0].Content)
	}
	if !strings.Contains(visible[0].Content, "domingo 08/11 libre") {
		t.Errorf("friday message = %q, want free Sunday line", visible[0].Content)
	}
	if !strings.Contains(visible[1].Content, "Aviso de utilidad p\u00fablica:") ||
		!strings.Contains(visible[1].Content, "<@"+userA+"> desaparece del mapa entre las **16:00 y las 21:30 hrs**.") {
		t.Errorf("matchday message = %q, want matchday warning with silent mention", visible[1].Content)
	}

	assertNoPings(t, w.fake)
	records := w.fake.Outbound()
	for i, rec := range records {
		if len(rec.MentionsRendered) != 1 || !strings.Contains(rec.MentionsRendered[0], userA) {
			t.Errorf("record[%d] MentionsRendered = %v, want silent mention of userA", i, rec.MentionsRendered)
		}
	}

	logText := w.logs.String()
	for _, wantLog := range []string{`"msg":"uc alert sent"`, `"kind":"friday_weekend"`, `"kind":"matchday_morning"`} {
		if !strings.Contains(logText, wantLog) {
			t.Errorf("logs missing %s in:\n%s", wantLog, logText)
		}
	}
	forbidden := []string{
		userA, userB, userC, guildID, w.guild.ChannelID, w.fake.Token(), w.fake.ApplicationID(),
		"Colo-Colo", "Claro Arena", "Preparen el finde", "Hoy desaparece",
	}
	if found, isLeak := firstFound(logText, forbidden...); isLeak {
		t.Errorf("logs leaked %q:\n%s", found, logText)
	}
	if snow := snowflakePattern.FindString(logText); snow != "" {
		t.Errorf("logs contain snowflake-shaped number %q:\n%s", snow, logText)
	}
}

func TestSendUCAlertRejectsBadChannelOrAlertAndReducesRESTErrors(t *testing.T) {
	match := sampleUCChannelAlertMatch(t)
	validAlert := uccalendar.Alert{
		Kind:    uccalendar.AlertMatchdayMorning,
		Matches: []uccalendar.Match{match},
	}

	t.Run("nil notifier and aggregated invalid arguments", func(t *testing.T) {
		var nilNotifier *appdiscord.Notifier
		if err := nilNotifier.SendUCAlert(t.Context(), "1", validAlert); err == nil {
			t.Fatal("nilNotifier.SendUCAlert succeeded, want error")
		}

		w := newWorld(t)
		//nolint:staticcheck // testing aggregated nil context + invalid channel + unrenderable alert
		joinedErr := w.notifier.SendUCAlert(nil, "0", uccalendar.Alert{Kind: uccalendar.AlertMatchdayMorning})
		if joinedErr == nil ||
			!strings.Contains(joinedErr.Error(), "context is nil") ||
			!strings.Contains(joinedErr.Error(), "invalid channel snowflake") {
			t.Errorf("joined SendUCAlert error = %v, want context + channel errors", joinedErr)
		}
	})

	t.Run("invalid channel snowflake does not leak channelID", func(t *testing.T) {
		w := newWorld(t)
		badChannel := "not-a-channel-999999999999999999"

		err := w.notifier.SendUCAlert(t.Context(), badChannel, validAlert)
		if err == nil {
			t.Fatal("SendUCAlert accepted invalid channelID")
		}
		if strings.Contains(err.Error(), badChannel) || snowflakePattern.FindString(err.Error()) != "" {
			t.Errorf("error %q leaked channel input", err)
		}
		if len(w.fake.Outbound()) != 0 {
			t.Error("SendUCAlert called Discord with an invalid channelID")
		}
	})

	t.Run("zero channel snowflake is rejected", func(t *testing.T) {
		w := newWorld(t)

		err := w.notifier.SendUCAlert(t.Context(), "0", validAlert)
		if err == nil {
			t.Fatal("SendUCAlert accepted zero channelID")
		}
	})

	t.Run("unrenderable alert fails before REST call", func(t *testing.T) {
		w := newWorld(t)

		err := w.notifier.SendUCAlert(t.Context(), w.guild.ChannelID, uccalendar.Alert{
			Kind: uccalendar.AlertMatchdayMorning,
		})
		if err == nil {
			t.Fatal("SendUCAlert accepted matchday alert without matches")
		}
		if len(w.fake.Outbound()) != 0 {
			t.Error("SendUCAlert called Discord for an unrenderable alert")
		}
	})

	t.Run("REST failure reduces to status and code only", func(t *testing.T) {
		w := newWorld(t)
		mustFail(t, w, routeCreateMessage, 403, 50001)

		err := w.notifier.SendUCAlert(t.Context(), w.guild.ChannelID, validAlert)
		if err == nil {
			t.Fatal("SendUCAlert succeeded when Discord returned 403")
		}
		want := "send uc alert matchday_morning: discord status 403 code 50001"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		if errors.As(err, new(*rest.Error)) {
			t.Error("returned error wraps *rest.Error")
		}
	})

	t.Run("transport failure names operation without URL or channel snowflake", func(t *testing.T) {
		w := newWorld(t)
		closeErr := w.fake.Close()
		if closeErr != nil {
			t.Fatalf("Close fake: %v", closeErr)
		}

		err := w.notifier.SendUCAlert(t.Context(), w.guild.ChannelID, validAlert)
		if err == nil {
			t.Fatal("SendUCAlert succeeded against closed server")
		}
		want := "send uc alert matchday_morning: send message failed"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		if _, leaked := firstFound(err.Error(), "127.0.0.1", "http", w.guild.ChannelID, w.fake.Token()); leaked {
			t.Errorf("error %q leaked transport details", err)
		}
	})
}

func FuzzSendUCAlertChannelID(f *testing.F) {
	seeds := []string{
		"",
		"0",
		"-1",
		"not-a-snowflake",
		"18446744073709551616",
		strings.Repeat("9", 4096),
		"\u202eRTL\u200b",
		"e\u0301\u0301\u0301",
		"\U0001f600",
		"<#200000000000000001>",
		"@everyone",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	w := newWorld(f)
	match := sampleUCChannelAlertMatch(f)
	alert := uccalendar.Alert{
		Kind:    uccalendar.AlertMatchdayMorning,
		Matches: []uccalendar.Match{match},
	}

	f.Fuzz(func(t *testing.T, channelID string) {
		err := w.notifier.SendUCAlert(t.Context(), channelID, alert)

		parsed, parseErr := strconv.ParseUint(channelID, 10, 64)
		isInvalid := parseErr != nil || parsed == 0
		if isInvalid && err == nil {
			t.Fatalf("SendUCAlert(%q) succeeded, want error", channelID)
		}
		if err != nil && len(channelID) > 8 && strings.Contains(err.Error(), channelID) {
			t.Errorf("error %q repeats channelID %q", err, channelID)
		}
	})
}
