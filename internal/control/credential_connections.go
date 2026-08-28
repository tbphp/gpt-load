package control

import (
	"context"
	"encoding/json"
	"strconv"

	"gorm.io/gorm"

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

type CredentialConnectInspection struct {
	DuplicatedStageIDs []string `json:"duplicated_stage_ids"`
}

type credentialConnectDigestBody struct {
	StagedCredentialIDs []string `json:"staged_credential_ids"`
}

// InspectGroupCredentialConnection identifies ready stages that the final
// connection will skip because their subscription identity is already present.
func (s *Service) InspectGroupCredentialConnection(
	ctx context.Context,
	groupID uint,
	stageIDs []string,
) (CredentialConnectInspection, error) {
	normalized, err := normalizeCredentialStageIDs(stageIDs)
	if groupID == 0 || err != nil {
		return CredentialConnectInspection{}, app_errors.ErrValidation
	}
	db := s.db.WithContext(ctx)
	group, err := loadGroupRow(db, groupID)
	if err != nil {
		return CredentialConnectInspection{}, err
	}
	if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
		return CredentialConnectInspection{}, app_errors.ErrValidation
	}
	stages, err := s.loadConsumableCredentialStages(
		db, channel.ID(group.ChannelID), group.ConnectionType, normalized, false,
	)
	if err != nil {
		return CredentialConnectInspection{}, err
	}
	duplicatedStageIDs, _, err := classifyCredentialStages(db, groupID, stages)
	if err != nil {
		return CredentialConnectInspection{}, err
	}
	return CredentialConnectInspection{DuplicatedStageIDs: duplicatedStageIDs}, nil
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
	added, duplicatedStageIDs, err := s.consumeCredentialStages(
		tx, group.ID, channel.ID(group.ChannelID), group.ConnectionType, stageIDs,
	)
	if err != nil {
		return CredentialImportResult{}, nil, err
	}
	entries, err := stateloader.BuildGroupCredentialEntriesWithProxy(ctx, tx, groupID, s.encryption)
	if err != nil {
		return CredentialImportResult{}, nil, err
	}
	return CredentialImportResult{
		GroupID: groupID, CredentialsAdded: added,
		CredentialsDuplicated: len(duplicatedStageIDs),
	}, entries, nil
}
