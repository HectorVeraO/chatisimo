//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
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
	santiago := mustLoadSantiago(t)

	// Friday 2026-11-06 14:00 in America/Santiago (UTC-3).
	h.clock.Set(time.Date(2026, time.November, 6, 14, 0, 0, 0, santiago))
	h.ucSettings.Set(uccalendar.SettingFriendUserID, userB)

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

	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")
	mustSetUserLocale(t, h, userC, "fr")
	mustSetGuildLocale(t, h, "en-US")

	cases := []struct {
		name        string
		caller      string
		guildLocale string
		path        string
		inGuild     bool
		options     map[string]any
		wantParts   []string
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
			name:        "default weekend in DM Spanish on /chatisimo/uc",
			caller:      userA,
			guildLocale: "en-US",
			path:        "/chatisimo/uc",
			inGuild:     false,
			options:     nil,
			wantParts: []string{
				"\U0001f534 **Bloqueado (este finde (07/11-08/11)): Juega la franja**",
				"Universidad Cat\u00f3lica vs Colo-Colo",
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
			name:        "this_month window in English in DM",
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
	}

	// Verify /uc and /chatisimo/uc produce identical output for the same caller and options.
	shortRes := slashOrFail(t, h, userA, h.slashAt("/uc", true, map[string]any{"window": "this_weekend"}))
	longRes := slashOrFail(t, h, userA, h.slashAt("/chatisimo/uc", true, map[string]any{"window": "this_weekend"}))
	if shortRes.Message == nil || longRes.Message == nil || shortRes.Message.Content != longRes.Message.Content {
		t.Errorf("/uc and /chatisimo/uc replies differ:\nshort: %+v\nlong:  %+v", shortRes.Message, longRes.Message)
	}

	records := h.fake.Outbound()
	if len(records) != len(cases)+2 {
		t.Fatalf("outbound record count = %d, want %d", len(records), len(cases)+2)
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
		hasRenderedFriend := false
		for _, rendered := range rec.MentionsRendered {
			if strings.Contains(rendered, userB) {
				hasRenderedFriend = true
			}
		}
		if !hasRenderedFriend {
			t.Errorf("record[%d] MentionsRendered = %v, want silent mention of userB", i, rec.MentionsRendered)
		}
	}

	_ = h.waitInteractionLogs(t, len(cases)+2)
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
	santiago := mustLoadSantiago(t)

	h.ucSettings.Set(uccalendar.SettingChannelID, h.guild.ChannelID)
	h.ucSettings.Set(uccalendar.SettingFriendUserID, userB)

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

	visible := h.fake.Visible(h.users[userA], h.guild.ChannelID)
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
	if len(records) != 2 {
		t.Fatalf("outbound records = %d, want 2", len(records))
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

	assertNoUCLogLeaks(t, h, "Universidad de Chile", "Claro Arena", "Reporte de disponibilidad")
}
