package control

import (
	"sort"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/pricing"
)

type ModelPriceMatchSource string

const (
	ModelPriceMatchSourceChannelCatalogProvider   ModelPriceMatchSource = "channel_catalog_provider"
	ModelPriceMatchSourceProviderPriorityFallback ModelPriceMatchSource = "provider_priority_fallback"
)

type automaticPriceMatch struct {
	cost       *catalog.ModelCost
	providerID string
	source     ModelPriceMatchSource
}

func resolveAutomaticPriceForIdentity(
	snapshot *catalog.Snapshot,
	identity pricing.Identity,
) (automaticPriceMatch, bool) {
	model, providerID, source, ok := resolveCatalogModelForIdentity(snapshot, identity, true)
	if !ok {
		return automaticPriceMatch{}, false
	}
	return automaticPriceMatch{
		cost: model.Cost, providerID: providerID, source: source,
	}, true
}

func resolveCatalogModelForIdentity(
	snapshot *catalog.Snapshot,
	identity pricing.Identity,
	requirePrice bool,
) (catalog.Model, string, ModelPriceMatchSource, bool) {
	if snapshot == nil || len(snapshot.Providers) == 0 || identity.ModelID == "" {
		return catalog.Model{}, "", "", false
	}
	exactProviderID, known := modelPriceChannelRegistry.CatalogProviderID(channel.ID(identity.ChannelID))
	if !known {
		return catalog.Model{}, "", "", false
	}
	lookup := func(providerID string) (catalog.Model, bool) {
		provider, exists := snapshot.Providers[providerID]
		if !exists {
			return catalog.Model{}, false
		}
		model, exists := provider.Models[identity.ModelID]
		if !exists || (requirePrice && model.Cost == nil) {
			return catalog.Model{}, false
		}
		return model, true
	}
	if exactProviderID != "" {
		model, ok := lookup(exactProviderID)
		if ok {
			return model, exactProviderID, ModelPriceMatchSourceChannelCatalogProvider, true
		}
	}
	for _, providerID := range catalogProviderLookupOrder(snapshot) {
		model, ok := lookup(providerID)
		if !ok {
			continue
		}
		return model, providerID, ModelPriceMatchSourceProviderPriorityFallback, true
	}
	return catalog.Model{}, "", "", false
}

func catalogProviderLookupOrder(snapshot *catalog.Snapshot) []string {
	if snapshot == nil || len(snapshot.Providers) == 0 {
		return nil
	}
	result := make([]string, 0, len(snapshot.Providers))
	seen := make(map[string]struct{}, len(snapshot.Providers))
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
