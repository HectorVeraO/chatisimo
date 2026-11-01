package uccmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

// Runtime setting keys in platform.setting for Universidad Catolica Discord message templates.
const (
	SettingReportTemplateESCL        = "uc.template.report.es_cl"
	SettingReportTemplateENUS        = "uc.template.report.en_us"
	SettingFridayAlertTemplateESCL   = "uc.template.friday_alert.es_cl"
	SettingFridayAlertTemplateENUS   = "uc.template.friday_alert.en_us"
	SettingMatchdayAlertTemplateESCL = "uc.template.matchday_alert.es_cl"
	SettingMatchdayAlertTemplateENUS = "uc.template.matchday_alert.en_us"
)

const (
	// MaxTemplateBytes caps custom UC message templates stored in platform.setting at 16 KiB.
	MaxTemplateBytes = 16 << 10

	maxTemplateOutputBytes  = 64 << 10
	defaultTemplateCacheTTL = 5 * time.Minute
)

// DefaultReportTemplateESCL is the default Chilean Spanish text/template for /uc and /chatisimo uc
// schedule reports.
const DefaultReportTemplateESCL = "" +
	"{{- if .Blocked}}\u26aa\U0001f535 \U0001f534 **Bloqueado \u00b7 {{.Period}}**\n" +
	"{{- else if .PendingANFP}}\u26aa\U0001f535 \U0001f7e1 **Pendiente ANFP \u00b7 {{.Period}}**\n" +
	"{{- else}}\u26aa\U0001f535 \U0001f7e2 **V\u00eda libre \u00b7 {{.Period}}**{{- end}}\n" +
	"{{- range .Matches}}\n" +
	"\u26bd **{{.Matchup}}**\n" +
	"- {{.Venue}}{{if .Tournament}} \u00b7 {{.Tournament}}{{end}}\n" +
	"{{- if .TBD}}\n" +
	"- **Fecha:** {{.Weekday}} {{.Date}} \u00b7 horario pendiente ANFP para {{$.Friend}}\n" +
	"{{- else}}\n" +
	"- **Inicio:** {{.Weekday}} {{.Date}} a las {{.KickoffTime}} ({{.KickoffRelative}})\n" +
	"- **Bloqueo:** {{.BlockStart}} a {{.BlockEnd}} hrs para {{$.Friend}}\n" +
	"{{- end}}\n" +
	"{{- end}}\n" +
	"{{- if .Overflowed}}\n" +
	"...\n" +
	"{{- else if not .Matches}}\n" +
	"- **Disponibilidad:** {{.Friend}} sin partido de la UC\n" +
	"{{- end}}\n" +
	"> {{.Banter}}\n" +
	"{{- with .NextMatch}}\n" +
	"-# Pr\u00f3ximo partido: {{.Matchup}} ({{.Venue}}) \u00b7 {{.Weekday}} {{.Date}}" +
	"{{if .TBD}} \u00b7 horario pendiente ANFP" +
	"{{else}} a las {{.KickoffTime}} ({{.KickoffRelative}}) \u00b7 bloqueo {{.BlockStart}} a {{.BlockEnd}} hrs{{end}}\n" +
	"{{- end}}"

// DefaultReportTemplateENUS is the default English (United States) text/template for /uc and
// /chatisimo uc schedule reports.
const DefaultReportTemplateENUS = "" +
	"{{- if .Blocked}}\u26aa\U0001f535 \U0001f534 **Blocked \u00b7 {{.Period}}**\n" +
	"{{- else if .PendingANFP}}\u26aa\U0001f535 \U0001f7e1 **Pending ANFP \u00b7 {{.Period}}**\n" +
	"{{- else}}\u26aa\U0001f535 \U0001f7e2 **All clear \u00b7 {{.Period}}**{{- end}}\n" +
	"{{- range .Matches}}\n" +
	"\u26bd **{{.Matchup}}**\n" +
	"- {{.Venue}}{{if .Tournament}} \u00b7 {{.Tournament}}{{end}}\n" +
	"{{- if .TBD}}\n" +
	"- **Date:** {{.Weekday}} {{.Date}} \u00b7 kickoff hour pending ANFP for {{$.Friend}}\n" +
	"{{- else}}\n" +
	"- **Kickoff:** {{.Weekday}} {{.Date}} at {{.KickoffTime}} ({{.KickoffRelative}})\n" +
	"- **Unavailable:** {{.BlockStart}} to {{.BlockEnd}} for {{$.Friend}}\n" +
	"{{- end}}\n" +
	"{{- end}}\n" +
	"{{- if .Overflowed}}\n" +
	"...\n" +
	"{{- else if not .Matches}}\n" +
	"- **Availability:** {{.Friend}} has no UC match\n" +
	"{{- end}}\n" +
	"> {{.Banter}}\n" +
	"{{- with .NextMatch}}\n" +
	"-# Next match: {{.Matchup}} ({{.Venue}}) \u00b7 {{.Weekday}} {{.Date}}" +
	"{{if .TBD}} \u00b7 kickoff hour pending ANFP" +
	"{{else}} at {{.KickoffTime}} ({{.KickoffRelative}}) \u00b7 unavailable {{.BlockStart}} to {{.BlockEnd}}{{end}}\n" +
	"{{- end}}"

