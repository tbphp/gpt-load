package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func (s *Service) discoverSubscriptionStageModels(
	ctx context.Context,
	channelID channel.ID,
	stageID string,
) (ModelDiscoveryResult, error) {
	stage, err := s.loadCredentialStage(ctx, strings.TrimSpace(stageID))
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	if stage.Status != models.CredentialStageReady || stage.ChannelID != string(channelID) ||
		stage.ConnectionType != models.ConnectionTypeSubscription {
		return ModelDiscoveryResult{}, app_errors.ErrStagedCredentialNotReady
	}
	if s.now().UnixMilli() >= stage.ExpiresAtMS {
		return ModelDiscoveryResult{}, app_errors.ErrStagedCredentialExpired
	}
	credential, err := s.decodeStageCodexCredential(stage)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	return s.discoverCodexModels(ctx, credential)
}

func (s *Service) discoverSubscriptionGroupModels(
	ctx context.Context,
	rows groupDiscoverySnapshotRows,
) (ModelDiscoveryResult, error) {
	if len(rows.credentials) == 0 {
		return ModelDiscoveryResult{}, app_errors.ErrNoActiveCredential
	}
	for _, row := range rows.credentials {
		canonical, _, err := s.decodeCredential(rows.group, row)
		if err != nil {
			return ModelDiscoveryResult{}, err
		}
		credential, err := cpaembedded.ParseCodexCredentialJSON(canonical)
		clear(canonical)
		if err != nil {
			return ModelDiscoveryResult{}, fmt.Errorf("decode subscription credential: %w", app_errors.ErrInternalServer)
		}
		result, err := s.discoverCodexModels(ctx, credential)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return ModelDiscoveryResult{}, ctx.Err()
		}
	}
	return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
}

func (s *Service) discoverCodexModels(
	ctx context.Context,
	credential cpaembedded.CodexCredential,
) (ModelDiscoveryResult, error) {
	if s == nil || s.listCodexModels == nil {
		return ModelDiscoveryResult{}, app_errors.ErrInternalServer
	}
	discoveryContext, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	models, err := s.listCodexModels(discoveryContext, credential)
	if err != nil {
		return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
	}
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	target := discoveryTarget{channelID: channel.Codex, catalogProviderID: "openai"}
	return s.mergeDiscoveredModels(ctx, normalizeDiscoveredModels(ids), target)
}

func (s *Service) decodeStageCodexCredential(stage models.CredentialStage) (cpaembedded.CodexCredential, error) {
	plaintext, err := s.encryption.Decrypt(stage.EncryptedPayload)
	if err != nil {
		return cpaembedded.CodexCredential{}, app_errors.ErrStagedCredentialMismatch
	}
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		plaintext = ""
		return cpaembedded.CodexCredential{}, app_errors.ErrStagedCredentialMismatch
	}
	plaintext = ""
	canonical, err := json.Marshal(payload.Credential)
	if err != nil {
		return cpaembedded.CodexCredential{}, app_errors.ErrInternalServer
	}
	credential, err := cpaembedded.ParseCodexCredentialJSON(canonical)
	clear(canonical)
	if err != nil {
		return cpaembedded.CodexCredential{}, app_errors.ErrStagedCredentialMismatch
	}
	return credential, nil
}
