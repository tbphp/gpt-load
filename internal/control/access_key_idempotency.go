package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

type accessKeyCreateDigestBody struct {
	Name     string                 `json:"name"`
	Status   *state.AccessKeyStatus `json:"status,omitempty"`
	Filters  AccessKeyFilters       `json:"filters"`
	RPMLimit int64                  `json:"rpm_limit"`
}

func (s *Service) CreateAccessKeyIdempotent(
	ctx context.Context,
	idempotencyKey string,
	request AccessKeyCreateRequest,
) (AccessKeyCreateResult, error) {
	name, err := normalizeAccessKeyName(request.Name)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	filters, err := normalizeAccessKeyFilters(request.Filters)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	rpmLimit, err := normalizeRPMLimit(request.RPMLimit, 0)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	status := state.AccessKeyStatusActive
	if request.Status != nil {
		status = *request.Status
	}
	if status != state.AccessKeyStatusActive && status != state.AccessKeyStatusDisabled {
		return AccessKeyCreateResult{}, app_errors.ErrValidation
	}
	var digestStatus *state.AccessKeyStatus
	if status != state.AccessKeyStatusActive {
		digestStatus = &status
	}
	digestFilters := canonicalAccessKeyFilterSet(filters)
	canonicalBody, err := canonicalIdempotencyBody(accessKeyCreateDigestBody{
		Name: name, Status: digestStatus, Filters: digestFilters, RPMLimit: rpmLimit,
	})
	if err != nil {
		return AccessKeyCreateResult{}, app_errors.ErrInternalServer
	}
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version:         1,
		Method:          "POST",
		OperationKind:   operationKindAccessKeyCreate,
		PathTemplate:    "/api/access-keys",
		ResourceLocator: "new",
		AuthScopeID:     idempotencyAuthScopeID,
		CanonicalBody:   canonicalBody,
	})
	if err != nil {
		return AccessKeyCreateResult{}, app_errors.ErrInternalServer
	}

	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindAccessKeyCreate,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			if err := validateFilterGroupReferences(tx, filters.Groups); err != nil {
				return idempotentMutationResult{}, err
			}
			row, plaintext, err := s.newAccessKeyRow(name, filters, rpmLimit)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			row.Status = string(status)
			if err := tx.Create(&row).Error; err != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
				ID: row.ID, Name: row.Name, KeySuffix: row.KeySuffix,
				Status: row.Status, Filters: row.Filters, RPMLimit: row.RPMLimit,
				CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
			})
			if err != nil {
				return idempotentMutationResult{}, err
			}
			input, err := stateloader.BuildCompileInput(ctx, tx)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := state.Compile(input); err != nil {
				return idempotentMutationResult{}, err
			}
			canonicalResult, err := canonicaljson.Marshal(metadata)
			if err != nil {
				return idempotentMutationResult{}, fmt.Errorf(
					"encode AccessKey operation result: %w",
					app_errors.ErrInternalServer,
				)
			}
			return idempotentMutationResult{
				ResourceIdentity: fmt.Sprintf("access-key:%d", row.ID),
				CanonicalResult:  canonicalResult,
				Ephemeral:        plaintext,
			}, nil
		},
	})
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	var metadata AccessKeyMetadata
	if err := json.Unmarshal(operationResult.CanonicalResult, &metadata); err != nil {
		return AccessKeyCreateResult{}, app_errors.ErrInternalServer
	}
	result := AccessKeyCreateResult{
		AccessKeyMetadata: metadata,
		Replayed:          operationResult.Replayed,
	}
	if plaintext, ok := operationResult.Ephemeral.(string); ok &&
		!operationResult.Replayed {
		result.Key = plaintext
	}
	return result, nil
}

func canonicalAccessKeyFilterSet(filters AccessKeyFilters) AccessKeyFilters {
	result := AccessKeyFilters{
		Groups:    append([]uint(nil), filters.Groups...),
		Protocols: append([]protocol.Protocol(nil), filters.Protocols...),
		Models:    append([]string(nil), filters.Models...),
	}
	sort.Slice(result.Groups, func(left, right int) bool {
		return result.Groups[left] < result.Groups[right]
	})
	sort.Slice(result.Protocols, func(left, right int) bool {
		return string(result.Protocols[left]) < string(result.Protocols[right])
	})
	sort.Strings(result.Models)
	return result
}