// DefaultFridayAlertTemplateESCL is the default Chilean Spanish text/template for proactive Friday
// weekend UC summaries.
const DefaultFridayAlertTemplateESCL = "" +
	"\u26aa\U0001f535 **Reporte cruzado de finde \u00b7 {{.SaturdayDate}}-{{.SundayDate}}**\n" +
	"- **Hincha:** {{.Friend}}\n" +
	"{{- range .Matches}}\n" +
	"{{- if .TBD}}\n" +
	"- \U0001f7e1 **{{.Weekday}} {{.Date}}** (pendiente ANFP) \u00b7 {{.Matchup}} ({{.Venue}})\n" +
	"{{- else}}\n" +
	"- \U0001f534 **{{.Weekday}} {{.Date}}** ({{.BlockStart}} a {{.BlockEnd}} hrs) \u00b7 {{.Matchup}} \u00b7 {{.KickoffTime}} ({{.Venue}})\n" +
	"{{- end}}\n" +
	"{{- end}}\n" +
	"{{- if .SaturdayFree}}\n" +
	"- \U0001f7e2 **{{.SaturdayWeekday}} {{.SaturdayDate}}** libre para armar panorama\n" +
	"{{- end}}\n" +
	"{{- if .SundayFree}}\n" +
	"- \U0001f7e2 **{{.SundayWeekday}} {{.SundayDate}}** libre para armar panorama\n" +
	"{{- end}}\n" +
	"> {{.Banter}}"

// DefaultFridayAlertTemplateENUS is the default English (United States) text/template for
// proactive Friday weekend UC summaries.
const DefaultFridayAlertTemplateENUS = "" +
	"\u26aa\U0001f535 **UC weekend report \u00b7 {{.SaturdayDate}}-{{.SundayDate}}**\n" +
	"- **Fan:** {{.Friend}}\n" +
	"{{- range .Matches}}\n" +
	"{{- if .TBD}}\n" +
	"- \U0001f7e1 **{{.Weekday}} {{.Date}}** (pending ANFP) \u00b7 {{.Matchup}} ({{.Venue}})\n" +
	"{{- else}}\n" +
	"- \U0001f534 **{{.Weekday}} {{.Date}}** ({{.BlockStart}} to {{.BlockEnd}}) \u00b7 {{.Matchup}} \u00b7 {{.KickoffTime}} ({{.Venue}})\n" +
	"{{- end}}\n" +
	"{{- end}}\n" +
	"{{- if .SaturdayFree}}\n" +
	"- \U0001f7e2 **{{.SaturdayWeekday}} {{.SaturdayDate}}** free for plans\n" +
	"{{- end}}\n" +
	"{{- if .SundayFree}}\n" +
	"- \U0001f7e2 **{{.SundayWeekday}} {{.SundayDate}}** free for plans\n" +
	"{{- end}}\n" +
	"> {{.Banter}}"

// DefaultMatchdayAlertTemplateESCL is the default Chilean Spanish text/template for proactive
// matchday morning UC reminders.
const DefaultMatchdayAlertTemplateESCL = "" +
	"\u26aa\U0001f535 **Hoy juega la franja**\n" +
	"{{- range .Matches}}\n" +
	"\u26bd **{{.Matchup}}**\n" +
	"- {{.Venue}}{{if .Tournament}} \u00b7 {{.Tournament}}{{end}}\n" +
	"- **Inicio:** hoy a las {{.KickoffTime}} ({{.KickoffRelative}})\n" +
	"- **Bloqueo:** {{$.Friend}} no disponible de **{{.BlockStart}} a {{.BlockEnd}} hrs**\n" +
	"{{- end}}\n" +
	"{{- if .Banter}}\n" +
	"> {{.Banter}}\n" +
	"{{- end}}"

