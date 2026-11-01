package discord

import "github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"

// Runtime setting keys in platform.setting for Universidad Catolica Discord message templates.
const (
	SettingUCReportTemplateESCL        = uccmd.SettingReportTemplateESCL
	SettingUCReportTemplateENUS        = uccmd.SettingReportTemplateENUS
	SettingUCFridayAlertTemplateESCL   = uccmd.SettingFridayAlertTemplateESCL
	SettingUCFridayAlertTemplateENUS   = uccmd.SettingFridayAlertTemplateENUS
	SettingUCMatchdayAlertTemplateESCL = uccmd.SettingMatchdayAlertTemplateESCL
	SettingUCMatchdayAlertTemplateENUS = uccmd.SettingMatchdayAlertTemplateENUS
)

// MaxUCTemplateBytes caps custom UC message templates stored in platform.setting at 16 KiB.
const MaxUCTemplateBytes = uccmd.MaxTemplateBytes

// Embedded default text/template definitions for Universidad Catolica Discord messages.
const (
	DefaultUCReportTemplateESCL        = uccmd.DefaultReportTemplateESCL
	DefaultUCReportTemplateENUS        = uccmd.DefaultReportTemplateENUS
	DefaultUCFridayAlertTemplateESCL   = uccmd.DefaultFridayAlertTemplateESCL
	DefaultUCFridayAlertTemplateENUS   = uccmd.DefaultFridayAlertTemplateENUS
	DefaultUCMatchdayAlertTemplateESCL = uccmd.DefaultMatchdayAlertTemplateESCL
	DefaultUCMatchdayAlertTemplateENUS = uccmd.DefaultMatchdayAlertTemplateENUS
)

// SettingReader reads non-secret runtime settings by key. *config.Store satisfies it.
type SettingReader = uccmd.SettingReader

// ValidateUCReportTemplate validates a uc.template.report.* text/template definition without
// echoing raw input in returned errors.
func ValidateUCReportTemplate(raw string) error {
	return uccmd.ValidateReportTemplate(raw) //nolint:wrapcheck // preserves value-free validation sentinel
}

// ValidateUCFridayAlertTemplate validates a uc.template.friday_alert.* text/template definition
// without echoing raw input in returned errors.
func ValidateUCFridayAlertTemplate(raw string) error {
	return uccmd.ValidateFridayAlertTemplate(raw) //nolint:wrapcheck // preserves value-free validation sentinel
}

// ValidateUCMatchdayAlertTemplate validates a uc.template.matchday_alert.* text/template definition
// without echoing raw input in returned errors.
func ValidateUCMatchdayAlertTemplate(raw string) error {
	return uccmd.ValidateMatchdayAlertTemplate(raw) //nolint:wrapcheck // preserves value-free validation sentinel
}
