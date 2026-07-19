package control

import (
	"context"
	"fmt"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

type ModelDiscoveryRequest struct {
	UpstreamURL string              `json:"upstream_url"`
	Protocols   []protocol.Protocol `json:"protocols"`
	Keys        string              `json:"keys"`
	Config      config.Settings     `json:"config"`
}

type ModelDiscoveryResult struct {
	Models []string `json:"models"`
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
	keys, err := s.normalizeUpstreamKeys(request.Keys)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	groupSettings, _, err := normalizeGroupSettings(request.Config)
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
			Protocols: protocols, Settings: groupSettings, Enabled: true,
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
	return s.executeModelDiscovery(ctx, discoveryTarget{
		baseURL:     baseURL,
		protocols:   protocols,
		keys:        plaintextKeys,
		headerRules: group.HeaderRules,
	})
}
