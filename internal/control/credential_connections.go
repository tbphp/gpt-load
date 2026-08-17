package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type CredentialConnectRequest struct {
	StagedCredentialIDs []string `json:"staged_credential_ids"`
}

type CredentialReauthorizationRequest struct {
	StageID               string `json:"stage_id"`
	ExpectedSecretVersion uint64 `json:"expected_secret_version"`
}

type credentialConnectDigestBody struct {
	StagedCredentialIDs []string `json:"staged_credential_ids"`
}

type credentialReauthorizationDigestBody struct {
	StageID               string `json:"stage_id"`
	ExpectedSecretVersion uint64 `json:"expected_secret_version"`
}

type credentialReauthorizationResult struct {
	GroupID       uint   `json:"group_id"`
	CredentialID  uint   `json:"credential_id"`
	SecretVersion uint64 `json:"secret_version"`
}

// ConnectGroupCredentialsIdempotent consumes subscription stages exactly once
// while allowing the same HTTP operation to recover after a lost response.
func (s *Service) ConnectGroupCredentialsIdempotent(
	ctx context.Context,
	idempotencyKey string,
	groupID uint,
	stageIDs []string,
) (CredentialImportResult, error) {
	normalized, err := normalizeCredentialStageIDs(stageIDs)
	if groupID == 0 || err != nil {
		return CredentialImportResult{}, app_errors.ErrValidation
	}
	canonicalBody, err := canonicalIdempotencyBody(credentialConnectDigestBody{
		StagedCredentialIDs: normalized,
	})
	if err != nil {
		return CredentialImportResult{}, app_errors.ErrInternalServer
	}
	resourceIdentity := "group:" + strconv.FormatUint(uint64(groupID), 10)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindCredentialImport,
		PathTemplate:    "/api/groups/:group_id/credentials/connect",
		ResourceLocator: resourceIdentity, AuthScopeID: idempotencyAuthScopeID,
		CanonicalBody: canonicalBody,
	})
	if err != nil {
		return CredentialImportResult{}, app_errors.ErrInternalServer
	}
	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey, DigestVersion: 1, RequestDigest: digest.Digest,
		Kind: operationKindCredentialImport,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			result, entries, err := s.connectGroupCredentialsMutation(ctx, tx, groupID, normalized)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if err := state.ValidateCredentialEntries(entries); err != nil {
				return idempotentMutationResult{}, err
			}
			canonicalResult, err := canonicaljson.Marshal(result)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			return idempotentMutationResult{
				ResourceIdentity: resourceIdentity,
				CanonicalResult:  canonicalResult,
			}, nil
		},
	})
	if err != nil {
		return CredentialImportResult{}, err
	}
	var result CredentialImportResult
	if err := json.Unmarshal(operationResult.CanonicalResult, &result); err != nil {
		return CredentialImportResult{}, app_errors.ErrInternalServer
	}
	s.requestCredentialObservationRefresh()
	return result, nil
}

// ConnectGroupCredentials promotes ready subscription stages into an existing
// subscription Group without changing the API-key text import contract.
func (s *Service) ConnectGroupCredentials(
	ctx context.Context,
	groupID uint,
	stageIDs []string,
) (CredentialImportResult, error) {
	normalized, err := normalizeCredentialStageIDs(stageIDs)
	if groupID == 0 || err != nil {
		return CredentialImportResult{}, app_errors.ErrValidation
	}
	result := CredentialImportResult{GroupID: groupID}
	var entries []state.CredentialEntry
	err = s.writeCredentialConfig(ctx, groupID, 0, func(tx *gorm.DB) error {
		result, entries, err = s.connectGroupCredentialsMutation(ctx, tx, groupID, normalized)
		if err != nil {
			return err
		}
		return state.ValidateCredentialEntries(entries)
	}, func() error {
		_, reconcileErr := s.reconcileRegistryGroup(groupID, entries)
		return reconcileErr
	})
	if err != nil {
		return CredentialImportResult{}, err
	}
	s.requestCredentialObservationRefresh()
	return result, nil
}

