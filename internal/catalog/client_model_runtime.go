package catalog

import (
	"slices"
	"sort"
)

type ClientModelProfiles struct {
	providers map[string]map[string]ClientModelProfile
	fallback  map[string]ClientModelProfile
}

func (runtime *Runtime) ClientProfiles() *ClientModelProfiles {
	if runtime == nil {
		return nil
	}
	generation := runtime.snapshot.Load()
	if generation == nil {
		return nil
	}
	return generation.profiles
}

func (profiles *ClientModelProfiles) Resolve(providerID, modelID string) (ClientModelProfile, bool) {
	if profiles != nil {
		if profile, found := profiles.providers[providerID][modelID]; found {
			return profile.Clone(), true
		}
		if profile, found := profiles.fallback[modelID]; found {
			return profile.Clone(), true
		}
	}
	return DefaultClientModelProfile(modelID), false
}

func compileClientModelProfiles(snapshot *Snapshot) *ClientModelProfiles {
	profiles := &ClientModelProfiles{
		providers: make(map[string]map[string]ClientModelProfile),
		fallback:  make(map[string]ClientModelProfile),
	}
	order := AutomaticPriceProviderPriority()
	var remaining []string
	for providerID := range snapshot.Providers {
		if !slices.Contains(order, providerID) {
			remaining = append(remaining, providerID)
		}
	}
	sort.Strings(remaining)
	for _, providerID := range append(order, remaining...) {
		provider, found := snapshot.Providers[providerID]
		if !found {
			continue
		}
		models := make(map[string]ClientModelProfile, len(provider.Models))
		for modelID, model := range provider.Models {
			profile := DefaultClientModelProfile(modelID)
			profile.Description = model.Metadata.Description
			if limit := model.Metadata.Limits.Context; limit != nil && *limit > 0 {
				profile.ContextWindow = cloneProfileValue(limit)
			}
			for _, modality := range []string{"image", "audio"} {
				if slices.Contains(model.Metadata.Modalities.Input, modality) {
					profile.InputModalities = append(profile.InputModalities, modality)
				}
			}
			models[modelID] = profile
			if _, exists := profiles.fallback[modelID]; !exists {
				profiles.fallback[modelID] = profile
			}
		}
		profiles.providers[providerID] = models
	}
	return profiles
}
