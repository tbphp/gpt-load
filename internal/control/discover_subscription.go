package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (s *Service) discoverSubscriptionStageModels(
	ctx context.Context,
	channelID channel.ID,
	stageID string,
) (ModelDiscoveryResult, error) {
	credential, err := s.loadReadySubscriptionStageCredential(ctx, channelID, stageID)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	return s.discoverSubscriptionModelsForChannel(ctx, channelID, credential)
}

func (s *Service) loadReadySubscriptionStageCredential(
	ctx context.Context,
	channelID channel.ID,
	stageID string,
) (subscriptionruntime.Credential, error) {
	stage, err := s.loadCredentialStage(ctx, strings.TrimSpace(stageID))
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	if stage.Status != models.CredentialStageReady || stage.ChannelID != string(channelID) ||
		stage.ConnectionType != models.ConnectionTypeSubscription {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialNotReady
	}
	if s.now().UnixMilli() >= stage.ExpiresAtMS {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialExpired
	}
	credential, err := s.decodeStageSubscriptionCredential(channelID, stage)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	driver, err := s.subscriptionDriver(channelID)
	if err != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	return s.prepareReadySubscriptionStageCredential(ctx, stage, driver, credential)
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
		credential, err := s.prepareStoredSubscriptionCredential(ctx, rows.group, row)
		if err != nil {
			preparationErr = err
			continue
		}
		attempted = true
		result, err := s.discoverSubscriptionModelsForChannel(ctx, channel.ID(rows.group.ChannelID), credential)
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

func (s *Service) prepareStoredSubscriptionCredential(
	ctx context.Context,
	group models.Group,
	row models.Credential,
) (subscriptionruntime.Credential, error) {
	switch row.AuthState {
	case "", models.CredentialAuthStateReady:
	case models.CredentialAuthStateReauthorizationRequired:
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialReauthorizationRequired
	case models.CredentialAuthStateRefreshing, models.CredentialAuthStateOutcomeUnknown:
		return subscriptionruntime.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
	default:
		return subscriptionruntime.Credential{}, app_errors.ErrInternalServer
	}
	canonical, _, err := s.decodeCredential(group, row)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	defer clear(canonical)
	channelID := channel.ID(group.ChannelID)
	driver, driverErr := s.subscriptionDriver(channelID)
	if driverErr != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrInternalServer
	}
	if s.prepareSubscriptionCredential == nil {
		credential, parseErr := driver.Parse(canonical)
		if parseErr != nil {
			return subscriptionruntime.Credential{}, app_errors.ErrInternalServer
		}
		return credential, nil
	}
	prepareContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	credential, evidence := s.prepareSubscriptionCredential(prepareContext, channelID, execution.NewCredentialSnapshot(
		row.ID,
		groupCollectionCredentialVersion(row.SecretVersion),
		groupCollectionCredentialIdentity(row.IdentityFingerprint, group),
		canonical,
	), false)
	if evidence != nil {
		return subscriptionruntime.Credential{}, subscriptionPreparationAPIError(evidence)
	}
	return credential, nil
}

func subscriptionPreparationAPIError(evidence *execution.ErrorEvidence) error {
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

func (s *Service) discoverSubscriptionModelsForChannel(
	ctx context.Context,
	channelID channel.ID,
	credential subscriptionruntime.Credential,
) (ModelDiscoveryResult, error) {
	if s == nil || s.discoverSubscriptionModels == nil {
		return ModelDiscoveryResult{}, app_errors.ErrInternalServer
	}
	discoveryContext, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	ids, err := s.discoverSubscriptionModels(discoveryContext, channelID, credential)
	if err != nil {
		return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
	}
	target := discoveryTarget{channelID: channelID}
	return s.mergeDiscoveredModels(ctx, normalizeDiscoveredModels(ids), target)
}

func (s *Service) decodeStageSubscriptionCredential(channelID channel.ID, stage models.CredentialStage) (subscriptionruntime.Credential, error) {
	plaintext, err := s.encryption.Decrypt(stage.EncryptedPayload)
	if err != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		plaintext = ""
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	plaintext = ""
	driver, driverErr := s.subscriptionDriver(channelID)
	if driverErr != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	credential, err := driver.Parse(payload.Credential)
	if err != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	return credential, nil
}
