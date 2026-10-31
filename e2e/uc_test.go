//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func seedSharedUCFixtures(fake *fakediscord.Server) {
	fake.SetUCFixtures([]fakediscord.UCFixture{
		{
			ID:        "9001",
			Date:      "2030-05-11T21:00Z",
			Opponent:  "Deportes Iquique",
			Home:      true,
			Venue:     "Claro Arena",
			TimeValid: true,
		},
		{
			ID:        "9002",
			Date:      "2030-05-19T21:30Z",
			Opponent:  "Colo-Colo",
			Home:      false,
			Venue:     "Estadio Monumental",
			TimeValid: true,
		},
		{
			ID:           "9003",
			Date:         "2030-05-26T04:00Z",
			Opponent:     "Huachipato",
			Home:         true,
			Venue:        "Claro Arena",
			TimeValid:    false,
			StatusDetail: "TBD",
		},
	})
}

func TestUCCommandsAndAvailabilityStatuses(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 2)
	spanishCaller := s.fake.AddUser(fakediscord.User{Name: "Matias", Locale: "es-419"})
	created.add("user snowflake", spanishCaller.ID)
	s.fake.AddMember(s.guild, spanishCaller)

	englishCaller := s.fake.AddUser(fakediscord.User{Name: "Claire", Locale: "en-US"})
	created.add("user snowflake", englishCaller.ID)
	s.fake.AddMember(s.guild, englishCaller)

	observer := s.members[1]
	friendMention := "<@" + sharedUCFriendID + ">"

	// Default no-option /uc and /chatisimo/uc both post publicly in the channel.
	defaultShort := s.slash(t, spanishCaller, "/uc", nil)
	defaultGrouped := s.slash(t, spanishCaller, "/chatisimo/uc", nil)
	if defaultShort.message.EphemeralFor != "" || defaultGrouped.message.EphemeralFor != "" {
		t.Errorf("default /uc ephemeral = (%q, %q), want public messages",
			defaultShort.message.EphemeralFor, defaultGrouped.message.EphemeralFor)
	}
	if !strings.Contains(defaultShort.text, friendMention) {
		t.Errorf("default /uc = %q, want silent friend mention %s", defaultShort.text, friendMention)
	}

	// Named window lookup.
	todayReply := s.slash(t, spanishCaller, "/uc", map[string]any{"window": "today"})
	if todayReply.message.EphemeralFor != "" {
		t.Errorf("/uc window:today EphemeralFor = %q, want public", todayReply.message.EphemeralFor)
	}

	// Scenario A: Confirmed home match (-2h to +3h30m -> 15:00 to 20:30 Santiago).
	blocked := s.slash(t, spanishCaller, "/uc", map[string]any{"date": "2030-05-11"})
	if blocked.message.EphemeralFor != "" {
		t.Errorf("blocked reply EphemeralFor = %q, want public", blocked.message.EphemeralFor)
	}
	for _, want := range []string{"\U0001F534", "Deportes Iquique", "Claro Arena", "15:00", "20:30", "<t:", friendMention} {
		if !strings.Contains(blocked.text, want) {
			t.Errorf("blocked /uc date:2030-05-11 = %q, want substring %q", blocked.text, want)
		}
	}

	isSeenByObserver := false
	for _, msg := range s.fake.Visible(observer, s.guild.ChannelID) {
		if msg.ID == blocked.message.ID {
			isSeenByObserver = true
			break
		}
	}
	if !isSeenByObserver {
		t.Error("other guild member cannot see public /uc reply")
	}

	// Scenario B: Free day warning about nearest upcoming away match (-30m to +2h30m -> 17:00 to 20:00).
	freeDay := s.slash(t, spanishCaller, "/uc", map[string]any{"date": "2030-05-18"})
	for _, want := range []string{"\U0001F7E2", "Colo-Colo", "17:00", "20:00", friendMention} {
		if !strings.Contains(freeDay.text, want) {
			t.Errorf("free /uc date:2030-05-18 = %q, want substring %q", freeDay.text, want)
		}
	}

	// Scenario C: Pending ANFP (TBD kickoff -> yellow light, no placeholder timestamp).
	pending := s.slash(t, spanishCaller, "/uc", map[string]any{"date": "2030-05-26"})
	for _, want := range []string{"\U0001F7E1", "Huachipato", "ANFP", friendMention} {
		if !strings.Contains(pending.text, want) {
			t.Errorf("pending /uc date:2030-05-26 = %q, want substring %q", pending.text, want)
		}
	}
	if strings.Contains(pending.text, "<t:") {
		t.Errorf("pending /uc date:2030-05-26 rendered a Discord timestamp for a TBD match: %q", pending.text)
	}

	// English catalog via /chatisimo/uc.
	englishBlocked := s.slash(t, englishCaller, "/chatisimo/uc", map[string]any{"date": "2030-05-11"})
	for _, want := range []string{"\U0001F534", "Deportes Iquique", "15:00", "20:30", friendMention} {
		if !strings.Contains(englishBlocked.text, want) {
			t.Errorf("english /chatisimo/uc = %q, want substring %q", englishBlocked.text, want)
		}
	}

	// Validation errors are ephemeral and never echo raw user input.
	bothOpts := s.slash(t, spanishCaller, "/uc", map[string]any{"window": "today", "date": "2030-05-11"})
	if bothOpts.message.EphemeralFor != spanishCaller.ID {
		t.Errorf("mutually exclusive options EphemeralFor = %q, want %q", bothOpts.message.EphemeralFor, spanishCaller.ID)
	}

	const badDateInput = "99/99/bad!"
	badDate := s.slash(t, spanishCaller, "/uc", map[string]any{"date": badDateInput})
	if badDate.message.EphemeralFor != spanishCaller.ID {
		t.Errorf("invalid date EphemeralFor = %q, want %q", badDate.message.EphemeralFor, spanishCaller.ID)
	}
	if strings.Contains(badDate.text, badDateInput) {
		t.Errorf("invalid date reply echoed raw input: %q", badDate.text)
	}
}

