package loader

import (
	"testing"

	"gpt-load/internal/storage/models"
)

func TestMapSystemAndGroupsDeepClonesProviderID(t *testing.T) {
	providerID := "openai"
	rows := compileRows{groups: []models.Group{{
		ID:          1,
		Name:        "provider-group",
		ProviderID:  &providerID,
		UpstreamURL: "https://api.openai.com/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[{"id":"gpt-4o","alias":"public"}]`),
		Config:      models.JSON(`{}`),
		Enabled:     true,
	}}}

	input, err := mapSystemAndGroups(rows)
	if err != nil {
		t.Fatalf("mapSystemAndGroups() error = %v", err)
	}
	providerID = "mutated-source"
	*rows.groups[0].ProviderID = "mutated-row"

	if got := input.Groups[0].ProviderID; got == nil || *got != "openai" {
		t.Fatalf("GroupConfig.ProviderID = %v, want independent openai", got)
	}
}
