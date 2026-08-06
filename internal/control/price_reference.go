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

	lookup := func(providerID string) (*catalog.ModelCost, bool) {
		provider, exists := snapshot.Providers[providerID]
		if !exists {
			return nil, false
		}
		model, exists := provider.Models[modelID]
		if !exists || model.Cost == nil {
			return nil, false
		}
		return model.Cost, true
	}

	if scopeProviderID != "" {
		if cost, ok := lookup(scopeProviderID); ok {
			return cost, scopeProviderID, true
		}
	}

	priority := catalog.AutomaticPriceProviderPriority()
	fixedProviderIDs := make(map[string]struct{}, len(priority))
	for _, providerID := range priority {
		fixedProviderIDs[providerID] = struct{}{}
		if providerID == scopeProviderID {
			continue
		}
		if cost, ok := lookup(providerID); ok {
			return cost, providerID, true
		}
	}

	providerIDs := make([]string, 0, len(snapshot.Providers))
	for providerID := range snapshot.Providers {
		if providerID == scopeProviderID {
			continue
		}
		if _, fixed := fixedProviderIDs[providerID]; fixed {
			continue
		}
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		if cost, ok := lookup(providerID); ok {
			return cost, providerID, true
		}
	}

	return nil, "", false
}
