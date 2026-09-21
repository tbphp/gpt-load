package control

import (
	"strconv"

	"gpt-load/internal/requestaudit"
)

type requestAuditResponse struct {
	Mode       string                 `json:"mode"`
	Status     string                 `json:"status"`
	Reason     string                 `json:"reason,omitempty"`
	Checks     int                    `json:"checks"`
	DurationMs int64                  `json:"duration_ms"`
	Findings   []requestaudit.Finding `json:"findings"`
	Calls      []auditCallResponse    `json:"calls"`
}
type auditCallResponse struct {
	Model                string `json:"model"`
	GroupName            string `json:"group_name"`
	Reason               string `json:"reason,omitempty"`
	Called               bool   `json:"called"`
	DurationMs           int64  `json:"duration_ms"`
	EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
	CostState            string `json:"cost_state"`
	PricingCompleteness  string `json:"pricing_completeness"`
}

func mapRequestAudit(value *requestaudit.Result) *requestAuditResponse {
	if value == nil {
		return nil
	}
	response := &requestAuditResponse{Mode: value.Mode, Status: value.Status, Reason: value.Reason, Checks: value.Checks, DurationMs: value.DurationMs, Findings: value.Findings, Calls: []auditCallResponse{}}
	for _, call := range value.Calls {
		response.Calls = append(response.Calls, auditCallResponse{Model: call.UpstreamModel, GroupName: call.GroupName, Reason: call.Reason, Called: call.Called, DurationMs: call.DurationMs, EstimatedCostNanoUSD: strconv.FormatInt(call.EstimatedCostNanoUSD, 10), CostState: call.CostState, PricingCompleteness: call.PricingCompleteness})
	}
	return response
}
