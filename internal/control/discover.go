package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type ModelDiscoveryRequest struct {
	ChannelID          channel.ID            `json:"channel_id"`
	ConnectionType     models.ConnectionType `json:"connection_type"`
	Params             json.RawMessage       `json:"params"`
	Credentials        string                `json:"credentials"`
	StagedCredentialID string                `json:"staged_credential_id"`
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

	if s == nil || s.channelRegistry == nil || request.ChannelID == "" {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	connectionType, err := s.resolveChannelConnectionType(request.ChannelID, request.ConnectionType)
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	if connectionType == models.ConnectionTypeSubscription {
		if request.ChannelID != channel.Codex || strings.TrimSpace(request.Credentials) != "" || strings.TrimSpace(request.StagedCredentialID) == "" {
			return ModelDiscoveryResult{}, app_errors.ErrValidation
		}
		return s.discoverSubscriptionStageModels(ctx, request.ChannelID, request.StagedCredentialID)
	}
	if strings.TrimSpace(request.StagedCredentialID) != "" {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	resolvedTarget, err := s.channelRegistry.Resolve(request.ChannelID, request.Params)
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	credentials, err := s.normalizeCredentials(request.ChannelID, request.Credentials)
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
		SystemSettings: systemSettings, ChannelRegistry: s.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "draft", ChannelID: request.ChannelID,
			Params: append(json.RawMessage(nil), request.Params...), Enabled: true,
		}},
	})
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	group, ok := snapshot.Groups[1]
	if !ok {
		return ModelDiscoveryResult{}, fmt.Errorf("compiled model discovery draft is missing")
	}

	discoveryCredentials := make([]discoveryCredential, 0, len(credentials.candidates))
	for index, candidate := range credentials.candidates {
		credential, err := s.channelRegistry.ValidateCredential(request.ChannelID, candidate.canonical)
		if err != nil {
			return ModelDiscoveryResult{}, app_errors.ErrValidation
		}
		apiKey, _ := credential.Value("api_key")
		discoveryCredentials = append(discoveryCredentials, discoveryCredential{
			snapshot: execution.NewCredentialSnapshot(
				uint(index+1), 1, uint64(index+1), credential.CanonicalJSON(),
			),
			apiKey: apiKey,
		})
	}
	return s.executeModelDiscovery(ctx, discoveryTarget{
		channelID: request.ChannelID, resolvedTarget: resolvedTarget,
		credentials: discoveryCredentials, headerRules: group.HeaderRules,
		timeouts: group.Timeouts, catalogProviderID: resolvedTarget.CatalogProviderID,
	})
}