// DefaultMatchdayAlertTemplateENUS is the default English (United States) text/template for
// proactive matchday morning UC reminders.
const DefaultMatchdayAlertTemplateENUS = "" +
	"\u26aa\U0001f535 **UC matchday alert**\n" +
	"{{- range .Matches}}\n" +
	"\u26bd **{{.Matchup}}**\n" +
	"- {{.Venue}}{{if .Tournament}} \u00b7 {{.Tournament}}{{end}}\n" +
	"- **Kickoff:** today at {{.KickoffTime}} ({{.KickoffRelative}})\n" +
	"- **Unavailable:** {{$.Friend}} blocked from **{{.BlockStart}} to {{.BlockEnd}}**\n" +
	"{{- end}}\n" +
	"{{- if .Banter}}\n" +
	"> {{.Banter}}\n" +
	"{{- end}}"

var (
	errTemplateEmpty          = errors.New("uccmd: template is empty")
	errTemplateTooLong        = errors.New("uccmd: template exceeds maximum size")
	errTemplateInvalidUTF8    = errors.New("uccmd: template is not valid UTF-8")
	errTemplateControlChars   = errors.New("uccmd: template contains disallowed control characters")
	errTemplateSyntax         = errors.New("uccmd: invalid template syntax")
	errTemplateEval           = errors.New("uccmd: template references unknown field or invalid action")
	errTemplateEmptyRender    = errors.New("uccmd: template renders empty message")
	errTemplateOutputTooLarge = errors.New("uccmd: template output exceeds maximum size")
)

// SettingReader reads non-secret runtime settings by key, returning config.ErrNotSet when a key has
// no stored row.
type SettingReader interface {
	Get(ctx context.Context, name string) (string, error)
}

// MatchView holds pre-escaped display fields for one Universidad Catolica fixture inside a UC
// message template.
type MatchView struct {
	Matchup         string
	Opponent        string
	Tournament      string
	Venue           string
	Home            bool
	TBD             bool
	Weekday         string
	Date            string
	KickoffTime     string
	KickoffRelative string
	BlockStart      string
	BlockEnd        string
}

// ReportView holds pre-escaped display fields passed to uc.template.report.* templates.
type ReportView struct {
	Status         string
	Blocked        bool
	PendingANFP    bool
	Free           bool
	Period         string
	Friend         string
	HasFriend      bool
	Matches        []MatchView
	Overflowed     bool
	OmittedMatches int
	Banter         string
	NextMatch      *MatchView
}

// FridayAlertView holds pre-escaped display fields passed to uc.template.friday_alert.* templates.
type FridayAlertView struct {
	Friend          string
	HasFriend       bool
	SaturdayWeekday string
	SaturdayDate    string
	SaturdayStatus  string
	SaturdayFree    bool
	SundayWeekday   string
	SundayDate      string
	SundayStatus    string
	SundayFree      bool
	Matches         []MatchView
	Banter          string
}

// MatchdayAlertView holds pre-escaped display fields passed to uc.template.matchday_alert.*
// templates.
type MatchdayAlertView struct {
	Friend    string
	HasFriend bool
	Matches   []MatchView
	Banter    string
}

type compiledTemplateSet struct {
	reportESCL   *template.Template
	reportENUS   *template.Template
	fridayESCL   *template.Template
	fridayENUS   *template.Template
	matchdayESCL *template.Template
	matchdayENUS *template.Template
}

func (s compiledTemplateSet) reportFor(loc Locale) *template.Template {
	if loc == LocaleENUS {
		return s.reportENUS
	}

	return s.reportESCL
}

func (s compiledTemplateSet) fridayFor(loc Locale) *template.Template {
	if loc == LocaleENUS {
		return s.fridayENUS
	}

	return s.fridayESCL
}

func (s compiledTemplateSet) matchdayFor(loc Locale) *template.Template {
	if loc == LocaleENUS {
		return s.matchdayENUS
	}

	return s.matchdayESCL
}

