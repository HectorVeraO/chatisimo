package discord_test

import (
	"strings"
	"testing"

	appdiscord "github.com/HectorVeraO/chatisimo/internal/discord"
)

func TestPublicUCTemplateValidatorsAcceptDefaultsAndRedactInvalidInputs(t *testing.T) {
	t.Parallel()

	if err := appdiscord.ValidateUCReportTemplate(appdiscord.DefaultUCReportTemplateESCL); err != nil {
		t.Fatalf("ValidateUCReportTemplate(es-CL): %v", err)
	}
	if err := appdiscord.ValidateUCReportTemplate(appdiscord.DefaultUCReportTemplateENUS); err != nil {
		t.Fatalf("ValidateUCReportTemplate(en-US): %v", err)
	}
	if err := appdiscord.ValidateUCFridayAlertTemplate(appdiscord.DefaultUCFridayAlertTemplateESCL); err != nil {
		t.Fatalf("ValidateUCFridayAlertTemplate(es-CL): %v", err)
	}
	if err := appdiscord.ValidateUCFridayAlertTemplate(appdiscord.DefaultUCFridayAlertTemplateENUS); err != nil {
		t.Fatalf("ValidateUCFridayAlertTemplate(en-US): %v", err)
	}
	if err := appdiscord.ValidateUCMatchdayAlertTemplate(appdiscord.DefaultUCMatchdayAlertTemplateESCL); err != nil {
		t.Fatalf("ValidateUCMatchdayAlertTemplate(es-CL): %v", err)
	}
	if err := appdiscord.ValidateUCMatchdayAlertTemplate(appdiscord.DefaultUCMatchdayAlertTemplateENUS); err != nil {
		t.Fatalf("ValidateUCMatchdayAlertTemplate(en-US): %v", err)
	}

	const probe = "{{.LeakedFieldIdentifier}}"
	for _, fn := range []func(string) error{
		appdiscord.ValidateUCReportTemplate,
		appdiscord.ValidateUCFridayAlertTemplate,
		appdiscord.ValidateUCMatchdayAlertTemplate,
	} {
		err := fn(probe)
		if err == nil {
			t.Fatal("validator accepted unknown field")
		}
		if strings.Contains(err.Error(), "LeakedFieldIdentifier") {
			t.Errorf("validator error %q leaked field identifier", err)
		}
	}
}
