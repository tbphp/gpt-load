package embedded

import (
	"sort"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

func TestMergeModelCatalogUsesCPAStaticDefinitionsForEverySubscriptionChannel(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{ProviderCodex, ProviderClaude, ProviderAntigravity, ProviderGrok} {
		t.Run(provider, func(t *testing.T) {
			static := registry.GetStaticModelDefinitionsByChannel(provider)
			if len(static) == 0 {
				t.Fatalf("CPA static catalog for %q is empty", provider)
			}
			catalogID := ""
			for _, model := range static {
				if model != nil && strings.TrimSpace(model.ID) != "" {
					candidate := strings.TrimSpace(model.ID)
					if provider == ProviderAntigravity {
						if _, excluded := antigravityExcludedModelIDs[candidate]; excluded {
							continue
						}
					}
					catalogID = candidate
					break
				}
			}
			if catalogID == "" {
				t.Fatalf("CPA static catalog for %q has no usable model ID", provider)
			}

			got := MergeModelCatalog(provider, []string{" upstream-only ", catalogID, catalogID})
			if !sort.StringsAreSorted(got) || !containsModelID(got, "upstream-only") || !containsModelID(got, catalogID) {
				t.Fatalf("merged model IDs = %#v", got)
			}
			if len(got) != len(uniqueModelIDs(got)) {
				t.Fatalf("merged model IDs contain duplicates: %#v", got)
			}
		})
	}
}

func TestMergeModelCatalogKeepsAntigravityExcludedModelsOut(t *testing.T) {
	t.Parallel()

	got := MergeModelCatalog("antigravity", []string{"gemini-2.5-pro"})
	for id := range antigravityExcludedModelIDs {
		if containsModelID(got, id) {
			t.Fatalf("merged Antigravity catalog retained excluded model %q: %#v", id, got)
		}
	}
}

func containsModelID(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func uniqueModelIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
