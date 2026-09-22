package jev

import "encoding/json"

// Observation records one internal Decisions call without its input or response body.
type Observation struct {
	Reason               string          `json:"reason,omitempty"`
	Provider             string          `json:"provider,omitempty"`
	GroupID              uint            `json:"group_id,omitempty"`
	GroupName            string          `json:"group_name,omitempty"`
	ChannelID            string          `json:"channel_id,omitempty"`
	ChannelName          string          `json:"channel_name,omitempty"`
	CredentialID         uint            `json:"credential_id,omitempty"`
	RequestedModel       string          `json:"requested_model,omitempty"`
	UpstreamModel        string          `json:"upstream_model,omitempty"`
	ReportedModel        string          `json:"reported_model,omitempty"`
	RequestID            string          `json:"request_id,omitempty"`
	DurationMs           int64           `json:"duration_ms"`
	Called               bool            `json:"called"`
	InputTokens          *int64          `json:"input_tokens,omitempty"`
	OutputTokens         *int64          `json:"output_tokens,omitempty"`
	ProviderCostUSD      string          `json:"provider_cost_usd,omitempty"`
	CostState            string          `json:"cost_state"`
	PricingCompleteness  string          `json:"pricing_completeness"`
	EstimatedCostNanoUSD int64           `json:"estimated_cost_nano_usd"`
	Receipt              json.RawMessage `json:"receipt,omitempty"`
}
