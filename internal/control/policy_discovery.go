package control

import (
	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/response"
	"gpt-load/internal/policy"
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
}

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
	}
}

func (s *Server) handleGetPolicyDiscovery(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "get_policy_discovery") {
		return
	}
	result := s.service.GetPolicyDiscovery()
	response.SuccessI18n(c, "common.success", result)
}