func (s *Service) connectGroupCredentialsMutation(
	ctx context.Context,
	tx *gorm.DB,
	groupID uint,
	stageIDs []string,
) (CredentialImportResult, []state.CredentialEntry, error) {
	group, err := loadGroupRow(tx, groupID)
	if err != nil {
		return CredentialImportResult{}, nil, err
	}
	if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
		return CredentialImportResult{}, nil, app_errors.ErrValidation
	}
	added, err := s.consumeCredentialStages(
		tx, group.ID, channel.ID(group.ChannelID), group.ConnectionType, stageIDs,
	)
	if err != nil {
		return CredentialImportResult{}, nil, err
	}
	entries, err := stateloader.BuildGroupCredentialEntries(ctx, tx, groupID)
	if err != nil {
		return CredentialImportResult{}, nil, err
	}
	return CredentialImportResult{GroupID: groupID, CredentialsAdded: added}, entries, nil
}

// ReauthorizeGroupCredential replaces only the secret of the same logical
// subscription account. A different account must be connected separately.
func (s *Service) ReauthorizeGroupCredentialIdempotent(
	ctx context.Context,
	idempotencyKey string,
	groupID uint,
	credentialID uint,
	request CredentialReauthorizationRequest,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 || strings.TrimSpace(request.StageID) == "" || request.ExpectedSecretVersion == 0 {
		return CredentialItemResponse{}, app_errors.ErrValidation
	}
	canonicalBody, err := canonicalIdempotencyBody(credentialReauthorizationDigestBody{
		StageID:               strings.TrimSpace(request.StageID),
		ExpectedSecretVersion: request.ExpectedSecretVersion,
	})
	if err != nil {
		return CredentialItemResponse{}, app_errors.ErrInternalServer
	}
	resourceIdentity := "group:" + strconv.FormatUint(uint64(groupID), 10)
	digestResourceLocator := resourceIdentity +
		"/credential:" + strconv.FormatUint(uint64(credentialID), 10)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "POST", OperationKind: operationKindCredentialImport,
		PathTemplate:    "/api/groups/:group_id/credentials/:credential_id/reauthorize",
		ResourceLocator: digestResourceLocator, AuthScopeID: idempotencyAuthScopeID,
		CanonicalBody: canonicalBody,
	})
	if err != nil {
		return CredentialItemResponse{}, app_errors.ErrInternalServer
	}
	var operationResult idempotentOperationResult
	run := func() {
		operationResult, err = s.executeIdempotentOperation(ctx, idempotentOperationInput{
			IdempotencyKey: idempotencyKey, DigestVersion: 1, RequestDigest: digest.Digest,
			Kind: operationKindCredentialImport, CredentialMutationID: credentialID,
			Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
				secretVersion, entries, mutationErr := s.reauthorizeGroupCredentialMutation(ctx, tx, groupID, credentialID, request)
				if mutationErr != nil {
					return idempotentMutationResult{}, mutationErr
				}
				if validationErr := state.ValidateCredentialEntries(entries); validationErr != nil {
					return idempotentMutationResult{}, validationErr
				}
				canonicalResult, marshalErr := canonicaljson.Marshal(credentialReauthorizationResult{
					GroupID: groupID, CredentialID: credentialID, SecretVersion: secretVersion,
				})
				if marshalErr != nil {
					return idempotentMutationResult{}, app_errors.ErrInternalServer
				}
				return idempotentMutationResult{ResourceIdentity: resourceIdentity, CanonicalResult: canonicalResult}, nil
			},
		})
	}
	run()
	if err != nil {
		return CredentialItemResponse{}, err
	}
	var result credentialReauthorizationResult
	if err := json.Unmarshal(operationResult.CanonicalResult, &result); err != nil ||
		result.GroupID != groupID || result.CredentialID != credentialID || result.SecretVersion == 0 {
		return CredentialItemResponse{}, app_errors.ErrInternalServer
	}
	return s.loadCredentialItem(ctx, groupID, credentialID)
}

func (s *Service) ReauthorizeGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	request CredentialReauthorizationRequest,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 || strings.TrimSpace(request.StageID) == "" || request.ExpectedSecretVersion == 0 {
		return CredentialItemResponse{}, app_errors.ErrValidation
	}
	var entries []state.CredentialEntry
	err := s.writeCredentialConfig(ctx, groupID, credentialID, func(tx *gorm.DB) error {
		_, mutationEntries, err := s.reauthorizeGroupCredentialMutation(ctx, tx, groupID, credentialID, request)
		entries = mutationEntries
		if err != nil {
			return err
		}
		return state.ValidateCredentialEntries(entries)
	}, func() error {
		_, reconcileErr := s.reconcileRegistryGroup(groupID, entries)
		return reconcileErr
	})
	if err != nil {
		return CredentialItemResponse{}, err
	}
	return s.loadCredentialItem(ctx, groupID, credentialID)
}

