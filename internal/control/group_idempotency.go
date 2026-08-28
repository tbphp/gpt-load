package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type groupCreateDigestBody struct {
	Name                *string               `json:"name"`
	ChannelID           channel.ID            `json:"channel_id"`
	ConnectionType      models.ConnectionType `json:"connection_type"`
	Params              json.RawMessage       `json:"params"`
	Models              []GroupModel          `json:"models"`
	Credentials         []string              `json:"credentials"`
	StagedCredentialIDs []string              `json:"staged_credential_ids,omitempty"`
	ConfirmSameTarget   bool                  `json:"confirm_same_target,omitempty"`
	Proxy               *outboundproxy.Config `json:"proxy,omitempty"`
}

type credentialImportDigestBody struct {
	Credentials []string `json:"credentials"`
}

func (s *Service) CreateGroupIdempotent(
	ctx context.Context,
	idempotencyKey string,
	request GroupCreateRequest,
) (GroupCreateResult, error) {
	normalized, err := s.normalizeGroupCreate(ctx, request)
	if err != nil {
		return GroupCreateResult{}, err
	}
	credentialLines := []string(nil)
	if normalized.connectionType == models.ConnectionTypeAPIKey {
		credentialLines, err = normalizeIdempotencyKeyLines(request.Credentials)
		if err != nil {
			return GroupCreateResult{}, err
		}
	}
	digestBody := groupCreateDigestBody{
		Name:                normalized.explicitName,
		ChannelID:           normalized.channelID,
		ConnectionType:      normalized.connectionType,
		Params:              append(json.RawMessage(nil), normalized.params...),
		Models:              append([]GroupModel(nil), normalized.models...),
		Credentials:         credentialLines,
		StagedCredentialIDs: append([]string(nil), normalized.stagedCredentialIDs...),
		ConfirmSameTarget:   normalized.confirmSameTarget,
		Proxy:               normalized.proxy,
	}
	canonicalBody, err := canonicalIdempotencyBody(digestBody)
	if err != nil {
		return GroupCreateResult{}, app_errors.ErrInternalServer
	}
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindGroupCreate,
		PathTemplate:    "/api/groups",
		ResourceLocator: "new",
		AuthScopeID:     idempotencyAuthScopeID,
		CanonicalBody:   canonicalBody,
	})
	if err != nil {
		return GroupCreateResult{}, app_errors.ErrInternalServer
	}
	if isLiteralPrivateHost(normalized.hostname) {
		utils.LogPlaneBestEffort(
			logrus.StandardLogger(),
			logrus.WarnLevel,
			utils.LogPlaneControl,
			logrus.Fields{"host": normalized.hostname},
			"Creating channel group with a private or local host",
		)
	}
	var catalogSnapshot *catalog.Snapshot

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindGroupCreate,
		PrepareMutation: func() {
			if s.catalogRuntime != nil {
				catalogSnapshot = s.catalogRuntime.Load()
			}
		},
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			if !normalized.confirmSameTarget {
				conflicts, err := findGroupsByTarget(tx, normalized.channelID, normalized.connectionType, normalized.params)
				if err != nil {
					return idempotentMutationResult{}, err
				}
				if len(conflicts) > 0 {
					return idempotentMutationResult{}, app_errors.NewAPIErrorWithData(
						app_errors.ErrChannelTargetConflict,
						SameTargetConflictData{Groups: conflicts},
					)
				}
			}
			name, err := resolveGroupCreateName(tx, normalized.explicitName, normalized.defaultName)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			encodedModels, err := json.Marshal(normalized.models)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			group := models.Group{
				Name:           name,
				ChannelID:      string(normalized.channelID),
				ConnectionType: normalized.connectionType,
				Params:         append(models.JSON(nil), normalized.params...),
				Models:         models.JSON(encodedModels),
				Overrides:      normalized.encodedOverrides,
				ProxyConfig:    normalized.proxyConfig,
				Enabled:        true,
			}
			if err := tx.Create(&group).Error; err != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			added := 0
			duplicated := 0
			if normalized.connectionType == models.ConnectionTypeSubscription {
				var duplicatedStageIDs []string
				added, duplicatedStageIDs, err = s.consumeCredentialStages(
					tx,
					group.ID,
					normalized.channelID,
					normalized.connectionType,
					normalized.stagedCredentialIDs,
				)
				duplicated = len(duplicatedStageIDs)
			} else {
				added, duplicated, err = s.persistCredentials(tx, group.ID, normalized.credentials)
			}
			if err != nil {
				return idempotentMutationResult{}, err
			}
			entries, err := stateloader.BuildGroupCredentialEntriesWithProxy(ctx, tx, group.ID, s.encryption)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if err := state.ValidateCredentialEntries(entries); err != nil {
				return idempotentMutationResult{}, err
			}
			if err := reconcileReferencedPrices(tx, catalogSnapshot); err != nil {
				return idempotentMutationResult{}, err
			}
			input, err := stateloader.BuildCompileInputWithProxy(
				ctx, tx, s.encryption, s.environmentProxy, s.channelRegistry,
			)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := state.Compile(input); err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := loadPriceTable(ctx, tx); err != nil {
				return idempotentMutationResult{}, err
			}
			result := GroupCreateResult{
				GroupID:               group.ID,
				GroupName:             group.Name,
				CredentialsAdded:      added,
				CredentialsDuplicated: duplicated,
			}
			canonicalResult, err := canonicaljson.Marshal(result)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			return idempotentMutationResult{
				ResourceIdentity: "group:" + strconv.FormatUint(uint64(group.ID), 10),
				CanonicalResult:  canonicalResult,
			}, nil
		},
	})
	if err != nil {
		return GroupCreateResult{}, err
	}
	var result GroupCreateResult
	if err := json.Unmarshal(operationResult.CanonicalResult, &result); err != nil {
		return GroupCreateResult{}, app_errors.ErrInternalServer
	}
	if !operationResult.Replayed && len(normalized.models) > 0 && s.catalogSync != nil {
		s.catalogSync.RequestGroupSync()
	}
	return result, nil
}