var defaultTemplates = sync.OnceValue(func() compiledTemplateSet {
	reportES, err := compileReportTemplate(DefaultReportTemplateESCL)
	if err != nil {
		panic("uccmd: invalid default es-CL report template: " + err.Error())
	}
	reportEN, err := compileReportTemplate(DefaultReportTemplateENUS)
	if err != nil {
		panic("uccmd: invalid default en-US report template: " + err.Error())
	}
	fridayES, err := compileFridayAlertTemplate(DefaultFridayAlertTemplateESCL)
	if err != nil {
		panic("uccmd: invalid default es-CL friday alert template: " + err.Error())
	}
	fridayEN, err := compileFridayAlertTemplate(DefaultFridayAlertTemplateENUS)
	if err != nil {
		panic("uccmd: invalid default en-US friday alert template: " + err.Error())
	}
	matchdayES, err := compileMatchdayAlertTemplate(DefaultMatchdayAlertTemplateESCL)
	if err != nil {
		panic("uccmd: invalid default es-CL matchday alert template: " + err.Error())
	}
	matchdayEN, err := compileMatchdayAlertTemplate(DefaultMatchdayAlertTemplateENUS)
	if err != nil {
		panic("uccmd: invalid default en-US matchday alert template: " + err.Error())
	}

	set := compiledTemplateSet{
		reportESCL:   reportES,
		reportENUS:   reportEN,
		fridayESCL:   fridayES,
		fridayENUS:   fridayEN,
		matchdayESCL: matchdayES,
		matchdayENUS: matchdayEN,
	}
	return set
})

// ValidateReportTemplate validates a uc.template.report.* text/template definition against sample
// ReportView states without echoing raw template input in returned errors.
func ValidateReportTemplate(raw string) error {
	_, err := compileReportTemplate(raw)
	return err
}

// ValidateFridayAlertTemplate validates a uc.template.friday_alert.* text/template definition
// against sample FridayAlertView states without echoing raw template input in returned errors.
func ValidateFridayAlertTemplate(raw string) error {
	_, err := compileFridayAlertTemplate(raw)
	return err
}

// ValidateMatchdayAlertTemplate validates a uc.template.matchday_alert.* text/template definition
// against sample MatchdayAlertView states without echoing raw template input in returned errors.
func ValidateMatchdayAlertTemplate(raw string) error {
	_, err := compileMatchdayAlertTemplate(raw)
	return err
}

func compileReportTemplate(raw string) (*template.Template, error) {
	tmpl, err := parseSafeTemplate(raw)
	if err != nil {
		return nil, err
	}

	samples := sampleReportViews()
	for _, sample := range samples {
		out, execErr := executeTemplate(tmpl, sample)
		if execErr != nil {
			return nil, execErr
		}
		if strings.TrimSpace(out) == "" {
			return nil, errTemplateEmptyRender
		}
	}

	return tmpl, nil
}

func compileFridayAlertTemplate(raw string) (*template.Template, error) {
	tmpl, err := parseSafeTemplate(raw)
	if err != nil {
		return nil, err
	}

	samples := sampleFridayAlertViews()
	for _, sample := range samples {
		out, execErr := executeTemplate(tmpl, sample)
		if execErr != nil {
			return nil, execErr
		}
		if strings.TrimSpace(out) == "" {
			return nil, errTemplateEmptyRender
		}
	}

	return tmpl, nil
}

func compileMatchdayAlertTemplate(raw string) (*template.Template, error) {
	tmpl, err := parseSafeTemplate(raw)
	if err != nil {
		return nil, err
	}

	samples := sampleMatchdayAlertViews()
	for _, sample := range samples {
		out, execErr := executeTemplate(tmpl, sample)
		if execErr != nil {
			return nil, execErr
		}
		if strings.TrimSpace(out) == "" {
			return nil, errTemplateEmptyRender
		}
	}

	return tmpl, nil
}

func parseSafeTemplate(raw string) (*template.Template, error) {
	if !utf8.ValidString(raw) {
		return nil, errTemplateInvalidUTF8
	}

	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return nil, errTemplateEmpty
	}
	if len(trimmed) > MaxTemplateBytes {
		return nil, errTemplateTooLong
	}

	for _, r := range trimmed {
		isDisallowedControl := unicode.IsControl(r) && r != '\n'
		if isDisallowedControl || isBidiRune(r) {
			return nil, errTemplateControlChars
		}
	}

	tmpl, parseErr := template.New("uc").Option("missingkey=error").Parse(trimmed)
	if parseErr != nil {
		return nil, errTemplateSyntax
	}

	return tmpl, nil
}

