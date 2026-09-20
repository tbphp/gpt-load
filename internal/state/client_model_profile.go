package state

import (
	"slices"
	"sort"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
)

var clientModelChannelRegistry = channel.NewRegistry()

type ClientModelProfileResult struct {
	Automatic          catalog.ClientModelProfile
	Effective          catalog.ClientModelProfile
	SourceCount        int
	UnknownSourceCount int
}

func ResolveClientModelProfile(snapshot *ConfigSnapshot, runtime *catalog.Runtime, model string, filters FilterSet, protocols []protocol.Protocol) ClientModelProfileResult {
	return resolveClientModelProfile(snapshot, runtime.ClientProfiles(), model, filters, protocols)
}

func ResolveClientModelProfiles(snapshot *ConfigSnapshot, runtime *catalog.Runtime, models []string, filters FilterSet, protocols []protocol.Protocol) map[string]ClientModelProfileResult {
	profiles := runtime.ClientProfiles()
	result := make(map[string]ClientModelProfileResult, len(models))
	for _, model := range models {
		result[model] = resolveClientModelProfile(snapshot, profiles, model, filters, protocols)
	}
	return result
}

func resolveClientModelProfile(snapshot *ConfigSnapshot, profiles *catalog.ClientModelProfiles, model string, filters FilterSet, protocols []protocol.Protocol) ClientModelProfileResult {
	result := ClientModelProfileResult{Automatic: catalog.DefaultClientModelProfile(model)}
	result.Effective = result.Automatic.Clone()
	if snapshot == nil {
		return result
	}
	if len(filters.Models) > 0 {
		if _, allowed := filters.Models[model]; !allowed {
			return result
		}
	}
	type sourceKey struct {
		groupID uint
		modelID string
	}
	sources := make(map[sourceKey]struct{})
	for clientProtocol, operations := range snapshot.ExecutionCandidates {
		if len(protocols) > 0 && !slices.Contains(protocols, clientProtocol) {
			continue
		}
		if len(filters.Protocols) > 0 {
			if _, allowed := filters.Protocols[clientProtocol]; !allowed {
				continue
			}
		}
		for _, byModel := range operations {
			for _, target := range byModel[model] {
				if len(filters.Groups) > 0 {
					if _, allowed := filters.Groups[target.GroupID]; !allowed {
						continue
					}
				}
				if _, enabled := snapshot.Groups[target.GroupID]; enabled && target.UpstreamModelID != "" {
					sources[sourceKey{groupID: target.GroupID, modelID: target.UpstreamModelID}] = struct{}{}
				}
			}
		}
	}
	ordered := make([]sourceKey, 0, len(sources))
	for source := range sources {
		ordered = append(ordered, source)
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].groupID != ordered[right].groupID {
			return ordered[left].groupID < ordered[right].groupID
		}
		return ordered[left].modelID < ordered[right].modelID
	})
	candidates := make([]catalog.ClientModelProfile, 0, len(ordered))
	for _, source := range ordered {
		group := snapshot.Groups[source.groupID]
		provider, _ := clientModelChannelRegistry.CatalogProviderID(group.ChannelID)
		profile, known := profiles.Resolve(provider, source.modelID)
		if !known {
			result.UnknownSourceCount++
		}
		candidates = append(candidates, profile)
	}
	result.SourceCount = len(candidates)
	result.Automatic = catalog.IntersectClientModelProfiles(model, candidates)
	result.Effective = result.Automatic.Apply(snapshot.ClientModelOverrides[model])
	return result
}
