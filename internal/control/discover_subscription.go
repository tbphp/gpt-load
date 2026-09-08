package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

// discoverSubscriptionStageModels discovers models with a staged credential
// and retries once after forcing a refresh on an authorization failure.
func (s *Service) discoverSubscriptionStageModels(
	ctx context.Context,
	channelID channel.ID,
	stageID string,
	network subscriptionruntime.NetworkContext,
	target subscriptionruntime.Target,
) (ModelDiscoveryResult, error) {
	ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	credential, err := s.loadReadySubscriptionStageCredential(ctx, channelID, stageID)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	result, err := s.discoverSubscriptionModelsForChannel(ctx, channelID, credential, target)
	if subscriptionUpstreamHTTPStatus(err) != http.StatusUnauthorized {
		return result, err
	}
	credential, err = s.forceRefreshReadySubscriptionStageCredential(ctx, channelID, stageID)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	return s.discoverSubscriptionModelsForChannel(ctx, channelID, credential, target)
}

func (s *Service) forceRefreshReadySubscriptionStageCredential(
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
	credential, err := s.decodeStageSubscriptionCredential(channelID, stage)
	if err != nil {
		return subscriptionruntime.Credential{}, err
	}
	driver, err := s.subscriptionDriver(channelID)
	if err != nil {
		return subscriptionruntime.Credential{}, app_errors.ErrStagedCredentialMismatch
	}
	return s.prepareReadySubscriptionStageCredential(ctx, stage, driver, credential, true)
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
	return s.prepareReadySubscriptionStageCredential(ctx, stage, driver, credential, false)
}

