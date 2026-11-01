package uccmd_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

type memorySettingReader struct {
	mu      sync.RWMutex
	values  map[string]string
	failErr error
}

func newMemorySettingReader() *memorySettingReader {
	r := &memorySettingReader{
		values: make(map[string]string),
	}
	return r
}

func (r *memorySettingReader) Get(_ context.Context, name string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.failErr != nil {
		return "", r.failErr
	}

	val, ok := r.values[name]
	if !ok {
		return "", config.ErrNotSet
	}

	return val, nil
}

func (r *memorySettingReader) Set(name, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.values[name] = value
}

func (r *memorySettingReader) SetFailure(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failErr = err
}

func TestValidateTemplatesAcceptDefaultsAndRejectMalformedOrUnsafeInputsWithoutEchoing(t *testing.T) {
	t.Parallel()

	if err := uccmd.ValidateReportTemplate(uccmd.DefaultReportTemplateESCL); err != nil {
		t.Fatalf("ValidateReportTemplate(DefaultReportTemplateESCL): %v", err)
	}
	if err := uccmd.ValidateReportTemplate(uccmd.DefaultReportTemplateENUS); err != nil {
		t.Fatalf("ValidateReportTemplate(DefaultReportTemplateENUS): %v", err)
	}
	if err := uccmd.ValidateFridayAlertTemplate(uccmd.DefaultFridayAlertTemplateESCL); err != nil {
		t.Fatalf("ValidateFridayAlertTemplate(DefaultFridayAlertTemplateESCL): %v", err)
	}
	if err := uccmd.ValidateFridayAlertTemplate(uccmd.DefaultFridayAlertTemplateENUS); err != nil {
		t.Fatalf("ValidateFridayAlertTemplate(DefaultFridayAlertTemplateENUS): %v", err)
	}
	if err := uccmd.ValidateMatchdayAlertTemplate(uccmd.DefaultMatchdayAlertTemplateESCL); err != nil {
		t.Fatalf("ValidateMatchdayAlertTemplate(DefaultMatchdayAlertTemplateESCL): %v", err)
	}
	if err := uccmd.ValidateMatchdayAlertTemplate(uccmd.DefaultMatchdayAlertTemplateENUS); err != nil {
		t.Fatalf("ValidateMatchdayAlertTemplate(DefaultMatchdayAlertTemplateENUS): %v", err)
	}

	amplification := strings.Repeat("{{.Banter}}", 5000)
	invalidCases := []struct {
		name      string
		raw       string
		forbidTok string
	}{
		{name: "empty", raw: "   \n  "},
		{name: "exceeds 16 KiB", raw: strings.Repeat("a", uccmd.MaxTemplateBytes+1)},
		{name: "invalid UTF-8", raw: "bad\xffutf8"},
		{name: "tab control char", raw: "header\t{{.Period}}"},
		{name: "bidi override", raw: "header \u202e{{.Period}}"},
		{name: "unclosed action", raw: "{{.Period", forbidTok: "Period"},
		{name: "undefined function", raw: "{{secretFunc .Period}}", forbidTok: "secretFunc"},
		{name: "unknown field", raw: "Token: {{.SecretTokenField}}", forbidTok: "SecretTokenField"},
		{name: "renders empty", raw: "{{if false}}never{{end}}"},
		{name: "output exceeds 64 KiB", raw: amplification},
	}

	validators := []struct {
		label string
		fn    func(string) error
	}{
		{label: "report", fn: uccmd.ValidateReportTemplate},
		{label: "friday", fn: uccmd.ValidateFridayAlertTemplate},
		{label: "matchday", fn: uccmd.ValidateMatchdayAlertTemplate},
	}

	for _, v := range validators {
		for _, tc := range invalidCases {
			t.Run(v.label+"/"+tc.name, func(t *testing.T) {
				err := v.fn(tc.raw)
				if err == nil {
					t.Fatalf("%s validator accepted %s", v.label, tc.name)
				}
				if tc.forbidTok != "" && strings.Contains(err.Error(), tc.forbidTok) {
					t.Errorf("%s error %q echoed input token %q", v.label, err, tc.forbidTok)
				}
			})
		}
	}
}

func FuzzValidateTemplates(f *testing.F) {
	seeds := []string{
		"",
		uccmd.DefaultReportTemplateESCL,
		uccmd.DefaultFridayAlertTemplateESCL,
		uccmd.DefaultMatchdayAlertTemplateESCL,
		"{{.UnknownSecretField}}",
		"{{evilFunc}}",
		"{{",
		"\u202e{{.Banter}}",
		"bad\xffutf8",
		"{{if false}}empty{{end}}",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		for _, fn := range []func(string) error{
			uccmd.ValidateReportTemplate,
			uccmd.ValidateFridayAlertTemplate,
			uccmd.ValidateMatchdayAlertTemplate,
		} {
			err := fn(raw)
			if err != nil && len(raw) >= 12 && strings.Contains(err.Error(), raw) {
				t.Fatalf("validator error %q echoed raw input", err)
			}
		}
	})
}

