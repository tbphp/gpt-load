package automodel

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"gpt-load/internal/reasoning"
	"gpt-load/internal/usage"
)

type Selection struct {
	EntryID            string          `json:"entry_id"`
	EntryName          string          `json:"entry_name"`
	PresetID           string          `json:"preset_id"`
	PresetName         string          `json:"preset_name"`
	TargetModel        string          `json:"target_model"`
	ParameterOverrides json.RawMessage `json:"parameter_overrides"`
	ConfigRevision     uint64          `json:"config_revision"`
	TaskFingerprint    string          `json:"task_fingerprint,omitempty"`
}

type Decision struct {
	PresetReasoning       reasoning.Config `json:"preset_reasoning"`
	AnswerFirstResponseMs *int64           `json:"answer_first_response_ms,omitempty"`
	Selection             Selection        `json:"selection"`
	Source                string           `json:"source"`
	Status                string           `json:"status"`
	Reason                string           `json:"reason,omitempty"`
	Provider              string           `json:"provider,omitempty"`
	GroupID               uint             `json:"group_id,omitempty"`
	GroupName             string           `json:"group_name,omitempty"`
	ChannelID             string           `json:"channel_id,omitempty"`
	ChannelName           string           `json:"channel_name,omitempty"`
	CredentialID          uint             `json:"credential_id,omitempty"`
	RequestedModel        string           `json:"requested_model,omitempty"`
	UpstreamModel         string           `json:"upstream_model,omitempty"`
	ReportedModel         string           `json:"reported_model,omitempty"`
	RequestID             string           `json:"request_id,omitempty"`
	PromptVersion         string           `json:"prompt_version"`
	ExecutionPhase        string           `json:"execution_phase,omitempty"`
	Choice                string           `json:"choice,omitempty"`
	Confidence            *float64         `json:"confidence,omitempty"`
	DurationMs            int64            `json:"duration_ms"`
	Called                bool             `json:"called"`
	ContextTruncated      bool             `json:"context_truncated"`
	InputTokens           *int64           `json:"input_tokens,omitempty"`
	OutputTokens          *int64           `json:"output_tokens,omitempty"`
	ProviderCostUSD       string           `json:"provider_cost_usd,omitempty"`
	CostState             string           `json:"cost_state"`
	PricingCompleteness   string           `json:"pricing_completeness"`
	EstimatedCostNanoUSD  int64            `json:"estimated_cost_nano_usd"`
	Receipt               json.RawMessage  `json:"receipt,omitempty"`
}

// BuildDecisionRequest creates the native Decisions payload and its initial
// fallback observation. Transport, scheduling and pricing remain in gateway.
func BuildDecisionRequest(compiled *Compiled, presets []CompiledPreset, state TaskState) ([]byte, Decision) {
	config := compiled.Config()
	decision := Decision{
		Source: "fallback", Status: "fallback", RequestedModel: config.Model,
		PromptVersion: PromptVersion, ExecutionPhase: state.ExecutionPhase,
		ContextTruncated: state.ContextTruncated,
		CostState:        "not_applicable", PricingCompleteness: "not_applicable",
	}
	criteria := make(map[string]string, len(presets))
	for _, preset := range presets {
		criteria[preset.ID] = preset.Description
	}
	payload, err := json.Marshal(map[string]any{
		"model": config.Model,
		"state": state,
		"questions": map[string]any{
			"preset": map[string]any{
				"type": "choice", "instructions": Instructions,
				"criteria": criteria,
			},
		},
	})
	if err != nil || len(payload) > MaxRequestBytes {
		decision.Reason = "request_too_large"
		return nil, decision
	}
	return payload, decision
}

// InterpretDecisionResponse applies only the automatic-model answer contract.
// The caller supplies normalized usage and later attaches the local quote.
func InterpretDecisionResponse(
	decision Decision,
	presets []CompiledPreset,
	statusCode int,
	header http.Header,
	body []byte,
	observed usage.Result,
) Decision {
	if observed.State == usage.StateComplete || observed.State == usage.StatePartial {
		input := observed.Tokens.UncachedInput
		decision.InputTokens = &input
		if observed.State == usage.StateComplete {
			output := observed.Tokens.Output
			decision.OutputTokens = &output
		}
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(body, &result) != nil {
		decision.Reason = "invalid_response"
		return decision
	}
	if reportedModel, ok := jsonString(result["model"]); ok && len(reportedModel) <= 255 {
		decision.ReportedModel = reportedModel
	}
	if requestID := strings.TrimSpace(header.Get("X-Request-Id")); len(requestID) <= 255 {
		decision.RequestID = requestID
	}
	decision.ProviderCostUSD = providerCost(result["usage"])
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		decision.Reason = "http_" + strconv.Itoa(statusCode)
		return decision
	}
	var answers map[string]json.RawMessage
	if json.Unmarshal(result["answers"], &answers) != nil {
		decision.Reason = "invalid_response"
		return decision
	}
	var answer struct {
		Choice     string   `json:"choice"`
		Confidence *float64 `json:"confidence"`
	}
	if json.Unmarshal(answers["preset"], &answer) != nil {
		decision.Reason = "invalid_answer"
		return decision
	}
	if len(answer.Choice) <= 255 {
		decision.Choice = answer.Choice
	}
	configured := false
	for _, preset := range presets {
		if preset.ID == answer.Choice {
			configured = true
			break
		}
	}
	if !configured {
		decision.Reason = "invalid_choice"
		return decision
	}
	if answer.Confidence == nil || math.IsNaN(*answer.Confidence) || math.IsInf(*answer.Confidence, 0) ||
		*answer.Confidence < 0 || *answer.Confidence > 1 {
		decision.Reason = "invalid_confidence"
		return decision
	}
	decision.Confidence = answer.Confidence
	decision.Source, decision.Status, decision.Reason = "jev", "selected", ""
	return decision
}

func jsonString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func providerCost(raw json.RawMessage) string {
	var value struct {
		Cost json.Number `json:"cost"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Cost == "" || len(value.Cost) > 128 {
		return ""
	}
	amount, err := strconv.ParseFloat(string(value.Cost), 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return ""
	}
	return string(value.Cost)
}
