package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	"gpt-load/internal/execution"
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
	var preparationErr error
	attempted := false
	for _, row := range rows.credentials {
		credential, err := s.prepareStoredCodexCredential(ctx, rows.group, row)
		if err != nil {
			preparationErr = err
			continue
		}
		attempted = true
		result, err := s.discoverCodexModels(ctx, credential)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return ModelDiscoveryResult{}, ctx.Err()
		}
	}
	if !attempted && preparationErr != nil {
		return ModelDiscoveryResult{}, preparationErr
	}
	return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
}

func (s *Service) prepareStoredCodexCredential(
	ctx context.Context,
	group models.Group,
	row models.Credential,
) (codex.Credential, error) {
	switch row.AuthState {
	case "", models.CredentialAuthStateReady:
	case models.CredentialAuthStateReauthorizationRequired:
		return codex.Credential{}, app_errors.ErrCredentialReauthorizationRequired
	case models.CredentialAuthStateRefreshing, models.CredentialAuthStateOutcomeUnknown:
		return codex.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
	default:
		return codex.Credential{}, app_errors.ErrInternalServer
	}
	canonical, _, err := s.decodeCredential(group, row)
	if err != nil {
		return codex.Credential{}, err
	}
	defer clear(canonical)
	if s.prepareCodexCredential == nil {
		credential, parseErr := codex.ParseCredentialJSON(canonical)
		if parseErr != nil {
			return codex.Credential{}, app_errors.ErrInternalServer
		}
		return credential, nil
	}
	prepareContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	credential, evidence := s.prepareCodexCredential(prepareContext, execution.NewCredentialSnapshot(
		row.ID,
		groupCollectionCredentialVersion(row.SecretVersion),
		groupCollectionCredentialIdentity(row.IdentityFingerprint, group),
		canonical,
	))
	if evidence != nil {
		return codex.Credential{}, codexPreparationAPIError(evidence)
	}
	return credential, nil
}

func codexPreparationAPIError(evidence *execution.ErrorEvidence) error {
	if evidence == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(evidence.Code)) {
	case "outcome_unknown", "refreshing", "refresh_outcome_unknown", "refresh_persist_failed",
		"refresh_commit_failed", "refresh_registry_mismatch", "refresh_state_commit_failed":
		return app_errors.ErrCredentialAuthOutcomeUnknown
	case "reauthorization_required", "refresh_rejected", "refresh_identity_changed":
		return app_errors.ErrCredentialReauthorizationRequired
	}
	if evidence.Hint == execution.FailureHintReauthorizationRequired {
		return app_errors.ErrCredentialReauthorizationRequired
	}
	if evidence.Kind == execution.ErrorKindCanceled || evidence.Kind == execution.ErrorKindTimeout ||
		evidence.Kind == execution.ErrorKindTransport || evidence.Kind == execution.ErrorKindHTTP ||
		evidence.Kind == execution.ErrorKindProvider {
		return app_errors.ErrBadGateway
	}
	return app_errors.ErrInternalServer
}

func (s *Service) discoverCodexModels(
	ctx context.Context,
	credential codex.Credential,
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
	target := discoveryTarget{channelID: channel.Codex}
	return s.mergeDiscoveredModels(ctx, normalizeDiscoveredModels(ids), target)
}

func (s *Service) decodeStageCodexCredential(stage models.CredentialStage) (codex.Credential, error) {
	plaintext, err := s.encryption.Decrypt(stage.EncryptedPayload)
	if err != nil {
		return codex.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		plaintext = ""
		return codex.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	plaintext = ""
	canonical, err := codex.MarshalCredential(payload.Credential)
	if err != nil {
		return codex.Credential{}, app_errors.ErrInternalServer
	}
	credential, err := codex.ParseCredentialJSON(canonical)
	clear(canonical)
	if err != nil {
		return codex.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	return credential, nil
}
