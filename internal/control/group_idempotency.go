package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type groupCreateDigestBody struct {
	Name                   *string             `json:"name"`
	UpstreamURL            string              `json:"upstream_url"`
	Protocols              []protocol.Protocol `json:"protocols"`
	Models                 []GroupModel        `json:"models"`
	Keys                   []string            `json:"keys"`
	ConfirmSameUpstreamURL bool                `json:"confirm_same_upstream_url"`
}

type groupKeyImportDigestBody struct {
	Keys []string `json:"keys"`
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
	keyLines, err := normalizeIdempotencyKeyLines(request.Keys)
	if err != nil {
		return GroupCreateResult{}, err
	}
	protocols := append([]protocol.Protocol(nil), normalized.protocols...)
	sort.Slice(protocols, func(left, right int) bool {
		return string(protocols[left]) < string(protocols[right])
	})
	canonicalBody, err := canonicalIdempotencyBody(groupCreateDigestBody{
		Name:                   normalized.explicitName,
		UpstreamURL:            normalized.upstreamURL,
		Protocols:              protocols,
		Models:                 append([]GroupModel(nil), normalized.models...),
		Keys:                   keyLines,
		ConfirmSameUpstreamURL: normalized.confirmSameUpstreamURL,
	})
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
			"Creating upstream group with a private or local host",
		)
	}

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindGroupCreate,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			if !normalized.confirmSameUpstreamURL {
				conflicts, err := findGroupsByUpstreamURL(tx, normalized.upstreamURL)
				if err != nil {
					return idempotentMutationResult{}, err
				}
				if len(conflicts) > 0 {
					return idempotentMutationResult{}, app_errors.NewAPIErrorWithData(
						app_errors.ErrUpstreamURLConflict,
						UpstreamURLConflictData{Groups: conflicts},
					)
				}
			}
			name, err := resolveGroupCreateName(
				tx,
				normalized.explicitName,
				normalized.hostname,
			)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			encodedProtocols, err := json.Marshal(normalized.protocols)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			encodedModels, err := json.Marshal(normalized.models)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			group := models.Group{
				Name:        name,
				UpstreamURL: normalized.upstreamURL,
				Protocols:   models.JSON(encodedProtocols),
				Models:      models.JSON(encodedModels),
				Config:      normalized.encodedConfig,
				Enabled:     true,
			}
			if err := tx.Create(&group).Error; err != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			entries, added, duplicated, err := s.persistUpstreamKeys(
				tx,
				group.ID,
				normalized.keys,
			)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if err := state.ValidateKeyEntries(entries); err != nil {
				return idempotentMutationResult{}, fmt.Errorf(
					"validate created group keys: %w",
					err,
				)
			}
			input, err := stateloader.BuildCompileInput(ctx, tx)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := state.Compile(input); err != nil {
				return idempotentMutationResult{}, err
			}
			result := GroupCreateResult{
				GroupID: group.ID, GroupName: group.Name,
				KeysAdded: added, KeysDuplicated: duplicated,
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
	return result, nil
}

func (s *Service) ImportGroupKeysIdempotent(
	ctx context.Context,
	idempotencyKey string,
	groupID uint,
	request GroupKeyImportRequest,
) (GroupKeyImportResult, error) {
	if groupID == 0 {
		return GroupKeyImportResult{}, app_errors.ErrValidation
	}
	keys, err := s.normalizeUpstreamKeys(request.Keys)
	if err != nil {
		return GroupKeyImportResult{}, err
	}
	keyLines, err := normalizeIdempotencyKeyLines(request.Keys)
	if err != nil {
		return GroupKeyImportResult{}, err
	}
	canonicalBody, err := canonicalIdempotencyBody(
		groupKeyImportDigestBody{Keys: keyLines},
	)
	if err != nil {
		return GroupKeyImportResult{}, app_errors.ErrInternalServer
	}
	resourceIdentity := "group:" + strconv.FormatUint(uint64(groupID), 10)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindGroupKeyImport,
		PathTemplate:    "/api/groups/:group_id/keys/import",
		ResourceLocator: resourceIdentity,
		AuthScopeID:     idempotencyAuthScopeID,
		CanonicalBody:   canonicalBody,
	})
	if err != nil {
		return GroupKeyImportResult{}, app_errors.ErrInternalServer
	}

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindGroupKeyImport,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			var group struct{ ID uint }
			if err := tx.Model(&models.Group{}).
				Select("id").
				Where("id = ?", groupID).
				Take(&group).Error; err != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			entries, added, duplicated, err := s.persistUpstreamKeys(
				tx,
				groupID,
				keys,
			)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if err := state.ValidateKeyEntries(entries); err != nil {
				return idempotentMutationResult{}, err
			}
			result := GroupKeyImportResult{
				GroupID: groupID, KeysAdded: added, KeysDuplicated: duplicated,
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
		return GroupKeyImportResult{}, err
	}
	var result GroupKeyImportResult
	if err := json.Unmarshal(operationResult.CanonicalResult, &result); err != nil {
		return GroupKeyImportResult{}, app_errors.ErrInternalServer
	}
	return result, nil
}
