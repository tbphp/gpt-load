package control

import (
	"context"
	"fmt"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

type ModelDiscoveryRequest struct {
	ProviderID  *string             `json:"provider_id,omitempty"`
	UpstreamURL string              `json:"upstream_url"`
	Protocols   []protocol.Protocol `json:"protocols"`
	Keys        string              `json:"keys"`
}

type ModelDiscoveryResult struct {
	Models []ModelCandidate `json:"models"`
}

func (s *Service) DiscoverModels(
	ctx context.Context,
	request ModelDiscoveryRequest,
) (ModelDiscoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return ModelDiscoveryResult{}, err
	}

	baseURL, _, err := normalizeUpstreamBaseURL(request.UpstreamURL)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	protocols, err := normalizeGroupProtocols(request.Protocols)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	providerID, err := normalizeProviderID(request.ProviderID)
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	keys, err := s.normalizeUpstreamKeys(request.Keys)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	systemSettings, err := stateloader.LoadSystemSettings(ctx, s.db)
	if parentErr := ctx.Err(); parentErr != nil {
		return ModelDiscoveryResult{}, parentErr
	}
	if err != nil {
		return ModelDiscoveryResult{}, fmt.Errorf("load model discovery settings: %w", err)
	}
	snapshot, err := state.Compile(state.CompileInput{
		SystemSettings: systemSettings,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "draft", UpstreamURL: baseURL,
			Protocols: protocols, Settings: config.Settings{}, Enabled: true,
		}},
	})
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	group, ok := snapshot.Groups[1]
	if !ok {
		return ModelDiscoveryResult{}, fmt.Errorf("compiled model discovery draft is missing")
	}

	plaintextKeys := make([]string, 0, len(keys.candidates))
	for _, candidate := range keys.candidates {
		plaintextKeys = append(plaintextKeys, candidate.plaintext)
	}
	priceScopeKey := ""
	if providerID != nil {
		priceScopeKey, _ = pricing.ProviderScopeKey(*providerID)
	}
	return s.executeModelDiscovery(ctx, discoveryTarget{
		baseURL:       baseURL,
		protocols:     protocols,
		keys:          plaintextKeys,
		headerRules:   group.HeaderRules,
		providerID:    providerID,
		priceScopeKey: priceScopeKey,
	})
}