func isBidiRune(r rune) bool {
	if r == '\u200e' || r == '\u200f' {
		return true
	}
	if r >= '\u202a' && r <= '\u202e' {
		return true
	}
	isIsolate := r >= '\u2066' && r <= '\u2069'
	return isIsolate
}

type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf)+len(p) > b.max {
		return 0, errTemplateOutputTooLarge
	}

	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}

func executeTemplate(tmpl *template.Template, data any) (string, error) {
	buf := &limitedBuffer{max: maxTemplateOutputBytes}
	err := tmpl.Execute(buf, data)
	if err != nil {
		if errors.Is(err, errTemplateOutputTooLarge) {
			return "", errTemplateOutputTooLarge
		}

		return "", errTemplateEval
	}

	rendered := strings.TrimSpace(buf.String())
	return rendered, nil
}

func sampleMatchViews() (MatchView, MatchView) {
	confirmed := MatchView{
		Matchup:         "Universidad Catolica vs Rival FC",
		Opponent:        "Rival FC",
		Tournament:      "Liga de Primera",
		Venue:           "Local Claro Arena",
		Home:            true,
		TBD:             false,
		Weekday:         "sabado",
		Date:            "07/11",
		KickoffTime:     "<t:1794085200:t>",
		KickoffRelative: "<t:1794085200:R>",
		BlockStart:      "16:00",
		BlockEnd:        "21:30",
	}
	tbd := MatchView{
		Matchup:    "Rival Unido vs Universidad Catolica",
		Opponent:   "Rival Unido",
		Tournament: "Copa Chile",
		Venue:      "Visita",
		Home:       false,
		TBD:        true,
		Weekday:    "domingo",
		Date:       "15/11",
	}
	return confirmed, tbd
}

func sampleReportViews() []ReportView {
	confirmed, tbd := sampleMatchViews()
	views := []ReportView{
		{
			Status:    "blocked",
			Blocked:   true,
			Period:    "este finde (07/11-08/11)",
			Friend:    "<@100000000000000001>",
			HasFriend: true,
			Matches:   []MatchView{confirmed, tbd},
			Banter:    "Juega la franja.",
			NextMatch: &confirmed,
		},
		{
			Status:      "pending_anfp",
			PendingANFP: true,
			Period:      "domingo 15/11",
			Friend:      "el hincha cruzado",
			HasFriend:   false,
			Matches:     []MatchView{tbd},
			Banter:      "Horario pendiente.",
			NextMatch:   &tbd,
		},
		{
			Status: "free",
			Free:   true,
			Period: "hoy",
			Friend: "el hincha cruzado",
			Banter: "Via libre.",
		},
		{
			Status:         "blocked",
			Blocked:        true,
			Period:         "este mes",
			Friend:         "<@100000000000000001>",
			HasFriend:      true,
			Matches:        []MatchView{confirmed},
			Overflowed:     true,
			OmittedMatches: 3,
			Banter:         "Mes cargado.",
		},
	}
	return views
}

func sampleFridayAlertViews() []FridayAlertView {
	confirmed, tbd := sampleMatchViews()
	views := []FridayAlertView{
		{
			Friend:          "<@100000000000000001>",
			HasFriend:       true,
			SaturdayWeekday: "sabado",
			SaturdayDate:    "07/11",
			SaturdayStatus:  "blocked",
			SundayWeekday:   "domingo",
			SundayDate:      "08/11",
			SundayStatus:    "pending_anfp",
			Matches:         []MatchView{confirmed, tbd},
			Banter:          "Ojo con el finde.",
		},
		{
			Friend:          "el hincha cruzado",
			SaturdayWeekday: "sabado",
			SaturdayDate:    "07/11",
			SaturdayStatus:  "free",
			SaturdayFree:    true,
			SundayWeekday:   "domingo",
			SundayDate:      "08/11",
			SundayStatus:    "free",
			SundayFree:      true,
			Banter:          "Finde despejado.",
		},
	}
	return views
}

func sampleMatchdayAlertViews() []MatchdayAlertView {
	confirmed, _ := sampleMatchViews()
	views := []MatchdayAlertView{
		{
			Friend:    "<@100000000000000001>",
			HasFriend: true,
			Matches:   []MatchView{confirmed},
			Banter:    "Hoy juega la Cato.",
		},
		{
			Friend:  "el hincha cruzado",
			Matches: []MatchView{confirmed},
		},
	}
	return views
}

