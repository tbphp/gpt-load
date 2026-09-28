package control

import (
	"strconv"

	"gpt-load/internal/requestaudit"
)

type requestAuditResponse struct {
	Status   string                 `json:"status"`
	Outcome  string                 `json:"outcome"`
	Reason   string                 `json:"reason,omitempty"`
	Findings []requestaudit.Finding `json:"findings"`
	Calls    []auditCallResponse    `json:"calls"`
}

type auditCallResponse struct {
	Model                string `json:"model"`
	GroupName            string `json:"group_name"`
	Called               bool   `json:"called"`
	EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
	CostState            string `json:"cost_state"`
	PricingCompleteness  string `json:"pricing_completeness"`
}

func mapRequestAudit(value *requestaudit.Result) *requestAuditResponse {
	if value == nil {
		return nil
	}
	response := &requestAuditResponse{Status: value.Status, Reason: value.Reason, Findings: value.Findings, Calls: []auditCallResponse{}}
	if response.Status == "matched" {
		response.Status = "warned"
		if value.Mode == "enforce" {
			response.Status = "blocked"
		}
	}
	// 展示最终处置；内部覆盖状态和原因继续保留，不能把取样结果当作完整通过证明。
	response.Outcome = "failed"
	switch response.Status {
	case "passed":
		response.Outcome = "allowed"
	case "warned":
		response.Outcome = "warned"
	case "blocked":
		response.Outcome = "blocked"
	case "incomplete":
		if response.Reason == "content_truncated" {
			response.Outcome = "allowed"
		}
	}
	for _, call := range value.Calls {
		response.Calls = append(response.Calls, auditCallResponse{Model: call.UpstreamModel, GroupName: call.GroupName, Called: call.Called, EstimatedCostNanoUSD: strconv.FormatInt(call.EstimatedCostNanoUSD, 10), CostState: call.CostState, PricingCompleteness: call.PricingCompleteness})
	}
	return response
}
