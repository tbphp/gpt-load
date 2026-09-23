package control

import (
	"sort"
	"strings"

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

// lookupCatalogModel attempts to find a model by exact ID first, then falls back
// to progressive token-boundary prefix trimming (splitting on hyphens from right to left)
// if exact match fails. This allows variant model names (e.g., "gemini-3.8-flash-high")
// to match their base catalog entries (e.g., "gemini-3.8-flash") without hardcoding.
func lookupCatalogModel(
	provider catalog.Provider,
	modelID string,
	requirePrice bool,
) (catalog.Model, bool) {
	if provider.Models == nil || modelID == "" {
		return catalog.Model{}, false
	}
	// 1. Exact match
	if model, exists := provider.Models[modelID]; exists {
		if !requirePrice || model.Cost != nil {
			return model, true
		}
	}
	// 2. Token-boundary prefix fallback
	for candidateID := modelID; ; {
		lastHyphen := strings.LastIndex(candidateID, "-")
		if lastHyphen <= 0 {
			break
		}
		candidateID = candidateID[:lastHyphen]
		if model, exists := provider.Models[candidateID]; exists {
			if !requirePrice || model.Cost != nil {
				return model, true
			}
		}
	}
	return catalog.Model{}, false
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
		return lookupCatalogModel(provider, identity.ModelID, requirePrice)
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
