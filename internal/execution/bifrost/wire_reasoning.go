package bifrost

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/reasoning"
)

const maxAppliedReasoningValueBytes = 64

// enableConvertedWireCapture asks Bifrost to return the final serialized
// provider request only when a converted typed request carries reasoning. The
// caller must clear RawRequest immediately after extracting the bounded result.
func enableConvertedWireCapture(
	ctx *schemas.BifrostContext,
	prepared preparedAttempt,
) {
	if ctx == nil || prepared.mode != channel.RouteConverted || !preparedReasoningPresent(prepared) {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyAllowPerRequestRawOverride, true)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawRequest, true)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawResponse, false)
}

func preparedReasoningPresent(prepared preparedAttempt) bool {
	if prepared.request != nil && prepared.request.Params != nil && prepared.request.Params.Reasoning != nil {
		return true
	}
	return prepared.responsesRequest != nil && prepared.responsesRequest.Params != nil &&
		prepared.responsesRequest.Params.Reasoning != nil
}

// takeAppliedReasoning clears the sensitive raw request before parsing it and
// returns only fields that were actually present in the final provider payload.
func takeAppliedReasoning(rawRequest *any) *reasoning.Config {
	if rawRequest == nil || *rawRequest == nil {
		return nil
	}
	raw := *rawRequest
	*rawRequest = nil

	body, ok := rawRequestJSON(raw)
	if !ok {
		return nil
	}
	return inspectWireAppliedReasoning(body)
}

func captureAppliedReasoning(rawRequest *any, appliedReasoning **reasoning.Config) {
	if rawRequest == nil || *rawRequest == nil {
		return
	}
	if config := takeAppliedReasoning(rawRequest); config != nil {
		*appliedReasoning = config
	}
}

func rawRequestJSON(raw any) ([]byte, bool) {
	switch value := raw.(type) {
	case json.RawMessage:
		return value, len(value) > 0
	case []byte:
		return value, len(value) > 0
	case string:
		return []byte(value), value != ""
	default:
		body, err := json.Marshal(value)
		return body, err == nil && len(body) > 0
	}
}

type wireReasoningEnvelope struct {
	ReasoningEffort              json.RawMessage `json:"reasoning_effort"`
	Reasoning                    json.RawMessage `json:"reasoning"`
	Thinking                     json.RawMessage `json:"thinking"`
	OutputConfig                 json.RawMessage `json:"output_config"`
	GenerationConfig             json.RawMessage `json:"generationConfig"`
	AdditionalModelRequestFields json.RawMessage `json:"additionalModelRequestFields"`
}

type wireReasoningObject struct {
	Type               json.RawMessage `json:"type"`
	Mode               json.RawMessage `json:"mode"`
	Effort             json.RawMessage `json:"effort"`
	MaxTokens          json.RawMessage `json:"max_tokens"`
	BudgetTokens       json.RawMessage `json:"budget_tokens"`
	MaxReasoningEffort json.RawMessage `json:"maxReasoningEffort"`
	ThinkingBudget     json.RawMessage `json:"thinkingBudget"`
	ThinkingLevel      json.RawMessage `json:"thinkingLevel"`
	Thinking           json.RawMessage `json:"thinking"`
	ThinkingConfig     json.RawMessage `json:"thinkingConfig"`
	OutputConfig       json.RawMessage `json:"output_config"`
	ReasoningConfig    json.RawMessage `json:"reasoningConfig"`
}

func inspectWireAppliedReasoning(body []byte) *reasoning.Config {
	var root wireReasoningEnvelope
	if len(body) == 0 || json.Unmarshal(body, &root) != nil {
		return nil
	}

	if additional, ok := decodeWireReasoningObject(root.AdditionalModelRequestFields); ok {
		config := reasoning.Config{}
		if thinking, exists := decodeWireReasoningObject(additional.Thinking); exists {
			config.Mode = wireReasoningString(thinking.Type)
			config.BudgetTokens = wireReasoningInteger(thinking.BudgetTokens)
		}
		if outputConfig, exists := decodeWireReasoningObject(additional.OutputConfig); exists {
			config.Effort = wireReasoningString(outputConfig.Effort)
		}
		if reasoningConfig, exists := decodeWireReasoningObject(additional.ReasoningConfig); exists {
			if config.Mode == "" {
				config.Mode = wireReasoningString(reasoningConfig.Type)
			}
			if config.Effort == "" {
				config.Effort = wireReasoningString(reasoningConfig.MaxReasoningEffort)
			}
			if config.BudgetTokens == nil {
				config.BudgetTokens = wireReasoningInteger(reasoningConfig.BudgetTokens)
			}
		}
		if config.Present() {
			return &config
		}
	}

	if generationConfig, ok := decodeWireReasoningObject(root.GenerationConfig); ok {
		if thinkingConfig, exists := decodeWireReasoningObject(generationConfig.ThinkingConfig); exists {
			config := reasoning.Config{
				Effort:       wireReasoningString(thinkingConfig.ThinkingLevel),
				BudgetTokens: wireReasoningInteger(thinkingConfig.ThinkingBudget),
			}
			if config.Present() {
				return &config
			}
		}
	}

	config := reasoning.Config{}
	if thinking, ok := decodeWireReasoningObject(root.Thinking); ok {
		config.Mode = wireReasoningString(thinking.Type)
		config.BudgetTokens = wireReasoningInteger(thinking.BudgetTokens)
	}
	if outputConfig, ok := decodeWireReasoningObject(root.OutputConfig); ok {
		config.Effort = wireReasoningString(outputConfig.Effort)
	}
	if config.Present() {
		return &config
	}

	if responsesReasoning, ok := decodeWireReasoningObject(root.Reasoning); ok {
		config = reasoning.Config{
			Mode:         wireReasoningString(responsesReasoning.Mode),
			Effort:       wireReasoningString(responsesReasoning.Effort),
			BudgetTokens: wireReasoningInteger(responsesReasoning.MaxTokens),
		}
		if config.Present() {
			return &config
		}
	}

	if effort := wireReasoningString(root.ReasoningEffort); effort != "" {
		return &reasoning.Config{Effort: effort}
	}
	return nil
}

func decodeWireReasoningObject(raw json.RawMessage) (wireReasoningObject, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return wireReasoningObject{}, false
	}
	var object wireReasoningObject
	if json.Unmarshal(raw, &object) != nil {
		return wireReasoningObject{}, false
	}
	return object, true
}

func wireReasoningString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil ||
		len(value) == 0 || len(value) > maxAppliedReasoningValueBytes {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return ""
	}
	return strings.ToLower(value)
}

func wireReasoningInteger(raw json.RawMessage) *int64 {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	value, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil {
		return nil
	}
	return &value
}
