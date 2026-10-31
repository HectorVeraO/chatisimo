//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func mustLoadSantiago(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(uccalendar.SantiagoTimezone)
	if err != nil {
		t.Fatalf("LoadLocation(%s): %v", uccalendar.SantiagoTimezone, err)
	}

	return loc
}

func mustSetUserLocale(t *testing.T, h *harness, userID, locale string) {
	t.Helper()

	err := h.fake.SetUserLocale(h.users[userID], locale)
	if err != nil {
		t.Fatalf("SetUserLocale(%s, %s): %v", userID, locale, err)
	}
}

func mustSetGuildLocale(t *testing.T, h *harness, locale string) {
	t.Helper()

	err := h.fake.SetGuildLocale(h.guild, locale)
	if err != nil {
		t.Fatalf("SetGuildLocale(%s): %v", locale, err)
	}
}

func mustSetGuildMemberPerms(t *testing.T, h *harness, guild fakediscord.Guild, user fakediscord.User, perms string) {
	t.Helper()

	err := h.fake.SetMemberPermissions(guild, user, perms)
	if err != nil {
		t.Fatalf("SetMemberPermissions(%s, %s, %s): %v", guild.ID, user.ID, perms, err)
	}
}

func resetUCGuildConfigsOnCleanup(t *testing.T, h *harness, guildIDs ...string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		for _, id := range guildIDs {
			scope := uccalendar.GuildScope{GuildID: id, CanManageGuild: true}
			_, err := h.uc.ClearGuildConfig(ctx, scope, uccalendar.ClearTargetAll)
			if err != nil {
				t.Errorf("cleanup ClearGuildConfig(%s): %v", id, err)
			}
		}
	})
}

func assertNoUCLogLeaks(t *testing.T, h *harness, extraSecrets ...string) {
	t.Helper()

	h.secrets = append(h.secrets, extraSecrets...)
	h.checkLogs(t, nil)

	logText := h.logs.String()
	if strings.Contains(logText, h.guild.ChannelID) {
		t.Errorf("logs leaked channel ID %q", h.guild.ChannelID)
	}

	snow := snowflakePattern.FindString(logText)
	if snow != "" {
		t.Errorf("logs contain snowflake-shaped token %q:\n%s", snow, logText)
	}
}

func TestUCBeforeSyncRejectsEphemerallyInCallerLocale(t *testing.T) {
	h := newHarness(t)
	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")

	esRes := slashOrFail(t, h, userA, h.slashAt("/uc", true, nil))
	if esRes.Message == nil || esRes.Message.EphemeralFor != userA {
		t.Fatalf("/uc before sync: reply is not ephemeral to userA: %+v", esRes.Message)
	}
	if !strings.Contains(esRes.Message.Content, "El calendario de la UC no est\u00e1 disponible") ||
		!strings.Contains(esRes.Message.Content, "Int\u00e9ntalo de nuevo en unos minutos.") {
		t.Errorf("es-419 unsynced message = %q, want Spanish schedule unavailable text", esRes.Message.Content)
	}

	enRes := slashOrFail(t, h, userB, h.slashAt("/chatisimo/uc", false, nil))
	if enRes.Message == nil || enRes.Message.EphemeralFor != userB {
		t.Fatalf("/chatisimo/uc before sync: reply is not ephemeral to userB: %+v", enRes.Message)
	}
	if !strings.Contains(enRes.Message.Content, "The UC match schedule is not available right now.") {
		t.Errorf("en-US unsynced message = %q, want English schedule unavailable text", enRes.Message.Content)
	}

	lines := h.waitInteractionLogs(t, 2)
	if lines[0]["route"] != "/uc" || lines[0]["outcome"] != "rejected:schedule_unavailable" {
		t.Errorf("first interaction log = %v, want /uc rejected:schedule_unavailable", lines[0])
	}
	if lines[1]["route"] != "/chatisimo/uc" || lines[1]["outcome"] != "rejected:schedule_unavailable" {
		t.Errorf("second interaction log = %v, want /chatisimo/uc rejected:schedule_unavailable", lines[1])
	}

	assertNoUCLogLeaks(t, h)
}

