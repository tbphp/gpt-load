package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type AccessKeyRotateResult struct {
	AccessKeyMetadata
	Key      string `json:"key,omitempty"`
	Replayed bool   `json:"replayed"`
}

func (s *Service) RotateAccessKeyIdempotent(
	ctx context.Context,
	idempotencyKey string,
	id uint,
) (AccessKeyRotateResult, error) {
	if id == 0 {
		return AccessKeyRotateResult{}, app_errors.ErrBadRequest
	}
	canonicalBody, err := canonicalIdempotencyBody(struct{}{})
	if err != nil {
		return AccessKeyRotateResult{}, app_errors.ErrInternalServer
	}
	resourceIdentity := fmt.Sprintf("access-key:%d", id)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindAccessKeyRotate,
		PathTemplate:    "/api/access-keys/:id/rotate",
		ResourceLocator: resourceIdentity,
		AuthScopeID:     idempotencyAuthScopeID,
		CanonicalBody:   canonicalBody,
	})
	if err != nil {
		return AccessKeyRotateResult{}, app_errors.ErrInternalServer
	}

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindAccessKeyRotate,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			row, err := loadAccessKeyMetadataRow(tx, id)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := mapAccessKeyMetadataRow(row); err != nil {
				return idempotentMutationResult{}, err
			}

			credential, err := s.generateAccessKeyCredential()
			if err != nil {
				return idempotentMutationResult{}, err
			}
			updated := tx.Model(&models.AccessKey{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"key_value":  credential.KeyValue,
					"key_hash":   credential.KeyHash,
					"key_suffix": credential.KeySuffix,
				})
			if updated.Error != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(updated.Error)
			}
			if updated.RowsAffected != 1 {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}

			row, err = loadAccessKeyMetadataRow(tx, id)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			metadata, err := mapAccessKeyMetadataRow(row)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			costLimitRows, err := loadAccessKeyCostLimitRuleRows(tx, id)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			metadata.CostLimitRules = mapAccessKeyCostLimitRules(costLimitRows)

			input, err := stateloader.BuildCompileInputWithProxy(
				ctx,
				tx,
				s.encryption,
				s.environmentProxy,
				s.channelRegistry,
			)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := state.Compile(input); err != nil {
				return idempotentMutationResult{}, err
			}
			canonicalResult, err := canonicaljson.Marshal(metadata)
			if err != nil {
				return idempotentMutationResult{}, fmt.Errorf(
					"encode AccessKey rotation result: %w",
					app_errors.ErrInternalServer,
				)
			}
			return idempotentMutationResult{
				ResourceIdentity: resourceIdentity,
				CanonicalResult:  canonicalResult,
				Ephemeral:        credential.Plaintext,
			}, nil
		},
	})
	if err != nil {
		return AccessKeyRotateResult{}, err
	}

	var metadata AccessKeyMetadata
	if err := json.Unmarshal(operationResult.CanonicalResult, &metadata); err != nil {
		return AccessKeyRotateResult{}, app_errors.ErrInternalServer
	}
	if metadata.CostLimitRules == nil {
		metadata.CostLimitRules = []AccessKeyCostLimitRule{}
	}
	if metadata.Filters.AllowedCIDRs == nil {
		metadata.Filters.AllowedCIDRs = []string{}
	}
	result := AccessKeyRotateResult{
		AccessKeyMetadata: metadata,
		Replayed:          operationResult.Replayed,
	}
	if plaintext, ok := operationResult.Ephemeral.(string); ok && !operationResult.Replayed {
		result.Key = plaintext
	}
	return result, nil
}

func loadAccessKeyMetadataRow(tx *gorm.DB, id uint) (accessKeyMetadataRow, error) {
	var row accessKeyMetadataRow
	if err := tx.Model(&models.AccessKey{}).
		Select(
			"id", "name", "key_suffix", "status", "filters", "rpm_limit",
			"expires_at_ms", "created_at_ms", "updated_at_ms",
		).
		Where("id = ?", id).
		Take(&row).Error; err != nil {
		return accessKeyMetadataRow{}, app_errors.ParseDBError(err)
	}
	return row, nil
}
