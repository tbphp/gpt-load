package automodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/pricing"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/usage"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

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
	RequestedModel        string           `json:"requested_model,omitempty"`
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

func Decide(ctx context.Context, client HTTPDoer, compiled *Compiled, presets []CompiledPreset, state TaskState, multiplier pricing.PriceMultiplier) Decision {
	config := compiled.Config()
	decision := Decision{Source: "fallback", Status: "fallback", Provider: config.Provider,
		RequestedModel: config.Model, PromptVersion: PromptVersion, ExecutionPhase: state.ExecutionPhase, ContextTruncated: state.ContextTruncated,
		CostState: "not_applicable", PricingCompleteness: "not_applicable"}
	criteria := map[string]string{}
	for _, preset := range presets {
		criteria[preset.ID] = preset.Description
	}
	payload, err := json.Marshal(map[string]any{"model": config.Model, "state": state, "questions": map[string]any{
		"preset": map[string]any{"type": "choice", "instructions": Instructions, "criteria": criteria},
	}})
	if err != nil || len(payload) > MaxRequestBytes {
		decision.Reason = "request_too_large"
		return decision
	}
	if ctx.Err() != nil {
		decision.Reason = "canceled"
		return decision
	}
	endpoint := "https://api.typesafe.ai/v1/systemone"
	if config.Provider == "openrouter" {
		endpoint = "https://openrouter.ai/api/alpha/decisions"
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		decision.Reason = "invalid_request"
		return decision
	}
	request.Header.Set("Authorization", "Bearer "+config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	decision.Called = true
	decision.CostState, decision.PricingCompleteness = "unpriced", "unavailable"
	started := time.Now()
	response, err := client.Do(request)
	decision.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		decision.Reason = "transport_error"
		if ctx.Err() != nil {
			decision.Reason = "canceled"
		} else if errors.Is(err, context.DeadlineExceeded) || callCtx.Err() != nil {
			decision.Reason = "timeout"
		}
		return decision
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	decision.DurationMs = time.Since(started).Milliseconds()
	if err != nil || len(body) > 1<<20 {
		decision.Reason = "invalid_response"
		return decision
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(body, &result) != nil {
		decision.Reason = "invalid_response"
		return decision
	}
	var reportedModel string
	if json.Unmarshal(result["model"], &reportedModel) == nil && len(reportedModel) <= 255 {
		decision.ReportedModel = maskDecisionSecret(reportedModel, config.APIKey)
	}
	if id := response.Header.Get("X-Request-Id"); len(id) <= 255 {
		decision.RequestID = maskDecisionSecret(id, config.APIKey)
	}
	// 用量独立解析，答案结构异常也不能丢掉已知的判断费用。
	decision.quote(compiled, result["usage"], multiplier)
	if response.StatusCode != 200 {
		decision.Reason = "http_" + strconv.Itoa(response.StatusCode)
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
		decision.Choice = maskDecisionSecret(answer.Choice, config.APIKey)
	}
	if _, exists := criteria[answer.Choice]; !exists {
		decision.Reason = "invalid_choice"
		return decision
	}
	if answer.Confidence == nil || math.IsNaN(*answer.Confidence) || math.IsInf(*answer.Confidence, 0) || *answer.Confidence < 0 || *answer.Confidence > 1 {
		decision.Reason = "invalid_confidence"
		return decision
	}
	decision.Confidence = answer.Confidence
	decision.Source, decision.Status = "jev", "selected"
	return decision
}

func maskDecisionSecret(value, key string) string {
	if key == "" {
		return value
	}
	return strings.ReplaceAll(value, key, "[REDACTED]")
}

func (decision *Decision) quote(compiled *Compiled, raw json.RawMessage, multiplier pricing.PriceMultiplier) {
	var result struct {
		Input  *int64      `json:"input_tokens"`
		Output *int64      `json:"output_tokens"`
		Cost   json.Number `json:"cost"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	if result.Cost != "" {
		if amount, err := strconv.ParseFloat(string(result.Cost), 64); err == nil && !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount >= 0 && len(result.Cost) <= 128 {
			decision.ProviderCostUSD = string(result.Cost)
		}
	}
	if result.Input == nil || *result.Input < 0 || (result.Output != nil && *result.Output < 0) {
		return
	}
	decision.InputTokens, decision.OutputTokens = result.Input, result.Output
	observed := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: *result.Input}}
	if result.Output != nil {
		observed.Tokens.Output = *result.Output
	} else {
		observed.State = usage.StatePartial
	}
	quote, receipt := compiled.Prices.QuoteForModeWithMultipliers(
		pricing.Identity{ChannelID: "jev-" + decision.Provider, ModelID: decision.RequestedModel}, observed, pricing.ModeStandard,
		pricing.PriceMultipliers{Group: pricing.DefaultPriceMultiplier, AccessKey: multiplier},
	)
	decision.CostState, decision.PricingCompleteness = string(quote.State), string(quote.Completeness)
	decision.EstimatedCostNanoUSD = int64(quote.EstimatedCostNanoUSD)
	if receipt != nil {
		decision.Receipt, _ = json.Marshal(receipt)
	}
}
