package fakediscord

import (
	"fmt"
	"sort"
	"strings"
)

// TemplateData and UserJSON let the external tests fill the payload templates.
type (
	TemplateData = templateData
	UserJSON     = userJSON
)

// FillTemplate renders one embedded payload template.
func FillTemplate(name string, data TemplateData) ([]byte, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	return fillTemplate(templates, name, data)
}

// TemplateNames lists the embedded payload template files.
func TemplateNames() ([]string, error) {
	entries, err := payloadFiles.ReadDir(strings.TrimSuffix(payloadDir, "/"))
	if err != nil {
		return nil, fmt.Errorf("list payload templates: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	return names, nil
}

// TemplateSource returns the raw text of an embedded payload template.
func TemplateSource(name string) (string, error) {
	raw, err := payloadFiles.ReadFile(payloadDir + name)
	if err != nil {
		return "", fmt.Errorf("read payload template %s: %w", name, err)
	}

	return string(raw), nil
}