// discoverSubscriptionGroupModels tries the active credentials in a persisted
// group until one completes provider-native model discovery.
func (s *Service) discoverSubscriptionGroupModels(
	ctx context.Context,
	rows groupDiscoverySnapshotRows,
) (ModelDiscoveryResult, error) {
	if len(rows.credentials) == 0 {
		return ModelDiscoveryResult{}, app_errors.ErrNoActiveCredential
	}
	target, err := s.resolveSubscriptionTarget(channel.ID(rows.group.ChannelID), rows.group.Params)
	if err != nil {
		return ModelDiscoveryResult{}, app_errors.ErrInternalServer
	}
	var preparationErr error
	attempted := false
	for _, row := range rows.credentials {
		network, networkErr := s.credentialNetworkContext(ctx, s.db, rows.group, row)
		if networkErr != nil {
			preparationErr = networkErr
			continue
		}
		attemptContext := subscriptionruntime.WithNetworkContext(ctx, network)
		credential, err := s.prepareStoredSubscriptionCredential(attemptContext, rows.group, row)
		if err != nil {
			preparationErr = err
			continue
		}
		attempted = true
		result, err := s.discoverSubscriptionModelsForChannel(attemptContext, channel.ID(rows.group.ChannelID), credential, target)
		if err == nil {
			return result, nil
		}
		if subscriptionUpstreamHTTPStatus(err) == http.StatusUnauthorized {
			credential, prepareErr := s.prepareStoredSubscriptionCredentialWithForce(attemptContext, rows.group, row, true)
			if prepareErr != nil {
				preparationErr = prepareErr
				continue
			}
			result, err = s.discoverSubscriptionModelsForChannel(attemptContext, channel.ID(rows.group.ChannelID), credential, target)
			if err == nil {
				return result, nil
			}
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
	return s.prepareStoredSubscriptionCredentialWithForce(ctx, group, row, false)
}

func (s *Service) prepareStoredSubscriptionCredentialWithForce(
	ctx context.Context,
	group models.Group,
	row models.Credential,
	forceRefresh bool,
) (subscriptionruntime.Credential, error) {
	return s.prepareStoredSubscriptionCredentialInternal(ctx, group, row, forceRefresh, false)
}

// recoverStoredSubscriptionCredential runs one operator-initiated refresh that
// is allowed to recover a credential from a failed auth state. Only the
// explicit "refresh credential" action may use it.
func (s *Service) recoverStoredSubscriptionCredential(
	ctx context.Context,
	group models.Group,
	row models.Credential,
) (subscriptionruntime.Credential, error) {
	return s.prepareStoredSubscriptionCredentialInternal(ctx, group, row, true, true)
}

func (s *Service) prepareStoredSubscriptionCredentialInternal(
	ctx context.Context,
	group models.Group,
	row models.Credential,
	forceRefresh bool,
	allowRecovery bool,
) (subscriptionruntime.Credential, error) {
	if _, frozen := subscriptionruntime.NetworkFromContext(ctx); !frozen {
		network, err := s.credentialNetworkContext(ctx, s.db, group, row)
		if err != nil {
			return subscriptionruntime.Credential{}, err
		}
		ctx = subscriptionruntime.WithNetworkContext(ctx, network)
	}
	switch row.AuthState {
	case "", models.CredentialAuthStateReady:
	case models.CredentialAuthStateReauthorizationRequired:
		if !allowRecovery {
			return subscriptionruntime.Credential{}, app_errors.ErrCredentialReauthorizationRequired
		}
	case models.CredentialAuthStateRefreshing, models.CredentialAuthStateOutcomeUnknown:
		if !allowRecovery {
			return subscriptionruntime.Credential{}, app_errors.ErrCredentialAuthOutcomeUnknown
		}
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
	if s.prepareSubscriptionCredential == nil || (allowRecovery && s.recoverSubscriptionCredential == nil) {
		credential, parseErr := driver.Parse(canonical)
		if parseErr != nil {
			return subscriptionruntime.Credential{}, app_errors.ErrInternalServer
		}
		return credential, nil
	}
	prepareContext, cancel := context.WithTimeout(ctx, defaultSubscriptionControlTimeout)
	defer cancel()
	snapshot := execution.NewCredentialSnapshot(
		row.ID,
		groupCollectionCredentialVersion(row.SecretVersion),
		groupCollectionCredentialIdentity(row.IdentityFingerprint, group),
		canonical,
	)
	var credential subscriptionruntime.Credential
	var evidence *execution.ErrorEvidence
	if allowRecovery {
		credential, evidence = s.recoverSubscriptionCredential(prepareContext, channelID, snapshot)
	} else {
		credential, evidence = s.prepareSubscriptionCredential(prepareContext, channelID, snapshot, forceRefresh)
	}
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
	case "refresh_temporarily_unavailable":
		return app_errors.ErrCredentialRefreshTemporarilyUnavailable
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

// discoverSubscriptionModelsForChannel invokes the channel capability under a
// bounded timeout and merges its normalized IDs into the shared catalog.
func (s *Service) discoverSubscriptionModelsForChannel(
	ctx context.Context,
	channelID channel.ID,
	credential subscriptionruntime.Credential,
	target subscriptionruntime.Target,
) (ModelDiscoveryResult, error) {
	if s == nil || s.discoverSubscriptionModels == nil {
		return ModelDiscoveryResult{}, app_errors.ErrInternalServer
	}
	discoveryContext, cancel := context.WithTimeout(ctx, s.modelDiscoveryTimeout)
	defer cancel()
	ids, err := s.discoverSubscriptionModels(discoveryContext, channelID, credential, target)
	if err != nil {
		if status := subscriptionUpstreamHTTPStatus(err); status != 0 {
			return ModelDiscoveryResult{}, fmt.Errorf(
				"discover upstream models: %w: %w",
				app_errors.ErrBadGateway,
				&subscriptionruntime.UpstreamHTTPError{StatusCode: status},
			)
		}
		return ModelDiscoveryResult{}, fmt.Errorf("discover upstream models: %w", app_errors.ErrBadGateway)
	}
	mergeTarget := discoveryTarget{channelID: channelID}
	return s.mergeDiscoveredModels(ctx, normalizeDiscoveredModels(ids), mergeTarget)
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