func (s *Service) ImportGroupCredentialsIdempotent(
	ctx context.Context,
	idempotencyKey string,
	groupID uint,
	request CredentialImportRequest,
) (CredentialImportResult, error) {
	if groupID == 0 {
		return CredentialImportResult{}, app_errors.ErrValidation
	}
	credentialLines, err := normalizeIdempotencyKeyLines(request.Credentials)
	if err != nil {
		return CredentialImportResult{}, err
	}
	canonicalBody, err := canonicalIdempotencyBody(credentialImportDigestBody{Credentials: credentialLines})
	if err != nil {
		return CredentialImportResult{}, app_errors.ErrInternalServer
	}
	resourceIdentity := "group:" + strconv.FormatUint(uint64(groupID), 10)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindCredentialImport,
		PathTemplate:    "/api/groups/:group_id/credentials/import",
		ResourceLocator: resourceIdentity,
		AuthScopeID:     idempotencyAuthScopeID,
		CanonicalBody:   canonicalBody,
	})
	if err != nil {
		return CredentialImportResult{}, app_errors.ErrInternalServer
	}

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindCredentialImport,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			var group models.Group
			if err := tx.Select("id", "channel_id", "connection_type").Where("id = ?", groupID).Take(&group).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return idempotentMutationResult{}, groupNotFoundError()
				}
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeAPIKey {
				return idempotentMutationResult{}, app_errors.ErrValidation
			}
			normalized, err := s.normalizeCredentials(channel.ID(group.ChannelID), request.Credentials)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			added, duplicated, err := s.persistCredentials(tx, groupID, normalized)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			entries, err := stateloader.BuildGroupCredentialEntriesWithProxy(ctx, tx, groupID, s.encryption)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if err := state.ValidateCredentialEntries(entries); err != nil {
				return idempotentMutationResult{}, err
			}
			result := CredentialImportResult{
				GroupID:               groupID,
				CredentialsAdded:      added,
				CredentialsDuplicated: duplicated,
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
