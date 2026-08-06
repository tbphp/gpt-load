package control

import (
	"sort"

	"gpt-load/internal/catalog"
)

// resolveAutomaticPrice selects an exact catalog model price without changing
// the supplied snapshot. It prefers the scoped provider, then the fixed
// automatic price provider order, then remaining providers by ID.
func resolveAutomaticPrice(
	snapshot *catalog.Snapshot,
	scopeProviderID string,
	modelID string,
) (cost *catalog.ModelCost, matchedProviderID string, ok bool) {
	if snapshot == nil || len(snapshot.Providers) == 0 || modelID == "" {
		return nil, "", false
	}
	for _, providerID := range catalogProviderLookupOrder(snapshot, scopeProviderID) {
		provider, exists := snapshot.Providers[providerID]
		if !exists {
			continue
		}
		model, exists := provider.Models[modelID]
		if !exists || model.Cost == nil {
			continue
		}
		return model.Cost, providerID, true
	}
	return nil, "", false
}

func catalogProviderLookupOrder(snapshot *catalog.Snapshot, preferredProviderID string) []string {
	if snapshot == nil || len(snapshot.Providers) == 0 {
		return nil
	}
	result := make([]string, 0, len(snapshot.Providers)+1)
	seen := make(map[string]struct{}, len(snapshot.Providers)+1)
	appendProvider := func(providerID string) {
		if providerID == "" {
			return
		}
		if _, duplicate := seen[providerID]; duplicate {
			return
		}
		seen[providerID] = struct{}{}
		result = append(result, providerID)
	}
	appendProvider(preferredProviderID)
	priority := catalog.AutomaticPriceProviderPriority()
	for _, providerID := range priority {
		appendProvider(providerID)
	}

	providerIDs := make([]string, 0, len(snapshot.Providers))
	for providerID := range snapshot.Providers {
		if _, duplicate := seen[providerID]; duplicate {
			continue
		}
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		appendProvider(providerID)
	}
	return result
}