func TestTemplateCacheLiveReloadsPreEscapesInputsAndFallsBackOnInvalidOrFailedReads(t *testing.T) {
	t.Parallel()

	tz := santiagoTZ(t)
	now := time.Date(2026, time.November, 6, 15, 0, 0, 0, tz)
	var clockMu sync.Mutex
	clock := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()

		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}

	reader := newMemorySettingReader()
	cache := uccmd.NewTemplateCache(reader, clock, 5*time.Minute)

	match := sampleConfirmedHomeMatch(tz)
	match.Opponent = "@everyone **Rival**"
	report := uccalendar.Report{
		Mode:         uccalendar.ModeWindow,
		Window:       uccalendar.WindowToday,
		Status:       uccalendar.StatusBlocked,
		Matches:      []uccalendar.Match{match},
		FriendUserID: testFriendID,
		Banter:       "@here\n- **Forged line**",
	}

	// Unset keys use the embedded default template.
	initial := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleESCL, report, tz)
	if !strings.Contains(initial, "\u26aa\U0001f535 \U0001f534 **Bloqueado \u00b7 hoy**") {
		t.Fatalf("initial default render = %q, want default es-CL header", initial)
	}

	// Custom template in reader stays behind TTL until 5 minutes pass, then applies and pre-escapes fields.
	reader.Set(
		uccmd.SettingReportTemplateESCL,
		"CUSTOM-ES {{.Period}} | {{.Friend}}{{range .Matches}} | {{.Opponent}}{{end}}\n> {{.Banter}}",
	)
	reader.Set(
		uccmd.SettingReportTemplateENUS,
		"CUSTOM-EN {{.Period}} | {{.Friend}}\n> {{.Banter}}",
	)
	reader.Set(
		uccmd.SettingFridayAlertTemplateESCL,
		"CUSTOM-FRI {{.SaturdayDate}}-{{.SundayDate}} {{.Friend}} > {{.Banter}}",
	)
	reader.Set(
		uccmd.SettingMatchdayAlertTemplateESCL,
		"CUSTOM-MATCHDAY {{.Friend}}{{range .Matches}} {{.Matchup}}{{end}}",
	)

	stillCached := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleESCL, report, tz)
	if strings.Contains(stillCached, "CUSTOM-ES") {
		t.Fatalf("template reloaded before TTL expired: %q", stillCached)
	}

	advance(6 * time.Minute)
	reloadedES := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleESCL, report, tz)
	for _, want := range []string{
		"CUSTOM-ES hoy | <@" + testFriendID + ">",
		`\@everyone \*\*Rival\*\*`,
		`> \@here - \*\*Forged line\*\*`,
	} {
		if !strings.Contains(reloadedES, want) {
			t.Errorf("reloaded es-CL render missing %q in:\n%s", want, reloadedES)
		}
	}
	if strings.Contains(reloadedES, "\n- **Forged line**") {
		t.Errorf("newline in banter escaped out of blockquote:\n%s", reloadedES)
	}

	reloadedEN := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleENUS, report, tz)
	if !strings.Contains(reloadedEN, "CUSTOM-EN today | <@"+testFriendID+">") {
		t.Errorf("reloaded en-US render = %q, want CUSTOM-EN", reloadedEN)
	}

	friMsg, err := uccmd.RenderAlertWith(t.Context(), cache, uccmd.LocaleESCL, uccalendar.Alert{
		Kind:         uccalendar.AlertFridayWeekend,
		FriendUserID: testFriendID,
		WeekendStart: uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 6},
		WeekendEnd:   uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8},
		Matches:      []uccalendar.Match{match},
	}, tz)
	if err != nil {
		t.Fatalf("RenderAlertWith(friday): %v", err)
	}
	if !strings.Contains(friMsg.Content, "CUSTOM-FRI 07/11-08/11 <@"+testFriendID+">") {
		t.Errorf("custom friday alert = %q, want CUSTOM-FRI", friMsg.Content)
	}

	matchMsg, err := uccmd.RenderAlertWith(t.Context(), cache, uccmd.LocaleESCL, uccalendar.Alert{
		Kind:         uccalendar.AlertMatchdayMorning,
		FriendUserID: testFriendID,
		Matches:      []uccalendar.Match{match},
	}, tz)
	if err != nil {
		t.Fatalf("RenderAlertWith(matchday): %v", err)
	}
	if !strings.Contains(matchMsg.Content, "CUSTOM-MATCHDAY <@"+testFriendID+">") {
		t.Errorf("custom matchday alert = %q, want CUSTOM-MATCHDAY", matchMsg.Content)
	}

	// Transient store failure after TTL expiry keeps serving the last good cached snapshot.
	reader.SetFailure(errors.New("db connection reset"))
	advance(6 * time.Minute)
	duringOutage := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleESCL, report, tz)
	if !strings.Contains(duringOutage, "CUSTOM-ES") {
		t.Errorf("outage render = %q, want previous cached CUSTOM-ES template", duringOutage)
	}

	// Corrupt stored template falls back cleanly to the embedded default template.
	reader.SetFailure(nil)
	reader.Set(uccmd.SettingReportTemplateESCL, "{{.BrokenField}}")
	advance(6 * time.Minute)
	afterCorrupt := uccmd.RenderReportWith(t.Context(), cache, uccmd.LocaleESCL, report, tz)
	if !strings.Contains(afterCorrupt, "\u26aa\U0001f535 \U0001f534 **Bloqueado \u00b7 hoy**") {
		t.Errorf("corrupt template fallback = %q, want default es-CL report header", afterCorrupt)
	}
}

func TestMessageLocaleDefaultsToChileanSpanishAndHonorsEnglish(t *testing.T) {
	t.Parallel()

	for _, esInput := range []string{"", "   ", "es-CL", "EN-us", " en-US ", "fr", "invalid"} {
		if got := uccmd.MessageLocale(esInput); got != uccmd.LocaleESCL {
			t.Errorf("MessageLocale(%q) = %q, want %q", esInput, got, uccmd.LocaleESCL)
		}
	}
	if got := uccmd.MessageLocale("en-US"); got != uccmd.LocaleENUS {
		t.Errorf("MessageLocale(%q) = %q, want %q", "en-US", got, uccmd.LocaleENUS)
	}
}