func TestUCAnswersPubliclyAcrossPathsContextsWindowsDatesAndLocalesWithSilentMention(t *testing.T) {
	h := newHarness(t)
	resetUCGuildConfigsOnCleanup(t, h, h.guild.ID)
	santiago := mustLoadSantiago(t)

	// Friday 2026-11-06 14:00 in America/Santiago (UTC-3).
	h.clock.Set(time.Date(2026, time.November, 6, 14, 0, 0, 0, santiago))

	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")
	mustSetUserLocale(t, h, userC, "fr")
	mustSetGuildLocale(t, h, "en-US")

	mustSetGuildMemberPerms(t, h, h.guild, h.users[userA], "32")
	cfgRes := slashOrFail(t, h, userA, h.slashAt("/uc-config/set", true, map[string]any{"friend": userB}))
	if cfgRes.Message == nil || cfgRes.Message.EphemeralFor != userA {
		t.Fatalf("/uc-config/set: reply is not ephemeral to userA: %+v", cfgRes.Message)
	}

	h.fake.SetUCFixtures([]fakediscord.UCFixture{
		{
			ID:         "401001",
			Date:       "2026-11-07T21:00Z",
			OpponentID: "9001",
			Opponent:   "Colo-Colo",
			Home:       true,
			Venue:      "Claro Arena",
			Tournament: "Liga de Primera",
			TimeValid:  true,
		},
		{
			ID:         "401002",
			Date:       "2026-11-15T16:00Z",
			OpponentID: "9002",
			Opponent:   "Audax Italiano",
			Home:       false,
			Venue:      "Bicentenario La Florida",
			Tournament: "Copa Chile",
			TimeValid:  false,
		},
	})

	syncErr := h.uc.Sync(t.Context())
	if syncErr != nil {
		t.Fatalf("uc.Sync: %v", syncErr)
	}

	cases := []struct {
		name        string
		caller      string
		guildLocale string
		path        string
		inGuild     bool
		options     map[string]any
		wantParts   []string
		forbidParts []string
	}{
		{
			name:        "default weekend in guild Spanish on /uc",
			caller:      userA,
			guildLocale: "en-US",
			path:        "/uc",
			inGuild:     true,
			options:     nil,
			wantParts: []string{
				"\U0001f534 **Bloqueado (este finde (07/11-08/11)): Juega la franja**",
				"Universidad Cat\u00f3lica vs Colo-Colo",
				"Local \U0001f3df\ufe0f Claro Arena",
				"de 16:00 a 21:30 hrs",
				"<@" + userB + ">",
				"*(Ojo - pr\u00f3ximo partido:",
				"Audax Italiano vs Universidad Cat\u00f3lica",
			},
		},
		{
			name:        "default weekend in DM Spanish on /chatisimo/uc uses generic fallback without guild friend",
			caller:      userA,
			guildLocale: "en-US",
			path:        "/chatisimo/uc",
			inGuild:     false,
			options:     nil,
			wantParts: []string{
				"\U0001f534 **Bloqueado (este finde (07/11-08/11)): Juega la franja**",
				"Universidad Cat\u00f3lica vs Colo-Colo",
				"el hincha cruzado",
			},
			forbidParts: []string{
				"<@" + userB + ">",
			},
		},
		{
			name:        "today free window in English in guild",
			caller:      userB,
			guildLocale: "en-US",
			path:        "/uc",
			inGuild:     true,
			options:     map[string]any{"window": "today"},
			wantParts: []string{
				"\U0001f7e2 **All clear (today): Plans can go ahead**",
				"*(Heads up - next match:",
				"Universidad Catolica vs Colo-Colo",
				"Home \U0001f3df\ufe0f Claro Arena",
				"<@" + userB + ">",
			},
		},
		{
			name:        "this_month window in English in DM uses generic fallback without guild friend",
			caller:      userB,
			guildLocale: "en-US",
			path:        "/chatisimo/uc",
			inGuild:     false,
			options:     map[string]any{"window": "this_month"},
			wantParts: []string{
				"\U0001f534 **Blocked (this month): UC plays**",
				"Universidad Catolica vs Colo-Colo",
				"Audax Italiano vs Universidad Catolica",
				"Away \U0001f4fa Bicentenario La Florida",
				"kickoff hour still TBD by ANFP",
				"the UC fan",
			},
			forbidParts: []string{
				"<@" + userB + ">",
			},
		},
		{
			name:        "DD/MM date query for pending ANFP match in Spanish",
			caller:      userA,
			guildLocale: "en-US",
			path:        "/uc",
			inGuild:     true,
			options:     map[string]any{"date": "15/11"},
			wantParts: []string{
				"\U0001f7e1 **Pendiente ANFP (el domingo 15/11): Horario por confirmar**",
				"Audax Italiano vs Universidad Cat\u00f3lica",
				"Visita \U0001f4fa Bicentenario La Florida",
				"horario a\u00fan pendiente de la ANFP",
			},
		},
		{
			name:        "YYYY-MM-DD date query for blocked match in English",
			caller:      userB,
			guildLocale: "en-US",
			path:        "/chatisimo/uc",
			inGuild:     true,
			options:     map[string]any{"date": "2026-11-07"},
			wantParts: []string{
				"\U0001f534 **Blocked (Saturday 07/11): UC plays**",
				"Universidad Catolica vs Colo-Colo",
				"16:00 to 21:30",
			},
		},
		{
			name:        "guild locale fallback to Chilean Spanish when user locale unsupported",
			caller:      userC,
			guildLocale: "es-419",
			path:        "/uc",
			inGuild:     true,
			options:     map[string]any{"window": "today"},
			wantParts: []string{
				"\U0001f7e2 **V\u00eda libre (hoy): Se puede armar junta**",
				"*(Ojo - pr\u00f3ximo partido:",
				"Universidad Cat\u00f3lica vs Colo-Colo",
				"<@" + userB + ">",
			},
		},
	}

	for _, tc := range cases {
		mustSetGuildLocale(t, h, tc.guildLocale)

		res := slashOrFail(t, h, tc.caller, h.slashAt(tc.path, tc.inGuild, tc.options))
		if res.Status != fakediscord.StatusResponded || res.CallbackType != callbackMessage {
			t.Fatalf("%s: status=%q callback=%d, want responded type 4", tc.name, res.Status, res.CallbackType)
		}
		if res.Message == nil {
			t.Fatalf("%s: nil reply message", tc.name)
		}
		if res.Message.EphemeralFor != "" {
			t.Errorf("%s: EphemeralFor = %q, want public reply", tc.name, res.Message.EphemeralFor)
		}
		for _, want := range tc.wantParts {
			if !strings.Contains(res.Message.Content, want) {
				t.Errorf("%s: reply missing %q in:\n%s", tc.name, want, res.Message.Content)
			}
		}
		for _, forbid := range tc.forbidParts {
			if strings.Contains(res.Message.Content, forbid) {
				t.Errorf("%s: reply unexpectedly contains %q in:\n%s", tc.name, forbid, res.Message.Content)
			}
		}
	}

	// Verify /uc and /chatisimo/uc produce identical output for the same caller and options.
	shortRes := slashOrFail(t, h, userA, h.slashAt("/uc", true, map[string]any{"window": "this_weekend"}))
	longRes := slashOrFail(t, h, userA, h.slashAt("/chatisimo/uc", true, map[string]any{"window": "this_weekend"}))
	if shortRes.Message == nil || longRes.Message == nil || shortRes.Message.Content != longRes.Message.Content {
		t.Errorf("/uc and /chatisimo/uc replies differ:\nshort: %+v\nlong:  %+v", shortRes.Message, longRes.Message)
	}

	wantTotal := 1 + len(cases) + 2
	records := h.fake.Outbound()
	if len(records) != wantTotal {
		t.Fatalf("outbound record count = %d, want %d", len(records), wantTotal)
	}
	for i, rec := range records {
		if len(rec.Pings) != 0 {
			t.Errorf("record[%d] Pings = %v, want empty silent mentions", i, rec.Pings)
		}
		sent := rec.AllowedMentionsSent
		hasEmptyLists := sent != nil && sent.Parse != nil && sent.Users != nil && sent.Roles != nil &&
			len(sent.Parse)+len(sent.Users)+len(sent.Roles) == 0
		if !hasEmptyLists {
			t.Errorf("record[%d] AllowedMentionsSent = %+v, want non-nil empty lists", i, sent)
		}
		isDMCase := i == 2 || i == 4
		hasRenderedFriend := false
		for _, rendered := range rec.MentionsRendered {
			if strings.Contains(rendered, userB) {
				hasRenderedFriend = true
			}
		}
		if isDMCase && hasRenderedFriend {
			t.Errorf("DM record[%d] MentionsRendered = %v, want no guild friend mention", i, rec.MentionsRendered)
		}
		if !isDMCase && !hasRenderedFriend {
			t.Errorf("guild record[%d] MentionsRendered = %v, want silent mention of userB", i, rec.MentionsRendered)
		}
	}

	_ = h.waitInteractionLogs(t, wantTotal)
	assertNoUCLogLeaks(t, h, "Colo-Colo", "Audax Italiano", "Claro Arena", "Bicentenario La Florida")
}