func (s *Service) reauthorizeGroupCredentialMutation(
	ctx context.Context,
	tx *gorm.DB,
	groupID uint,
	credentialID uint,
	request CredentialReauthorizationRequest,
) (uint64, []state.CredentialEntry, error) {
	group, err := loadGroupRow(tx, groupID)
	if err != nil {
		return 0, nil, err
	}
	if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
		return 0, nil, app_errors.ErrValidation
	}
	var credential models.Credential
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND group_id = ?", credentialID, groupID).Take(&credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil, credentialNotFoundError()
		}
		return 0, nil, app_errors.ParseDBError(err)
	}
	if credential.SecretVersion != request.ExpectedSecretVersion {
		return 0, nil, app_errors.ErrCredentialVersionConflict
	}
	stage, canonical, err := s.readyStageCredential(tx, request.StageID, channel.ID(group.ChannelID))
	if err != nil {
		return 0, nil, err
	}
	defer clear(canonical)
	if stage.IdentityFingerprint != credential.IdentityFingerprint {
		return 0, nil, app_errors.ErrStagedCredentialMismatch
	}
	fingerprint := s.encryption.Hash(string(canonical))
	ciphertext, err := s.encryption.Encrypt(string(canonical))
	if err != nil {
		return 0, nil, app_errors.ErrInternalServer
	}
	nowMS := s.now().UnixMilli()
	secretVersion := request.ExpectedSecretVersion + 1
	updated := tx.Model(&models.Credential{}).
		Where("id = ? AND group_id = ? AND secret_version = ?", credentialID, groupID, request.ExpectedSecretVersion).
		Updates(map[string]any{
			"data": ciphertext, "fingerprint": fingerprint,
			"secret_version": secretVersion,
			"auth_state":     models.CredentialAuthStateReady, "auth_error_code": "",
			"updated_at_ms": nowMS,
		})
	if updated.Error != nil {
		return 0, nil, app_errors.ParseDBError(updated.Error)
	}
	if updated.RowsAffected != 1 {
		return 0, nil, app_errors.ErrCredentialVersionConflict
	}
	consumed := tx.Model(&models.CredentialStage{}).
		Where("id = ? AND status = ?", stage.ID, models.CredentialStageReady).
		Updates(map[string]any{
			"status": models.CredentialStageConsumed, "encrypted_payload": "",
			"oauth_state_hash": nil, "consumed_at_ms": nowMS,
			"consumed_group_id": groupID, "updated_at_ms": nowMS,
		})
	if consumed.Error != nil {
		return 0, nil, app_errors.ParseDBError(consumed.Error)
	}
	if consumed.RowsAffected != 1 {
		return 0, nil, app_errors.ErrStagedCredentialConsumed
	}
	entries, err := stateloader.BuildGroupCredentialEntries(ctx, tx, groupID)
	if err != nil {
		return 0, nil, err
	}
	return secretVersion, entries, nil
}

func (s *Service) readyStageCredential(
	tx *gorm.DB,
	stageID string,
	channelID channel.ID,
) (models.CredentialStage, []byte, error) {
	var stage models.CredentialStage
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&stage, "id = ?", stageID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialNotReady
		}
		return models.CredentialStage{}, nil, app_errors.ParseDBError(err)
	}
	if stage.Status != models.CredentialStageReady {
		if stage.Status == models.CredentialStageConsumed {
			return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialConsumed
		}
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialNotReady
	}
	if s.now().UnixMilli() >= stage.ExpiresAtMS {
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialExpired
	}
	if stage.ChannelID != string(channelID) || stage.ConnectionType != models.ConnectionTypeSubscription {
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	plaintext, err := s.encryption.Decrypt(stage.EncryptedPayload)
	if err != nil {
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		plaintext = ""
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	plaintext = ""
	driver, driverErr := s.subscriptionDriver(channelID)
	if driverErr != nil {
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	credential, err := driver.Parse(payload.Credential)
	if err != nil || s.subscriptionIdentityFingerprint(channelID, credential.Identity()) != stage.IdentityFingerprint {
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	canonical := credential.Canonical()
	if len(canonical) == 0 {
		clear(canonical)
		return models.CredentialStage{}, nil, app_errors.ErrStagedCredentialMismatch
	}
	return stage, canonical, nil
}
