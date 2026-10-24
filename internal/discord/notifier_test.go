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
	w.notifier.AddGuildToCache(discord.Guild{ID: 200000000000000001, Name: guildName})

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
func rateLimitingProxy(t *testing.T, w *world) *httptest.Server {
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

	return proxy
}

func TestNotifyDeliversOnceAfterARateLimit(t *testing.T) {
	w := newWorld(t)
	proxy := rateLimitingProxy(t, w)
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