func TestUCInvalidOptionsReturnLocalizedEphemeralRejections(t *testing.T) {
	h := newHarness(t)
	santiago := mustLoadSantiago(t)

	h.clock.Set(time.Date(2026, time.November, 6, 14, 0, 0, 0, santiago))
	h.fake.SetUCFixtures(nil)
	if err := h.uc.Sync(t.Context()); err != nil {
		t.Fatalf("uc.Sync: %v", err)
	}

	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")

	oversizedInput := strings.Repeat("9", 4096)
	abuseInputs := []string{
		oversizedInput,
		"3\u03011/10-abuse",
		"\u202e07/11-rtl",
		"07/\u200b11-zwsp",
		"\U0001f600today-emoji",
		"@everyone",
		"<@" + userB + ">",
	}

	cases := []struct {
		name      string
		caller    string
		in        fakediscord.Slash
		wantSub   string
		forbidSub string
	}{
		{
			name:   "mutually exclusive window and date in Spanish",
			caller: userA,
			in: fakediscord.Slash{
				Guild:   &h.guild,
				Path:    "/uc",
				Options: map[string]any{"window": "today", "date": "07/11"},
			},
			wantSub: "Usa ventana o fecha, pero no ambas al mismo tiempo.",
		},
		{
			name:   "mutually exclusive window and date in English",
			caller: userB,
			in: fakediscord.Slash{
				Path:    "/chatisimo/uc",
				Options: map[string]any{"window": "today", "date": "07/11"},
			},
			wantSub: "Pass either window or date, not both.",
		},
		{
			name:   "invalid date in Spanish",
			caller: userA,
			in: fakediscord.Slash{
				Guild:   &h.guild,
				Path:    "/uc",
				Options: map[string]any{"date": "bad-date"},
			},
			wantSub:   "Fecha inv\u00e1lida.",
			forbidSub: "bad-date",
		},
		{
			name:   "invalid date in English",
			caller: userB,
			in: fakediscord.Slash{
				Path:    "/uc",
				Options: map[string]any{"date": "99/99"},
			},
			wantSub:   "Invalid date.",
			forbidSub: "99/99",
		},
		{
			name:   "bypassed unknown window in Spanish",
			caller: userA,
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/chatisimo/uc",
				Options:          map[string]any{"window": "bad-window-value"},
				BypassValidation: true,
			},
			wantSub:   "Ventana desconocida.",
			forbidSub: "bad-window-value",
		},
		{
			name:   "bypassed unknown window in English",
			caller: userB,
			in: fakediscord.Slash{
				Path:             "/uc",
				Options:          map[string]any{"window": "bad-window-value"},
				BypassValidation: true,
			},
			wantSub:   "Unknown time window.",
			forbidSub: "bad-window-value",
		},
	}

	for _, abuse := range abuseInputs {
		cases = append(
			cases,
			struct {
				name      string
				caller    string
				in        fakediscord.Slash
				wantSub   string
				forbidSub string
			}{
				name:   "bypassed date abuse",
				caller: userA,
				in: fakediscord.Slash{
					Guild:            &h.guild,
					Path:             "/uc",
					Options:          map[string]any{"date": abuse},
					BypassValidation: true,
				},
				wantSub:   "Fecha inv\u00e1lida.",
				forbidSub: abuse,
			},
			struct {
				name      string
				caller    string
				in        fakediscord.Slash
				wantSub   string
				forbidSub string
			}{
				name:   "bypassed window abuse",
				caller: userB,
				in: fakediscord.Slash{
					Guild:            &h.guild,
					Path:             "/chatisimo/uc",
					Options:          map[string]any{"window": abuse},
					BypassValidation: true,
				},
				wantSub:   "Unknown time window.",
				forbidSub: abuse,
			},
		)
	}

	for _, tc := range cases {
		res := slashOrFail(t, h, tc.caller, tc.in)
		if res.Message == nil || res.Message.EphemeralFor != tc.caller {
			t.Fatalf("%s: reply is not ephemeral to caller %s: %+v", tc.name, tc.caller, res.Message)
		}
		if !strings.Contains(res.Message.Content, tc.wantSub) {
			t.Errorf("%s: reply = %q, want substring %q", tc.name, res.Message.Content, tc.wantSub)
		}
		if tc.forbidSub != "" && strings.Contains(res.Message.Content, tc.forbidSub) {
			t.Errorf("%s: reply %q echoed rejected input", tc.name, res.Message.Content)
		}
	}

	for i, rec := range h.fake.Outbound() {
		if len(rec.Pings) != 0 {
			t.Errorf("rejection record[%d] Pings = %v, want zero pings", i, rec.Pings)
		}
	}

	lines := h.waitInteractionLogs(t, len(cases))
	for i, line := range lines {
		if line["outcome"] != "rejected:invalid_input" {
			t.Errorf("line[%d] outcome = %v, want rejected:invalid_input", i, line["outcome"])
		}
	}

	leakTokens := append([]string{"bad-date", "bad-window-value", "99/99"}, abuseInputs...)
	assertNoUCLogLeaks(t, h, leakTokens...)
}

