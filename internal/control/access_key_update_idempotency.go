package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

// UpdateAccessKeyIdempotent 将密钥与配置更新作为一次操作，重试只恢复原操作。
func (s *Service) UpdateAccessKeyIdempotent(ctx context.Context, idempotencyKey string, id uint, request AccessKeyUpdateRequest) (AccessKeyMetadata, error) {
	if request.Key == "" {
		return AccessKeyMetadata{}, app_errors.ErrBadRequest
	}
	mutate, err := s.accessKeyUpdateMutation(id, request)
	if err != nil {
		return AccessKeyMetadata{}, err
	}
	digestRequest := request
	digestRequest.Key = ""
	if request.Name != nil {
		name, err := normalizeAccessKeyName(*request.Name)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		digestRequest.Name = &name
	}
	if request.PriceMultiplier.Set {
		multiplier, err := normalizePriceMultiplier(request.PriceMultiplier)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		digestRequest.PriceMultiplier.Value = pricing.FormatPriceMultiplier(multiplier)
	}
	if request.CostLimitRules.Set {
		rules, err := normalizeAccessKeyCostLimitRules(request.CostLimitRules, true)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		digestRequest.CostLimitRules.Values = costLimitRuleRequestsForDigest(rules)
		// 编辑摘要必须保留规则 ID，区分保留既有规则与创建新规则。
		for index, rule := range rules {
			digestRequest.CostLimitRules.Values[index].ID = rule.ID
		}
	}
	if request.Filters != nil {
		filters, err := normalizeAccessKeyFilters(request.Filters)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		canonicalFilters := canonicalAccessKeyFilterSet(filters)
		digestRequest.Filters = &AccessKeyFilters{
			Groups: canonicalFilters.Groups, Protocols: canonicalFilters.Protocols,
			Models: canonicalFilters.Models, AllowedCIDRs: canonicalFilters.AllowedCIDRs,
		}
	}
	canonicalBody, err := canonicalIdempotencyBody(struct {
		Request AccessKeyUpdateRequest `json:"request"`
		KeyHash string                 `json:"key_hash"`
	}{Request: digestRequest, KeyHash: s.encryption.Hash(request.Key)})
	if err != nil {
		return AccessKeyMetadata{}, app_errors.ErrInternalServer
	}
	identity := fmt.Sprintf("access-key:%d", id)
	digest, err := buildIdempotencyDigest(idempotencyDigestInput{
		Version: 1, Method: "PUT", OperationKind: operationKindAccessKeyUpdate,
		PathTemplate: "/api/access-keys/:id", ResourceLocator: identity,
		AuthScopeID: idempotencyAuthScopeID, CanonicalBody: canonicalBody,
	})
	if err != nil {
		return AccessKeyMetadata{}, app_errors.ErrInternalServer
	}
	operation, err := s.executeIdempotentOperation(ctx, idempotentOperationInput{
		IdempotencyKey: idempotencyKey, DigestVersion: 1, RequestDigest: digest.Digest,
		Kind: operationKindAccessKeyUpdate,
		Mutate: func(tx *gorm.DB) (idempotentMutationResult, error) {
			metadata, err := mutate(tx)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			input, err := stateloader.BuildCompileInputWithProxy(ctx, tx, s.encryption, s.environmentProxy, s.channelRegistry)
			if err != nil {
				return idempotentMutationResult{}, err
			}
			if _, err := state.Compile(input); err != nil {
				return idempotentMutationResult{}, err
			}
			result, err := canonicaljson.Marshal(metadata)
			if err != nil {
				return idempotentMutationResult{}, app_errors.ErrInternalServer
			}
			return idempotentMutationResult{ResourceIdentity: identity, CanonicalResult: result}, nil
		},
	})
	if err != nil {
		return AccessKeyMetadata{}, err
	}
	var result AccessKeyMetadata
	if err := json.Unmarshal(operation.CanonicalResult, &result); err != nil {
		return AccessKeyMetadata{}, app_errors.ErrInternalServer
	}
	return result, nil
}
