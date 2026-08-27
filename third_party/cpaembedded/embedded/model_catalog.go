package embedded

import (
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

// MergeModelCatalog is the narrow CPA-owned model catalog seam used by
// subscription discovery. The caller must invoke it only after the account's
// live model request succeeds.
func MergeModelCatalog(provider string, upstream []string) []string {
	provider = strings.TrimSpace(provider)
	result := normalizeModelIDs(upstream)
	if strings.EqualFold(provider, ProviderAntigravity) {
		filtered := result[:0]
		for _, id := range result {
			if _, excluded := antigravityExcludedModelIDs[id]; excluded {
				continue
			}
			filtered = append(filtered, id)
		}
		result = filtered
	}
	seen := make(map[string]struct{}, len(result))
	for _, value := range result {
		seen[value] = struct{}{}
	}
	for _, model := range registry.GetStaticModelDefinitionsByChannel(provider) {
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" || strings.ContainsAny(id, "\r\n\x00") {
			continue
		}
		if strings.EqualFold(provider, ProviderAntigravity) {
			if _, excluded := antigravityExcludedModelIDs[id]; excluded {
				continue
			}
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func normalizeModelIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" || strings.ContainsAny(id, "\r\n\x00") {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
