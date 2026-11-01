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

	friend := s.fake.AddUser(fakediscord.User{Name: "Cruzado", Locale: "es-419"})
	created.add("user snowflake", friend.ID)
	s.fake.AddMember(s.guild, friend)

	observer := s.members[1]
	friendMention := "<@" + friend.ID + ">"

	// Before configuration, /uc defaults to es-CL with UC white/blue circles and generic fallback copy without mentions,
	// regardless of whether the caller uses es-419 or en-US in Discord.
	unconfigured := s.slash(t, englishCaller, "/uc", map[string]any{"date": "2030-05-11"})
	if unconfigured.message.EphemeralFor != "" {
		t.Errorf("unconfigured /uc EphemeralFor = %q, want public", unconfigured.message.EphemeralFor)
	}
	if !strings.Contains(unconfigured.text, "\u26aa\U0001f535") ||
		!strings.Contains(unconfigured.text, "el hincha cruzado") ||
		strings.Contains(unconfigured.text, "<@") {
		t.Errorf("unconfigured /uc = %q, want es-CL UC-branded generic fallback without mention", unconfigured.text)
	}

	// Non-managers cannot mutate or inspect guild UC configuration.
	deniedSet := s.slash(t, spanishCaller, "/uc-config/set", map[string]any{"friend": friend})
	if deniedSet.message.EphemeralFor != spanishCaller.ID || strings.Contains(deniedSet.text, friendMention) {
		t.Errorf("non-manager /uc-config/set = %q (ephemeral=%q), want ephemeral refusal",
			deniedSet.text, deniedSet.message.EphemeralFor)
	}

	deniedShow := s.slash(t, englishCaller, "/uc-config/show", nil)
	if deniedShow.message.EphemeralFor != englishCaller.ID {
		t.Errorf("non-manager /uc-config/show EphemeralFor = %q, want %q",
			deniedShow.message.EphemeralFor, englishCaller.ID)
	}

	// Grant ManageGuild ("32") to both managers, configure the guild's UC-fan friend, and inspect it.
	if err := s.fake.SetMemberPermissions(s.guild, spanishCaller, "32"); err != nil {
		t.Fatalf("SetMemberPermissions spanishCaller: %v", err)
	}
	if err := s.fake.SetMemberPermissions(s.guild, englishCaller, "32"); err != nil {
		t.Fatalf("SetMemberPermissions englishCaller: %v", err)
	}

	setReply := s.slash(t, spanishCaller, "/uc-config/set", map[string]any{"friend": friend})
	if setReply.message.EphemeralFor != spanishCaller.ID ||
		!strings.Contains(setReply.text, friendMention) ||
		!strings.Contains(setReply.text, "es-CL (por defecto)") {
		t.Fatalf("/uc-config/set = %q (ephemeral=%q), want ephemeral confirmation mentioning %s and default es-CL",
			setReply.text, setReply.message.EphemeralFor, friendMention)
	}

	showReply := s.slash(t, englishCaller, "/uc-config/show", nil)
	if showReply.message.EphemeralFor != englishCaller.ID ||
		!strings.Contains(showReply.text, friendMention) ||
		!strings.Contains(showReply.text, "es-CL (default)") {
		t.Errorf("/uc-config/show = %q (ephemeral=%q), want ephemeral config mentioning %s and es-CL (default)",
			showReply.text, showReply.message.EphemeralFor, friendMention)
	}

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
	for _, want := range []string{"\u26aa\U0001f535", "\U0001F534", "\u26bd", "Deportes Iquique", "Claro Arena", "15:00", "20:30", "<t:", friendMention} {
		if !strings.Contains(blocked.text, want) {
			t.Errorf("blocked /uc date:2030-05-11 = %q, want substring %q", blocked.text, want)
		}
	}
	if strings.Contains(blocked.text, "("+friendMention+")") {
		t.Errorf("blocked /uc date:2030-05-11 duplicated trailing friend mention: %q", blocked.text)
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
	for _, want := range []string{"\u26aa\U0001f535", "\U0001F7E2", "Colo-Colo", "17:00", "20:00", friendMention, "-# Pr\u00f3ximo partido:"} {
		if !strings.Contains(freeDay.text, want) {
			t.Errorf("free /uc date:2030-05-18 = %q, want substring %q", freeDay.text, want)
		}
	}

	// Scenario C: Pending ANFP (TBD kickoff -> yellow light, no placeholder timestamp).
	pending := s.slash(t, spanishCaller, "/uc", map[string]any{"date": "2030-05-26"})
	for _, want := range []string{"\u26aa\U0001f535", "\U0001F7E1", "Huachipato", "ANFP", friendMention} {
		if !strings.Contains(pending.text, want) {
			t.Errorf("pending /uc date:2030-05-26 = %q, want substring %q", pending.text, want)
		}
	}
	if strings.Contains(pending.text, "<t:") {
		t.Errorf("pending /uc date:2030-05-26 rendered a Discord timestamp for a TBD match: %q", pending.text)
	}

	// Switch guild UC language to en-US via /uc-config set, verify /chatisimo/uc renders English, then clear locale back to es-CL.
	setEN := s.slash(t, englishCaller, "/uc-config/set", map[string]any{"locale": "en-US"})
	if setEN.message.EphemeralFor != englishCaller.ID || !strings.Contains(setEN.text, "- **Message language:** en-US") {
		t.Fatalf("/uc-config/set locale:en-US = %q (ephemeral=%q), want en-US confirmation", setEN.text, setEN.message.EphemeralFor)
	}

	englishBlocked := s.slash(t, spanishCaller, "/chatisimo/uc", map[string]any{"date": "2030-05-11"})
	for _, want := range []string{"\u26aa\U0001f535 \U0001f534 **Blocked", "Deportes Iquique", "15:00", "20:30", friendMention} {
		if !strings.Contains(englishBlocked.text, want) {
			t.Errorf("english /chatisimo/uc = %q, want substring %q", englishBlocked.text, want)
		}
	}

	clearLocale := s.slash(t, englishCaller, "/uc-config/clear", map[string]any{"target": "locale"})
	if clearLocale.message.EphemeralFor != englishCaller.ID || !strings.Contains(clearLocale.text, "es-CL (default)") {
		t.Fatalf("/uc-config/clear target:locale = %q (ephemeral=%q), want es-CL (default)", clearLocale.text, clearLocale.message.EphemeralFor)
	}

	// Cross-guild and DM isolation: neither another guild nor a bot DM mentions s.guild's configured friend, and both default to es-CL.
	otherGuild := newGuild(t, 1)
	otherReply := otherGuild.slash(t, otherGuild.members[0], "/uc", map[string]any{"date": "2030-05-11"})
	if strings.Contains(otherReply.text, friend.ID) ||
		!strings.Contains(otherReply.text, "\u26aa\U0001f535") ||
		!strings.Contains(otherReply.text, "el hincha cruzado") {
		t.Errorf("unconfigured other guild /uc = %q, want default es-CL fallback without %s",
			otherReply.text, friend.ID)
	}

	dmReply := s.dmSlash(t, englishCaller, "/uc", map[string]any{"date": "2030-05-11"})
	if strings.Contains(dmReply.text, friend.ID) ||
		!strings.Contains(dmReply.text, "\u26aa\U0001f535") ||
		!strings.Contains(dmReply.text, "el hincha cruzado") {
		t.Errorf("bot DM /uc = %q, want default es-CL fallback without %s", dmReply.text, friend.ID)
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
	alertChannelID := b.fake.AddChannel(guild, "uc-alerts")
	if err := b.fake.SetMemberPermissions(guild, member, "32"); err != nil {
		t.Fatalf("SetMemberPermissions member: %v", err)
	}

	otherMember := b.fake.AddUser(fakediscord.User{Name: "AwayManager", Locale: "es-419"})
	otherGuild := b.fake.AddGuild(fakediscord.Guild{Name: "Away Guild"}, otherMember)
	if err := b.fake.SetMemberPermissions(otherGuild, otherMember, "32"); err != nil {
		t.Fatalf("SetMemberPermissions otherMember: %v", err)
	}

	secrets.add("member snowflake", member.ID)
	secrets.add("friend snowflake", friend.ID)
	secrets.add("guild snowflake", guild.ID)
	secrets.add("general channel snowflake", guild.ChannelID)
	secrets.add("alert channel snowflake", alertChannelID)
	secrets.add("other member snowflake", otherMember.ID)
	secrets.add("other guild snowflake", otherGuild.ID)
	secrets.add("other channel snowflake", otherGuild.ChannelID)

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
	res, setErr := b.runCLI(t.Context(), "00:00", "config", "set", "uc.matchday_alert_time")
	if setErr != nil || res.code != 0 {
		t.Fatalf("config set uc.matchday_alert_time: err=%v res=%+v", setErr, res)
	}

	// Run 1: configure per-guild UC targets via Discord slash commands (/uc-config set).
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

	sceneA := &scene{fake: b.fake, guild: guild}
	cfgA := sceneA.slash(t, member, "/uc-config/set", map[string]any{
		"friend":  friend,
		"channel": alertChannelID,
	})
	if cfgA.message.EphemeralFor != member.ID || !strings.Contains(cfgA.text, "<#"+alertChannelID+">") {
		t.Fatalf("guild A /uc-config/set = %q (ephemeral=%q)", cfgA.text, cfgA.message.EphemeralFor)
	}

	sceneB := &scene{fake: b.fake, guild: otherGuild}
	cfgB := sceneB.slash(t, otherMember, "/uc-config/set", map[string]any{
		"channel": otherGuild.ChannelID,
	})
	if cfgB.message.EphemeralFor != otherMember.ID || !strings.Contains(cfgB.text, "<#"+otherGuild.ChannelID+">") {
		t.Fatalf("guild B /uc-config/set = %q (ephemeral=%q)", cfgB.text, cfgB.message.EphemeralFor)
	}

	if err := p1.stopGracefully(stopTimeout); err != nil {
		t.Fatalf("stop run 1: %v", err)
	}

	countAlertsIn := func(viewer fakediscord.User, channelID string) (int, fakediscord.Message) {
		count := 0
		var last fakediscord.Message
		for _, msg := range b.fake.Visible(viewer, channelID) {
			isMatchdayAlert := msg.EphemeralFor == "" &&
				strings.Contains(msg.Content, "\u26aa\U0001f535 **Hoy juega la franja**") &&
				strings.Contains(msg.Content, "Audax Italiano")
			if isMatchdayAlert {
				count++
				last = msg
			}
		}
		return count, last
	}

	// Run 2: startup CheckAlerts reads persisted uccalendar.guild_config and delivers per-guild alerts.
	log2 := filepath.Join(tempLogs, "run2.log")
	p2, err := b.start(log2)
	if err != nil {
		t.Fatalf("start run 2: %v", err)
	}

	var alertMsgA, alertMsgB fakediscord.Message
	waitFor(t, "proactive matchday alerts in both configured guild channels", func() bool {
		if p2.hasExited() {
			t.Fatalf("run 2 exited before delivering proactive alerts")
		}
		var countA, countB int
		countA, alertMsgA = countAlertsIn(member, alertChannelID)
		countB, alertMsgB = countAlertsIn(otherMember, otherGuild.ChannelID)
		return countA >= 1 && countB >= 1
	})

	if generalCount, _ := countAlertsIn(member, guild.ChannelID); generalCount != 0 {
		t.Errorf("unselected general channel received %d alerts, want 0", generalCount)
	}

	wantStartToday := kickoffToday.Add(-2 * time.Hour).In(loc).Format("15:04")
	wantEndToday := kickoffToday.Add(3*time.Hour + 30*time.Minute).In(loc).Format("15:04")
	wantStartTomorrow := kickoffTomorrow.Add(-2 * time.Hour).In(loc).Format("15:04")
	wantEndTomorrow := kickoffTomorrow.Add(3*time.Hour + 30*time.Minute).In(loc).Format("15:04")

	hasTodayWindow := strings.Contains(alertMsgA.Content, wantStartToday) && strings.Contains(alertMsgA.Content, wantEndToday)
	hasTomorrowWindow := nearMidnight &&
		strings.Contains(alertMsgA.Content, wantStartTomorrow) &&
		strings.Contains(alertMsgA.Content, wantEndTomorrow)
	if !hasTodayWindow && !hasTomorrowWindow {
		t.Errorf("matchday alert A = %q, want block window %s..%s", alertMsgA.Content, wantStartToday, wantEndToday)
	}

	for _, want := range []string{"Audax Italiano", "<@" + friend.ID + ">", "<t:"} {
		if !strings.Contains(alertMsgA.Content, want) {
			t.Errorf("matchday alert A = %q, want substring %q", alertMsgA.Content, want)
		}
	}

	if strings.Contains(alertMsgB.Content, friend.ID) || !strings.Contains(alertMsgB.Content, "el hincha cruzado") {
		t.Errorf("matchday alert B = %q, want generic fallback without guild A friend %s", alertMsgB.Content, friend.ID)
	}

	for _, record := range b.fake.Outbound() {
		if len(record.Pings) != 0 {
			t.Errorf("record %d (%s) pinged %v, want silent mentions only", record.Seq, record.Source, record.Pings)
		}
	}

	if err := p2.stopGracefully(stopTimeout); err != nil {
		t.Fatalf("stop run 2: %v", err)
	}

	// Store custom uc.banter_prompt and uc.template.report.es_cl in platform.setting via CLI before Run 3.
	customPromptRes, promptErr := b.runCLI(
		t.Context(),
		"Tono chileno de grupo de amigos.\nUsa king, kin, kingcito y mi rey sin voseo.",
		"config", "set", "uc.banter_prompt",
	)
	if promptErr != nil || customPromptRes.code != 0 {
		t.Fatalf("config set uc.banter_prompt: err=%v res=%+v", promptErr, customPromptRes)
	}

	customTmplRes, tmplErr := b.runCLI(
		t.Context(),
		"\u26aa\U0001f535 [DB] {{.Period}} \u00b7 {{.Friend}}\n> {{.Banter}}",
		"config", "set", "uc.template.report.es_cl",
	)
	if tmplErr != nil || customTmplRes.code != 0 {
		t.Fatalf("config set uc.template.report.es_cl: err=%v res=%+v", tmplErr, customTmplRes)
	}

	// Run 3: restart against the same database; (guild_id, alert_key) in uccalendar.sent_alert prevents duplicates
	// and /uc renders the database-configured template.
	log3 := filepath.Join(tempLogs, "run3.log")
	p3, err := b.start(log3)
	if err != nil {
		t.Fatalf("start run 3: %v", err)
	}
	var run3Reply string
	waitFor(t, "run 3 to answer a /uc slash command", func() bool {
		if p3.hasExited() {
			t.Fatalf("run 3 exited before answering /uc")
		}
		slashRes, slashErr := b.fake.Slash(t.Context(), member, fakediscord.Slash{
			Guild:   &guild,
			Path:    "/uc",
			Options: map[string]any{"window": "tomorrow"},
		})
		if slashErr != nil || slashRes.Status == fakediscord.StatusTimeout || slashRes.Message == nil {
			return false
		}
		run3Reply = slashRes.Message.Content
		return true
	})

	if !strings.HasPrefix(run3Reply, "\u26aa\U0001f535 [DB] ") || !strings.Contains(run3Reply, "<@"+friend.ID+">") {
		t.Errorf("run 3 /uc reply = %q, want database-configured uc.template.report.es_cl with friend mention", run3Reply)
	}

	if countA, _ := countAlertsIn(member, alertChannelID); countA != 1 {
		t.Errorf("guild A matchday alert count after restart = %d, want 1 (deduplicated)", countA)
	}
	if countB, _ := countAlertsIn(otherMember, otherGuild.ChannelID); countB != 1 {
		t.Errorf("guild B matchday alert count after restart = %d, want 1 (deduplicated)", countB)
	}

	if err := p3.stopGracefully(stopTimeout); err != nil {
		t.Fatalf("stop run 3: %v", err)
	}

	for _, path := range []string{log1, log2, log3} {
		if auditErr := auditLog(path, &secrets); auditErr != nil {
			t.Error(auditErr)
		}
	}
	if fakeErr := auditText("isolated fake discord log", b.fakeLog.String(), &secrets); fakeErr != nil {
		t.Error(fakeErr)
	}
}
