package control

import (
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
)

// ModernCredentialItem 复用凭据读快照，仅补充配置来源，不改变经典 API 或调度逻辑。
type ModernCredentialItem struct {
	CredentialItemResponse
	WeightManual *int `json:"weight_manual"`
}

// 仅新版集合接口接受这些展示条件；经典接口仍使用原查询合同。
type modernCredentialFilters struct {
	sort          string
	proxy         string
	reset         string
	credentialKey string
}

func parseModernCredentialQuery(raw string) (CredentialCollectionQuery, *app_errors.APIError) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return CredentialCollectionQuery{}, app_errors.ErrBadRequest
	}
	filters := modernCredentialFilters{sort: "priority"}
	if entries, exists := values["credential_key"]; exists {
		if len(entries) != 1 {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		key := entries[0]
		if _, err := hex.DecodeString(key); err != nil || len(key) != 64 || key != strings.ToLower(key) {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		filters.credentialKey = key
		values.Del("credential_key")
	}
	for _, field := range []struct {
		name    string
		target  *string
		allowed []string
	}{
		{"sort", &filters.sort, []string{"priority", "newest", "oldest", "name", "weight_desc", "weight_asc", "failures"}},
		{"proxy", &filters.proxy, []string{"inherit", "direct", "custom"}},
		{"reset", &filters.reset, []string{"available", "none", "unknown"}},
	} {
		entries, exists := values[field.name]
		if !exists {
			continue
		}
		if len(entries) != 1 {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		valid := false
		for _, option := range field.allowed {
			if entries[0] == option {
				valid = true
				break
			}
		}
		if !valid {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		*field.target = entries[0]
		values.Del(field.name)
	}
	query, apiErr := parseCredentialCollectionQuery(values.Encode())
	if apiErr != nil {
		return CredentialCollectionQuery{}, apiErr
	}
	query.modern = &filters
	return query, nil
}

func matchesModernCredential(record credentialCollectionRecord, filters modernCredentialFilters) bool {
	if filters.credentialKey != "" && record.credentialKey != filters.credentialKey {
		return false
	}
	item := record.item
	if filters.proxy != "" && string(item.Proxy.ConfiguredMode) != filters.proxy {
		return false
	}
	if filters.reset == "" {
		return true
	}
	var credits *int64
	if item.Observation != nil && item.Observation.Snapshot != nil {
		credits = item.Observation.Snapshot.ResetCreditsAvailable
	}
	switch filters.reset {
	case "available":
		return credits != nil && *credits > 0
	case "none":
		return credits != nil && *credits == 0
	default:
		return credits == nil
	}
}

func modernCredentialLess(left, right credentialCollectionRecord, order string) bool {
	switch order {
	case "newest", "oldest":
		if left.createdAtMS != right.createdAtMS {
			if order == "newest" {
				return left.createdAtMS > right.createdAtMS
			}
			return left.createdAtMS < right.createdAtMS
		}
	case "name":
		name := func(item CredentialItemResponse) string {
			if item.Account.Email != "" {
				return strings.ToLower(item.Account.Email)
			}
			return strings.ToLower(item.Mask)
		}
		if a, b := name(left.item), name(right.item); a != b {
			return a < b
		}
	case "weight_desc", "weight_asc":
		if left.item.Weight != right.item.Weight {
			if order == "weight_desc" {
				return left.item.Weight > right.item.Weight
			}
			return left.item.Weight < right.item.Weight
		}
	case "failures":
		if left.item.RecentFailureCount != right.item.RecentFailureCount {
			return left.item.RecentFailureCount > right.item.RecentFailureCount
		}
	}
	return left.item.CredentialID < right.item.CredentialID
}

func (s *Server) handleListModernCredentials(c *gin.Context) {
	id, ok := groupID(c, "list_modern_credentials")
	if !ok {
		return
	}
	query, apiErr := parseModernCredentialQuery(c.Request.URL.RawQuery)
	if apiErr != nil {
		writeServiceError(c, "list_modern_credentials", apiErr)
		return
	}
	result, err := s.service.ListGroupCredentials(c.Request.Context(), id, query)
	if err != nil {
		writeServiceError(c, "list_modern_credentials", err)
		return
	}
	items := make([]ModernCredentialItem, 0, len(result.Items))
	ids := make([]uint, 0, len(result.Items))
	for _, item := range result.Items {
		ids = append(ids, item.CredentialID)
	}
	s.service.enrichCredentialActivityIDs(c.Request.Context(), result.Items, ids)
	for _, item := range result.Items {
		items = append(items, ModernCredentialItem{CredentialItemResponse: item, WeightManual: item.WeightManual})
	}
	response.SuccessI18n(c, "common.success", struct {
		CredentialCollectionResponse
		Items []ModernCredentialItem `json:"items"`
	}{CredentialCollectionResponse: result, Items: items})
}

func (s *Server) handleGetModernCredential(c *gin.Context) {
	group, ok := groupID(c, "get_modern_credential")
	if !ok {
		return
	}
	id, ok := credentialID(c, "get_modern_credential")
	if !ok {
		return
	}
	result, err := s.service.GetCredentialDetail(c.Request.Context(), group, id)
	if err != nil {
		writeServiceError(c, "get_modern_credential", err)
		return
	}
	// 经典详情已补充订阅账号活动；新版 API 密钥也展示同一来源的统计。
	if result.Credential.ConnectionType == "api_key" {
		items := []CredentialItemResponse{result.Credential}
		s.service.enrichCredentialActivityIDs(c.Request.Context(), items, []uint{id})
		result.Credential = items[0]
	}
	response.SuccessI18n(c, "common.success", struct {
		Credential  ModernCredentialItem          `json:"credential"`
		Observation CredentialObservationResponse `json:"observation"`
	}{
		Credential:  ModernCredentialItem{CredentialItemResponse: result.Credential, WeightManual: result.Credential.WeightManual},
		Observation: result.Observation,
	})
}
