package control

import (
	"context"
	"errors"
	"sort"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/policy"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type PolicyCapabilitiesResponse struct {
	AccountWise        bool   `json:"account_wise"`
	GroupAggregation   bool   `json:"group_aggregation"`
	FixedRecovery      string `json:"fixed_recovery"`
	LiveDynamicPricing bool   `json:"live_dynamic_pricing"`
}

type PolicyDiscoveryResponse struct {
	Parameters   []policy.ParamDescriptor     `json:"parameters"`
	Predicates   []policy.PredicateDescriptor `json:"predicates"`
	Actions      []policy.ActionDescriptor    `json:"actions"`
	Capabilities PolicyCapabilitiesResponse   `json:"capabilities"`
	// QuotaWindows 为当前运行时快照中真实观测到的 account 窗口周期（秒，升序去重）；
	// 无观测事实时为空数组，绝不推断 provider 声明。
	QuotaWindows []int64 `json:"quota_windows"`
}

// GetPolicyDiscovery 返回全局发现元数据，调用签名保持不变；全局视图不携带观测窗口。
func (s *Service) GetPolicyDiscovery() PolicyDiscoveryResponse {
	return PolicyDiscoveryResponse{
		Parameters: policy.DefaultRegistry.ListParams(),
		Predicates: policy.DefaultRegistry.ListPredicates(),
		Actions:    policy.DefaultRegistry.ListActions(),
		Capabilities: PolicyCapabilitiesResponse{
			AccountWise:        true,
			GroupAggregation:   false,
			FixedRecovery:      "unsupported",
			LiveDynamicPricing: false,
		},
		QuotaWindows: []int64{},
	}
}

// GetScopedPolicyDiscovery 纯读返回分组（或分组内单个凭据）观测到的 account 窗口周期，
// 仅校验分组存在与凭据归属；窗口取自运行时快照，刚删除的短时条目可能仍并入，不影响安全。
func (s *Service) GetScopedPolicyDiscovery(
	ctx context.Context,
	groupID uint,
	credentialID *uint,
) (PolicyDiscoveryResponse, error) {
	if groupID == 0 {
		return PolicyDiscoveryResponse{}, app_errors.ErrBadRequest
	}
	if credentialID != nil && *credentialID == 0 {
		return PolicyDiscoveryResponse{}, app_errors.ErrBadRequest
	}

	if _, err := loadGroupRow(s.db.WithContext(ctx), groupID); err != nil {
		return PolicyDiscoveryResponse{}, err
	}
	if credentialID != nil {
		var credential models.Credential
		err := s.db.WithContext(ctx).Select("id").
			Where("group_id = ? AND id = ?", groupID, *credentialID).
			Take(&credential).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PolicyDiscoveryResponse{}, credentialNotFoundError()
		}
		if err != nil {
			return PolicyDiscoveryResponse{}, app_errors.ParseDBError(err)
		}
	}

	result := s.GetPolicyDiscovery()
	var scopedCredentialID uint
	if credentialID != nil {
		scopedCredentialID = *credentialID
	}
	result.QuotaWindows = observedAccountQuotaWindows(s.registry.SnapshotForScope(groupID, scopedCredentialID), groupID, credentialID)
	return result, nil
}

// observedAccountQuotaWindows 从运行时快照提取 account 窗口周期（秒，升序去重）。
// 仅保留正周期且 IdentityGeneration 与条目一致的事实，跳过模型专属与过期身份。
func observedAccountQuotaWindows(
	views []state.CredentialRuntimeView,
	groupID uint,
	credentialID *uint,
) []int64 {
	seen := make(map[int64]struct{})
	for _, view := range views {
		if view.GroupID != groupID {
			continue
		}
		if credentialID != nil && view.ID != *credentialID {
			continue
		}
		for _, fact := range view.QuotaWindows {
			if fact.Scope != "account" || fact.WindowSeconds <= 0 {
				continue
			}
			if fact.IdentityGeneration != view.IdentityGeneration {
				continue
			}
			seen[int64(fact.WindowSeconds)] = struct{}{}
		}
	}
	windows := make([]int64, 0, len(seen))
	for window := range seen {
		windows = append(windows, window)
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i] < windows[j] })
	return windows
}

type policyDiscoveryQuery struct {
	groupID      uint
	credentialID *uint
}

// parsePolicyDiscoveryQuery 解析可选作用域查询参数；credential_id 必须与 group_id 同时给出。
func parsePolicyDiscoveryQuery(c *gin.Context) (policyDiscoveryQuery, *app_errors.APIError) {
	var query policyDiscoveryQuery
	rawGroup := c.Query("group_id")
	rawCredential := c.Query("credential_id")
	if rawCredential != "" && rawGroup == "" {
		return policyDiscoveryQuery{}, app_errors.ErrBadRequest
	}
	if rawGroup == "" {
		return query, nil
	}
	groupID, err := parseCanonicalSafePlatformUint(rawGroup)
	if err != nil || groupID == 0 {
		return policyDiscoveryQuery{}, app_errors.ErrBadRequest
	}
	query.groupID = groupID
	if rawCredential == "" {
		return query, nil
	}
	credentialID, err := parseCanonicalSafePlatformUint(rawCredential)
	if err != nil || credentialID == 0 {
		return policyDiscoveryQuery{}, app_errors.ErrBadRequest
	}
	query.credentialID = &credentialID
	return query, nil
}

func (s *Server) handleGetPolicyDiscovery(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "get_policy_discovery") {
		return
	}
	query, apiErr := parsePolicyDiscoveryQuery(c)
	if apiErr != nil {
		writeServiceError(c, "get_policy_discovery", apiErr)
		return
	}
	if query.groupID == 0 {
		response.SuccessI18n(c, "common.success", s.service.GetPolicyDiscovery())
		return
	}
	result, err := s.service.GetScopedPolicyDiscovery(c.Request.Context(), query.groupID, query.credentialID)
	if err != nil {
		writeServiceError(c, "get_policy_discovery", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
