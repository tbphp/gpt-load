package control

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

type accessKeyFilterDigestBody struct {
	Groups       []uint              `json:"groups"`
	Protocols    []protocol.Protocol `json:"protocols"`
	Models       []string            `json:"models"`
	AllowedCIDRs []string            `json:"allowed_cidrs,omitempty"`
}

type accessKeyCreateDigestBody struct {
	Name           string                          `json:"name"`
	Status         *state.AccessKeyStatus          `json:"status,omitempty"`
	Filters        accessKeyFilterDigestBody       `json:"filters"`
	RPMLimit       int64                           `json:"rpm_limit"`
	CostLimitRules []AccessKeyCostLimitRuleRequest `json:"cost_limit_rules,omitempty"`
	ExpiresAtMS    *int64                          `json:"expires_at_ms,omitempty"`
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
	costLimitRules, err := normalizeAccessKeyCostLimitRules(request.CostLimitRules, false)
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
	if err := validateOptionalExpiresAtMS(request.ExpiresAtMS); err != nil {
		return AccessKeyCreateResult{}, err
	}
	var digestStatus *state.AccessKeyStatus
	if status != state.AccessKeyStatusActive {
		digestStatus = &status
	}
	digestFilters := canonicalAccessKeyFilterSet(filters)
	canonicalBody, err := canonicalIdempotencyBody(accessKeyCreateDigestBody{
		Name: name, Status: digestStatus, Filters: digestFilters, RPMLimit: rpmLimit,
		CostLimitRules: costLimitRuleRequestsForDigest(costLimitRules),
		ExpiresAtMS:    request.ExpiresAtMS,
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

	var operationStartedAt time.Time
	operationResult, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey,
		DigestVersion:  1,
		RequestDigest:  digest.Digest,
		Kind:           operationKindAccessKeyCreate,
		PrepareMutation: func() {
			operationStartedAt = s.now()
		},
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			if err := validateFutureExpiresAtMS(request.ExpiresAtMS, operationStartedAt); err != nil {
				return idempotentMutationResult{}, err
			}
			if err := validateFilterGroupReferences(tx, filters.Groups); err != nil {
				return idempotentMutationResult{}, err
			}
			row, plaintext, err := s.newAccessKeyRow(name, filters, rpmLimit)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			row.Status = string(status)
			row.ExpiresAtMS = cloneOptionalInt64(request.ExpiresAtMS)
			if err := tx.Create(&row).Error; err != nil {
				return idempotentMutationResult{}, app_errors.ParseDBError(err)
			}
			persistedCostLimitRules, err := createAccessKeyCostLimitRules(tx, row.ID, costLimitRules)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
				ID: row.ID, Name: row.Name, KeySuffix: row.KeySuffix,
				Status: row.Status, Filters: row.Filters, RPMLimit: row.RPMLimit,
				ExpiresAtMS: row.ExpiresAtMS,
				CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
			})
			if err != nil {
				return idempotentMutationResult{}, err
			}
			metadata.CostLimitRules = mapAccessKeyCostLimitRules(persistedCostLimitRules)
			input, err := stateloader.BuildCompileInputWithProxy(
				ctx, tx, s.encryption, s.environmentProxy, s.channelRegistry,
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
	if metadata.CostLimitRules == nil {
		// Pre-0002 idempotency results did not carry this additive field. Preserve
		// replay compatibility while keeping the current wire contract array-shaped.
		metadata.CostLimitRules = []AccessKeyCostLimitRule{}
	}
	if metadata.Filters.AllowedCIDRs == nil {
		// Pre-0007 operation results did not carry this additive field. Preserve
		// replay compatibility while keeping the current wire contract array-shaped.
		metadata.Filters.AllowedCIDRs = []string{}
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

func canonicalAccessKeyFilterSet(filters AccessKeyFilters) accessKeyFilterDigestBody {
	result := accessKeyFilterDigestBody{
		Groups:       append([]uint(nil), filters.Groups...),
		Protocols:    append([]protocol.Protocol(nil), filters.Protocols...),
		Models:       append([]string(nil), filters.Models...),
		AllowedCIDRs: append([]string(nil), filters.AllowedCIDRs...),
	}
	sort.Slice(result.Groups, func(left, right int) bool {
		return result.Groups[left] < result.Groups[right]
	})
	sort.Slice(result.Protocols, func(left, right int) bool {
		return string(result.Protocols[left]) < string(result.Protocols[right])
	})
	sort.Strings(result.Models)
	sort.Strings(result.AllowedCIDRs)
	return result
}