func TestUCProactiveAlertsDeliverToGuildChannelWithSilentMentionAndDeduplicate(t *testing.T) {
	h := newHarness(t)
	resetUCGuildConfigsOnCleanup(t, h, h.guild.ID)
	santiago := mustLoadSantiago(t)

	mustSetGuildMemberPerms(t, h, h.guild, h.users[userA], "32")
	cfgRes := slashOrFail(t, h, userA, h.slashAt("/uc-config/set", true, map[string]any{
		"friend":  userB,
		"channel": h.guild.ChannelID,
	}))
	if cfgRes.Message == nil || cfgRes.Message.EphemeralFor != userA {
		t.Fatalf("/uc-config/set: reply is not ephemeral to userA: %+v", cfgRes.Message)
	}

	// Saturday 2026-12-05 18:00 in America/Santiago (21:00 UTC).
	h.fake.SetUCFixtures([]fakediscord.UCFixture{
		{
			ID:         "402001",
			Date:       "2026-12-05T21:00Z",
			OpponentID: "9010",
			Opponent:   "Universidad de Chile",
			Home:       true,
			Venue:      "Claro Arena",
			Tournament: "Liga de Primera",
			TimeValid:  true,
		},
	})

	// Friday 2026-12-04 17:30 in America/Santiago -> past default 17:00 Friday cutoff.
	h.clock.Set(time.Date(2026, time.December, 4, 17, 30, 0, 0, santiago))
	if err := h.uc.Sync(t.Context()); err != nil {
		t.Fatalf("uc.Sync: %v", err)
	}

	if err := h.uc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts (Friday): %v", err)
	}
	// Second check on the same Friday must be a deduplicated no-op.
	if err := h.uc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts (Friday repeat): %v", err)
	}

	// Saturday 2026-12-05 09:30 in America/Santiago -> past default 09:00 matchday cutoff.
	h.clock.Set(time.Date(2026, time.December, 5, 9, 30, 0, 0, santiago))
	if err := h.uc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts (Saturday matchday): %v", err)
	}
	// Second check on the same matchday must be a deduplicated no-op.
	if err := h.uc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts (Saturday repeat): %v", err)
	}

	// Check as userC so userA's ephemeral /uc-config/set reply is excluded, verifying both
	// ephemerality of /uc-config and public channel delivery of proactive alerts.
	visible := h.fake.Visible(h.users[userC], h.guild.ChannelID)
	if len(visible) != 2 {
		t.Fatalf("guild channel visible messages = %d, want 2 (1 Friday + 1 Matchday)", len(visible))
	}

	fridayMsg := visible[0].Content
	for _, want := range []string{
		"\U0001f4e2 **Reporte de disponibilidad de <@" + userB + "> para el finde (05/12-06/12):**",
		"\U0001f534 **s\u00e1bado 05/12 bloqueado de 16:00 a 21:30 hrs**",
		"Universidad Cat\u00f3lica vs Universidad de Chile",
		"\U0001f7e2 **domingo 06/12 libre** para armar panorama.",
	} {
		if !strings.Contains(fridayMsg, want) {
			t.Errorf("Friday alert missing %q in:\n%s", want, fridayMsg)
		}
	}

	matchdayMsg := visible[1].Content
	for _, want := range []string{
		"\u26a0\ufe0f **Aviso de utilidad p\u00fablica:** Hoy a las",
		"Universidad Cat\u00f3lica vs Universidad de Chile",
		"<@" + userB + "> desaparece del mapa entre las **16:00 y las 21:30 hrs**.",
	} {
		if !strings.Contains(matchdayMsg, want) {
			t.Errorf("Matchday alert missing %q in:\n%s", want, matchdayMsg)
		}
	}

	records := h.fake.Outbound()
	if len(records) != 3 {
		t.Fatalf("outbound records = %d, want 3 (1 /uc-config/set + 2 alerts)", len(records))
	}
	for i, rec := range records {
		if len(rec.Pings) != 0 {
			t.Errorf("alert record[%d] Pings = %v, want empty silent mentions", i, rec.Pings)
		}
		sent := rec.AllowedMentionsSent
		hasEmptyLists := sent != nil && sent.Parse != nil && sent.Users != nil && sent.Roles != nil &&
			len(sent.Parse)+len(sent.Users)+len(sent.Roles) == 0
		if !hasEmptyLists {
			t.Errorf("alert record[%d] AllowedMentionsSent = %+v, want non-nil empty lists", i, sent)
		}
	}

	_ = h.waitInteractionLogs(t, 1)
	assertNoUCLogLeaks(t, h, "Universidad de Chile", "Claro Arena", "Reporte de disponibilidad")
}