func TestUCProactiveMatchdayAlertAndRestartDedup(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatalf("load America/Santiago: %v", err)
	}

	b, err := newBackend(t.Context(), shared.bin, shared.coverDir)
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	t.Cleanup(func() {
		closeErr := b.close()
		if closeErr != nil {
			t.Errorf("close backend: %v", closeErr)
		}
	})

	var secrets registry
	secrets.add("application snowflake", b.fake.ApplicationID())
	secrets.add("bot token", b.fake.Token())

	member := b.fake.AddUser(fakediscord.User{Name: "Observer", Locale: "es-419"})
	friend := b.fake.AddUser(fakediscord.User{Name: "Cruzado", Locale: "es-419"})
	guild := b.fake.AddGuild(fakediscord.Guild{Name: "San Carlos Guild"}, member, friend)
	secrets.add("member snowflake", member.ID)
	secrets.add("friend snowflake", friend.ID)
	secrets.add("guild snowflake", guild.ID)
	secrets.add("channel snowflake", guild.ChannelID)

	nowSantiago := time.Now().In(loc)
	kickoffToday := time.Date(
		nowSantiago.Year(), nowSantiago.Month(), nowSantiago.Day(),
		23, 30, 0, 0, loc,
	)
	fixtures := []fakediscord.UCFixture{
		{
			ID:        "9501",
			Date:      kickoffToday.UTC().Format(time.RFC3339),
			Opponent:  "Audax Italiano",
			Home:      true,
			Venue:     "Claro Arena",
			TimeValid: true,
		},
	}

	// Guard against crossing midnight in America/Santiago between test setup and child-process sync.
	nearMidnight := nowSantiago.Hour() == 23 && nowSantiago.Minute() >= 55
	kickoffTomorrow := kickoffToday.AddDate(0, 0, 1)
	if nearMidnight {
		fixtures = append(fixtures, fakediscord.UCFixture{
			ID:        "9502",
			Date:      kickoffTomorrow.UTC().Format(time.RFC3339),
			Opponent:  "Audax Italiano",
			Home:      true,
			Venue:     "Claro Arena",
			TimeValid: true,
		})
	}
	b.fake.SetUCFixtures(fixtures)

	if err := b.configure(t.Context()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	for _, setting := range []struct{ key, value string }{
		{"uc.friend_user_id", friend.ID},
		{"uc.channel_id", guild.ChannelID},
		{"uc.matchday_alert_time", "00:00"},
	} {
		res, setErr := b.runCLI(t.Context(), setting.value, "config", "set", setting.key)
		if setErr != nil || res.code != 0 {
			t.Fatalf("config set %s: err=%v res=%+v", setting.key, setErr, res)
		}
	}

	tempLogs := t.TempDir()
	log1 := filepath.Join(tempLogs, "run1.log")
	p1, err := b.start(log1)
	if err != nil {
		t.Fatalf("start run 1: %v", err)
	}
	if err := awaitReady(t.Context(), b, p1); err != nil {
		if !p1.hasExited() {
			p1.kill()
		}
		t.Fatalf("awaitReady run 1: %v", err)
	}

	countAlerts := func() (int, fakediscord.Message) {
		count := 0
		var last fakediscord.Message
		for _, msg := range b.fake.Visible(member, guild.ChannelID) {
			isMatchdayAlert := msg.EphemeralFor == "" &&
				strings.Contains(msg.Content, "Aviso de utilidad pública") &&
				strings.Contains(msg.Content, "Audax Italiano")
			if isMatchdayAlert {
				count++
				last = msg
			}
		}
		return count, last
	}

	var alertMsg fakediscord.Message
	waitFor(t, "proactive matchday alert in configured channel", func() bool {
		var count int
		count, alertMsg = countAlerts()
		return count >= 1
	})

	wantStartToday := kickoffToday.Add(-2 * time.Hour).In(loc).Format("15:04")
	wantEndToday := kickoffToday.Add(3*time.Hour + 30*time.Minute).In(loc).Format("15:04")
	wantStartTomorrow := kickoffTomorrow.Add(-2 * time.Hour).In(loc).Format("15:04")
	wantEndTomorrow := kickoffTomorrow.Add(3*time.Hour + 30*time.Minute).In(loc).Format("15:04")

	hasTodayWindow := strings.Contains(alertMsg.Content, wantStartToday) && strings.Contains(alertMsg.Content, wantEndToday)
	hasTomorrowWindow := nearMidnight &&
		strings.Contains(alertMsg.Content, wantStartTomorrow) &&
		strings.Contains(alertMsg.Content, wantEndTomorrow)
	if !hasTodayWindow && !hasTomorrowWindow {
		t.Errorf("matchday alert = %q, want block window %s..%s", alertMsg.Content, wantStartToday, wantEndToday)
	}

	for _, want := range []string{"Audax Italiano", "<@" + friend.ID + ">", "<t:"} {
		if !strings.Contains(alertMsg.Content, want) {
			t.Errorf("matchday alert = %q, want substring %q", alertMsg.Content, want)
		}
	}

	for _, record := range b.fake.Outbound() {
		if len(record.Pings) != 0 {
			t.Errorf("record %d (%s) pinged %v, want silent mentions only", record.Seq, record.Source, record.Pings)
		}
	}

	if err := p1.stopGracefully(stopTimeout); err != nil {
		t.Fatalf("stop run 1: %v", err)
	}

	// Restart against the same database: uccalendar.sent_alert must prevent a duplicate alert.
	log2 := filepath.Join(tempLogs, "run2.log")
	p2, err := b.start(log2)
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}
	// Wait for run 2 to connect its Gateway session and answer a /uc slash command (WaitReady is
	// latched from run 1 on the same fake server).
	waitFor(t, "run 2 to answer a /uc slash command", func() bool {
		if p2.hasExited() {
			t.Fatalf("run 2 exited before answering /uc")
		}
		slashRes, slashErr := b.fake.Slash(t.Context(), member, fakediscord.Slash{
			Guild:   &guild,
			Path:    "/uc",
			Options: map[string]any{"window": "tomorrow"},
		})

		return slashErr == nil && slashRes.Status != fakediscord.StatusTimeout && slashRes.Message != nil
	})

	if count, _ := countAlerts(); count != 1 {
		t.Errorf("matchday alert count after restart = %d, want 1 (deduplicated)", count)
	}

	if err := p2.stopGracefully(stopTimeout); err != nil {
		t.Fatalf("stop run 2: %v", err)
	}

	for _, path := range []string{log1, log2} {
		if auditErr := auditLog(path, &secrets); auditErr != nil {
			t.Error(auditErr)
		}
	}
	if fakeErr := auditText("isolated fake discord log", b.fakeLog.String(), &secrets); fakeErr != nil {
		t.Error(fakeErr)
	}
}