// TemplateCache caches compiled UC Discord message templates loaded from a SettingReader with a
// bounded TTL, falling back to the embedded default templates when unset or nil.
type TemplateCache struct {
	reader SettingReader
	clock  func() time.Time
	ttl    time.Duration

	mu        sync.RWMutex
	cached    compiledTemplateSet
	expiresAt time.Time
	hasCached bool
}

// NewTemplateCache returns a concurrency-safe cache of UC Discord message templates. When reader is
// nil, it always serves the embedded default templates without I/O. When ttl <= 0, it defaults to
// 5 minutes.
func NewTemplateCache(reader SettingReader, clock func() time.Time, ttl time.Duration) *TemplateCache {
	if clock == nil {
		clock = time.Now
	}
	if ttl <= 0 {
		ttl = defaultTemplateCacheTTL
	}

	cache := &TemplateCache{
		reader: reader,
		clock:  clock,
		ttl:    ttl,
	}
	return cache
}

func (c *TemplateCache) resolve(ctx context.Context) compiledTemplateSet {
	defaults := defaultTemplates()
	if c == nil || c.reader == nil || ctx == nil {
		return defaults
	}

	now := c.clock()
	c.mu.RLock()
	if c.hasCached && now.Before(c.expiresAt) {
		cached := c.cached
		c.mu.RUnlock()
		return cached
	}
	previous := c.cached
	hadPrevious := c.hasCached
	c.mu.RUnlock()

	loaded, ok := c.loadFromReader(ctx, defaults)
	if !ok {
		if hadPrevious {
			return previous
		}

		return defaults
	}

	c.mu.Lock()
	c.cached = loaded
	c.expiresAt = now.Add(c.ttl)
	c.hasCached = true
	c.mu.Unlock()

	return loaded
}

func (c *TemplateCache) loadFromReader(ctx context.Context, defaults compiledTemplateSet) (compiledTemplateSet, bool) {
	reportES, ok := loadOneTemplate(ctx, c.reader, SettingReportTemplateESCL, defaults.reportESCL, compileReportTemplate)
	if !ok {
		return compiledTemplateSet{}, false
	}
	reportEN, ok := loadOneTemplate(ctx, c.reader, SettingReportTemplateENUS, defaults.reportENUS, compileReportTemplate)
	if !ok {
		return compiledTemplateSet{}, false
	}
	fridayES, ok := loadOneTemplate(
		ctx,
		c.reader,
		SettingFridayAlertTemplateESCL,
		defaults.fridayESCL,
		compileFridayAlertTemplate,
	)
	if !ok {
		return compiledTemplateSet{}, false
	}
	fridayEN, ok := loadOneTemplate(
		ctx,
		c.reader,
		SettingFridayAlertTemplateENUS,
		defaults.fridayENUS,
		compileFridayAlertTemplate,
	)
	if !ok {
		return compiledTemplateSet{}, false
	}
	matchdayES, ok := loadOneTemplate(
		ctx,
		c.reader,
		SettingMatchdayAlertTemplateESCL,
		defaults.matchdayESCL,
		compileMatchdayAlertTemplate,
	)
	if !ok {
		return compiledTemplateSet{}, false
	}
	matchdayEN, ok := loadOneTemplate(
		ctx,
		c.reader,
		SettingMatchdayAlertTemplateENUS,
		defaults.matchdayENUS,
		compileMatchdayAlertTemplate,
	)
	if !ok {
		return compiledTemplateSet{}, false
	}

	set := compiledTemplateSet{
		reportESCL:   reportES,
		reportENUS:   reportEN,
		fridayESCL:   fridayES,
		fridayENUS:   fridayEN,
		matchdayESCL: matchdayES,
		matchdayENUS: matchdayEN,
	}
	return set, true
}

func loadOneTemplate(
	ctx context.Context,
	reader SettingReader,
	key string,
	fallback *template.Template,
	compile func(string) (*template.Template, error),
) (*template.Template, bool) {
	raw, err := reader.Get(ctx, key)
	if err != nil {
		if errors.Is(err, config.ErrNotSet) {
			return fallback, true
		}

		return nil, false
	}

	compiled, compileErr := compile(raw)
	if compileErr != nil {
		return fallback, true
	}

	return compiled, true
}