func TestUCConfigAuthorizationValidationAndMultiGuildIsolation(t *testing.T) {
	h := newHarness(t)
	santiago := mustLoadSantiago(t)

	const (
		userD          = "100000000000000004"
		guildTwoID     = "200000000000000002"
		guildThreeID   = "200000000000000003"
		guildTwoName   = "Second server"
		guildThreeName = "Unconfigured server"
	)

	memberD := h.fake.AddUser(fakediscord.User{ID: userD, Name: "tester-3"})
	guildTwo := h.fake.AddGuild(fakediscord.Guild{ID: guildTwoID, Name: guildTwoName}, memberD)
	guildThree := h.fake.AddGuild(fakediscord.Guild{ID: guildThreeID, Name: guildThreeName}, h.users[userC])

	guildOneAlertCh := h.fake.AddChannel(h.guild, "uc-alerts")
	guildOneVoiceCh := h.fake.AddTypedChannel(h.guild, "General Voice", 2)
	guildTwoAlertCh := h.fake.AddChannel(guildTwo, "cruzados-alerts")

	resetUCGuildConfigsOnCleanup(t, h, h.guild.ID, guildTwo.ID, guildThree.ID)

	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")
	if err := h.fake.SetUserLocale(memberD, "en-US"); err != nil {
		t.Fatalf("SetUserLocale(userD): %v", err)
	}

	// 1. Non-manager (default "0" permissions) is denied on set, show, and clear without probing cross-guild IDs.
	for _, sub := range []struct {
		path    string
		options map[string]any
	}{
		{path: "/uc-config/show"},
		{path: "/uc-config/set", options: map[string]any{"friend": userD, "channel": guildTwoAlertCh}},
		{path: "/uc-config/clear", options: map[string]any{"target": "all"}},
	} {
		res := slashOrFail(t, h, userB, fakediscord.Slash{
			Guild:            &h.guild,
			Path:             sub.path,
			Options:          sub.options,
			BypassValidation: true,
		})
		if res.Message == nil || res.Message.EphemeralFor != userB {
			t.Fatalf("non-manager %s: reply not ephemeral to userB: %+v", sub.path, res.Message)
		}
		if !strings.Contains(res.Message.Content, "Manage Server permission") {
			t.Errorf("non-manager %s: reply = %q, want Manage Server rejection", sub.path, res.Message.Content)
		}
	}

	// 2. DM invocation is blocked by Discord client context validation, and forged DM payloads fail with guild_only.
	if _, dmErr := h.fake.Slash(t.Context(), h.users[userA], fakediscord.Slash{Path: "/uc-config/show"}); dmErr == nil {
		t.Fatal("unbypassed DM /uc-config/show succeeded, want context validation error")
	}
	forgedDM := slashOrFail(t, h, userA, fakediscord.Slash{
		Path:             "/uc-config/show",
		BypassValidation: true,
	})
	if forgedDM.Message == nil || forgedDM.Message.EphemeralFor != userA {
		t.Fatalf("forged DM /uc-config/show: reply not ephemeral to userA: %+v", forgedDM.Message)
	}
	if !strings.Contains(forgedDM.Message.Content, "solo funciona dentro de un servidor") {
		t.Errorf("forged DM /uc-config/show = %q, want Spanish guild-only text", forgedDM.Message.Content)
	}

	// 3. Grant ManageGuild (32) to userA in guild 1 and Administrator (8) to memberD in guild 2.
	mustSetGuildMemberPerms(t, h, h.guild, h.users[userA], "32")
	mustSetGuildMemberPerms(t, h, guildTwo, memberD, "8")

	initialShow := slashOrFail(t, h, userA, h.slashAt("/uc-config/show", true, nil))
	if initialShow.Message == nil || initialShow.Message.EphemeralFor != userA {
		t.Fatalf("initial /uc-config/show: reply not ephemeral to userA: %+v", initialShow.Message)
	}
	if strings.Count(initialShow.Message.Content, "sin configurar") != 2 {
		t.Errorf("initial /uc-config/show = %q, want both fields sin configurar", initialShow.Message.Content)
	}

	// 4. Reject invalid options: empty update, bot user, non-member from guild 2, channel from guild 2, voice channel, bad clear target.
	invalidCalls := []struct {
		name    string
		in      fakediscord.Slash
		wantSub string
	}{
		{
			name: "empty set",
			in: fakediscord.Slash{
				Guild: &h.guild,
				Path:  "/uc-config/set",
			},
			wantSub: "Elige al menos un amigo o un canal de alertas",
		},
		{
			name: "bot user as friend",
			in: fakediscord.Slash{
				Guild:   &h.guild,
				Path:    "/uc-config/set",
				Options: map[string]any{"friend": h.fake.ApplicationID()},
			},
			wantSub: "no a un bot",
		},
		{
			name: "cross-guild user as friend",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/uc-config/set",
				Options:          map[string]any{"friend": userD},
				BypassValidation: true,
			},
			wantSub: "miembro de este servidor",
		},
		{
			name: "cross-guild channel",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/uc-config/set",
				Options:          map[string]any{"channel": guildTwoAlertCh},
				BypassValidation: true,
			},
			wantSub: "canal de texto o anuncios de este servidor",
		},
		{
			name: "voice channel in same guild",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/uc-config/set",
				Options:          map[string]any{"channel": guildOneVoiceCh},
				BypassValidation: true,
			},
			wantSub: "canal de texto o anuncios de este servidor",
		},
		{
			name: "invalid clear target",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/uc-config/clear",
				Options:          map[string]any{"target": "everything"},
				BypassValidation: true,
			},
			wantSub: "Objetivo desconocido",
		},
	}

	for _, tc := range invalidCalls {
		res := slashOrFail(t, h, userA, tc.in)
		if res.Message == nil || res.Message.EphemeralFor != userA {
			t.Fatalf("%s: reply not ephemeral to userA: %+v", tc.name, res.Message)
		}
		if !strings.Contains(res.Message.Content, tc.wantSub) {
			t.Errorf("%s: reply = %q, want substring %q", tc.name, res.Message.Content, tc.wantSub)
		}
	}

	// 5. Configure guild 1 (friend + dedicated alert channel) and guild 2 (dedicated alert channel only, via Administrator).
	setG1 := slashOrFail(t, h, userA, h.slashAt("/uc-config/set", true, map[string]any{
		"friend":  userB,
		"channel": guildOneAlertCh,
	}))
	if setG1.Message == nil || setG1.Message.EphemeralFor != userA {
		t.Fatalf("guild 1 set: reply not ephemeral to userA: %+v", setG1.Message)
	}
	for _, want := range []string{"<@" + userB + ">", "<#" + guildOneAlertCh + ">"} {
		if !strings.Contains(setG1.Message.Content, want) {
			t.Errorf("guild 1 set missing %q in:\n%s", want, setG1.Message.Content)
		}
	}

	setG2, err := h.fake.Slash(t.Context(), memberD, fakediscord.Slash{
		Guild:   &guildTwo,
		Path:    "/uc-config/set",
		Options: map[string]any{"channel": guildTwoAlertCh},
	})
	if err != nil {
		t.Fatalf("guild 2 Slash /uc-config/set: %v", err)
	}
	if setG2.Message == nil || setG2.Message.EphemeralFor != userD {
		t.Fatalf("guild 2 set: reply not ephemeral to userD: %+v", setG2.Message)
	}
	for _, want := range []string{"not configured", "<#" + guildTwoAlertCh + ">"} {
		if !strings.Contains(setG2.Message.Content, want) {
			t.Errorf("guild 2 set missing %q in:\n%s", want, setG2.Message.Content)
		}
	}

	showG2, err := h.fake.Slash(t.Context(), memberD, fakediscord.Slash{
		Guild: &guildTwo,
		Path:  "/uc-config/show",
	})
	if err != nil {
		t.Fatalf("guild 2 Slash /uc-config/show: %v", err)
	}
	if showG2.Message == nil || strings.Contains(showG2.Message.Content, userB) || strings.Contains(showG2.Message.Content, guildOneAlertCh) {
		t.Fatalf("guild 2 show leaked guild 1 config: %+v", showG2.Message)
	}

	// 6. Sync a Saturday fixture (2026-10-17 18:00 Santiago = 21:00 UTC) and verify /uc isolation across guilds and DM.
	h.fake.SetUCFixtures([]fakediscord.UCFixture{
		{
			ID:         "403001",
			Date:       "2026-10-17T21:00Z",
			OpponentID: "9020",
			Opponent:   "Everton",
			Home:       true,
			Venue:      "Claro Arena",
			Tournament: "Liga de Primera",
			TimeValid:  true,
		},
	})
	h.clock.Set(time.Date(2026, time.October, 16, 17, 30, 0, 0, santiago))
	if syncErr := h.uc.Sync(t.Context()); syncErr != nil {
		t.Fatalf("uc.Sync: %v", syncErr)
	}

	ucG1 := slashOrFail(t, h, userA, h.slashAt("/uc", true, nil))
	if ucG1.Message == nil || !strings.Contains(ucG1.Message.Content, "<@"+userB+">") {
		t.Fatalf("guild 1 /uc missing configured <@%s>: %+v", userB, ucG1.Message)
	}

	ucG2, err := h.fake.Slash(t.Context(), memberD, fakediscord.Slash{Guild: &guildTwo, Path: "/uc"})
	if err != nil {
		t.Fatalf("guild 2 /uc: %v", err)
	}
	if ucG2.Message == nil || strings.Contains(ucG2.Message.Content, userB) || !strings.Contains(ucG2.Message.Content, "the UC fan") {
		t.Fatalf("guild 2 /uc = %+v, want generic fallback without guild 1 friend", ucG2.Message)
	}

	ucG3 := slashOrFail(t, h, userC, fakediscord.Slash{Guild: &guildThree, Path: "/uc"})
	if ucG3.Message == nil || strings.Contains(ucG3.Message.Content, userB) {
		t.Fatalf("guild 3 /uc leaked guild 1 friend: %+v", ucG3.Message)
	}

	ucDM := slashOrFail(t, h, userA, h.slashAt("/uc", false, nil))
	if ucDM.Message == nil || strings.Contains(ucDM.Message.Content, userB) || !strings.Contains(ucDM.Message.Content, "el hincha cruzado") {
		t.Fatalf("DM /uc = %+v, want generic fallback without guild 1 friend", ucDM.Message)
	}

	// 7. Proactive Friday alerts deliver independently to guildOneAlertCh and guildTwoAlertCh, skipping unconfigured guildThree.
	if alertErr := h.uc.CheckAlerts(t.Context()); alertErr != nil {
		t.Fatalf("CheckAlerts (Friday): %v", alertErr)
	}

	g1Alerts := h.fake.Visible(h.users[userC], guildOneAlertCh)
	if len(g1Alerts) != 1 || !strings.Contains(g1Alerts[0].Content, "<@"+userB+">") {
		t.Fatalf("guild 1 alert channel messages = %+v, want 1 Friday alert mentioning userB", g1Alerts)
	}
	g2Alerts := h.fake.Visible(memberD, guildTwoAlertCh)
	if len(g2Alerts) != 1 || strings.Contains(g2Alerts[0].Content, userB) || !strings.Contains(g2Alerts[0].Content, "el hincha cruzado") {
		t.Fatalf("guild 2 alert channel messages = %+v, want 1 Friday alert with generic fallback", g2Alerts)
	}
	g3Messages := h.fake.Visible(h.users[userC], guildThree.ChannelID)
	if len(g3Messages) != 1 || g3Messages[0].ID != ucG3.Message.ID {
		t.Errorf("unconfigured guild 3 channel messages = %+v, want only the earlier /uc reply", g3Messages)
	}

	// 8. Partial clear (friend only) keeps guild 1's alert channel active for Saturday matchday alerts, then full clear resets all.
	clearFriend := slashOrFail(t, h, userA, h.slashAt("/uc-config/clear", true, map[string]any{"target": "friend"}))
	if clearFriend.Message == nil || !strings.Contains(clearFriend.Message.Content, "sin configurar") ||
		!strings.Contains(clearFriend.Message.Content, "<#"+guildOneAlertCh+">") {
		t.Fatalf("partial clear reply = %+v, want friend cleared and channel preserved", clearFriend.Message)
	}

	h.clock.Set(time.Date(2026, time.October, 17, 9, 30, 0, 0, santiago))
	if alertErr := h.uc.CheckAlerts(t.Context()); alertErr != nil {
		t.Fatalf("CheckAlerts (Saturday matchday): %v", alertErr)
	}

	g1AlertsAfter := h.fake.Visible(h.users[userC], guildOneAlertCh)
	if len(g1AlertsAfter) != 2 || strings.Contains(g1AlertsAfter[1].Content, userB) ||
		!strings.Contains(g1AlertsAfter[1].Content, "el hincha cruzado") {
		t.Fatalf("guild 1 matchday alert after clearing friend = %+v, want generic fallback", g1AlertsAfter)
	}

	clearAll := slashOrFail(t, h, userA, h.slashAt("/uc-config/clear", true, nil))
	if clearAll.Message == nil || strings.Count(clearAll.Message.Content, "sin configurar") != 2 {
		t.Fatalf("full clear reply = %+v, want both fields sin configurar", clearAll.Message)
	}

	for i, rec := range h.fake.Outbound() {
		if len(rec.Pings) != 0 {
			t.Errorf("record[%d] Pings = %v, want empty silent mentions", i, rec.Pings)
		}
	}

	assertNoUCLogLeaks(
		t,
		h,
		userD,
		guildTwoID,
		guildThreeID,
		guildTwoName,
		guildThreeName,
		guildOneAlertCh,
		guildOneVoiceCh,
		guildTwoAlertCh,
		guildTwo.ChannelID,
		guildThree.ChannelID,
		"Everton",
		"everything",
	)
}
