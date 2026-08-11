package bifrost

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/parametertrace"
	"gpt-load/internal/reasoning"
)

const maxAppliedReasoningValueBytes = 64

// enableConvertedWireCapture asks Bifrost to return the final serialized
// provider request for one bounded converted attempt. The caller must remove
// RawRequest immediately after extracting the safe projection.
func enableConvertedWireCapture(
	ctx *schemas.BifrostContext,
	prepared preparedAttempt,
	client *parametertrace.Snapshot,
) {
	if ctx == nil || prepared.mode != channel.RouteConverted || !capturableSnapshot(client) {
		return
	}
	ctx.SetValue(schemas.BifrostContextKeyAllowPerRequestRawOverride, true)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawRequest, true)
	ctx.SetValue(schemas.BifrostContextKeySendBackRawResponse, false)
}

func capturableSnapshot(snapshot *parametertrace.Snapshot) bool {
	return snapshot != nil &&
		(snapshot.State == parametertrace.CaptureCaptured || snapshot.State == parametertrace.CapturePartial)
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

// takeWireObservation clears the sensitive raw request before parsing it and
// returns only the bounded target parameter projection plus the legacy
// reasoning projection used elsewhere in request-log summaries.
func takeWireObservation(
	rawRequest *any,
	client *parametertrace.Snapshot,
) (*reasoning.Config, *parametertrace.Trace) {
	if rawRequest == nil || *rawRequest == nil {
		return nil, defaultConversionTrace(client)
	}
	raw := *rawRequest
	*rawRequest = nil
	body, ok := rawRequestJSON(raw)
	if !ok {
		return nil, defaultConversionTrace(client)
	}
	reasoningConfig := inspectWireAppliedReasoning(body)
	if client == nil {
		return reasoningConfig, nil
	}
	target := parametertrace.ProjectJSON(body)
	trace := parametertrace.Compare(*client, target)
	return reasoningConfig, &trace
}

func defaultConversionTrace(client *parametertrace.Snapshot) *parametertrace.Trace {
	if client == nil {
		return nil
	}
	state := parametertrace.CaptureUnavailable
	if client.State == parametertrace.CaptureSkippedOversize {
		state = parametertrace.CaptureSkippedOversize
	}
	trace := parametertrace.Trace{
		SchemaVersion: parametertrace.SchemaVersion,
		State:         state,
		Target: parametertrace.Snapshot{
			SchemaVersion: parametertrace.SchemaVersion,
			State:         state,
			Entries:       []parametertrace.Entry{},
		},
		Changes: []parametertrace.Change{},
	}
	return &trace
}

func conversionClientParameters(spec *execution.AttemptSpec) *parametertrace.Snapshot {
	if spec == nil || spec.RouteMode != execution.RouteConverted {
		return nil
	}
	if spec.ClientParameters != nil {
		parameters := parametertrace.CloneSnapshot(*spec.ClientParameters)
		spec.ClientParameters = &parameters
		return spec.ClientParameters
	}
	parameters := parametertrace.ProjectJSON(spec.Body)
	spec.ClientParameters = &parameters
	return spec.ClientParameters
}

func captureWireObservation(
	rawRequest *any,
	client *parametertrace.Snapshot,
	appliedReasoning **reasoning.Config,
	conversionTrace **parametertrace.Trace,
) {
	if rawRequest == nil || *rawRequest == nil {
		return
	}
	reasoningConfig, trace := takeWireObservation(rawRequest, client)
	if reasoningConfig != nil {
		*appliedReasoning = reasoningConfig
	}
	if trace != nil {
		*conversionTrace = trace
	}
}

func finalConversionTrace(
	client *parametertrace.Snapshot,
	trace *parametertrace.Trace,
) *parametertrace.Trace {
	if trace != nil {
		clone := parametertrace.CloneTrace(*trace)
		return &clone
	}
	return defaultConversionTrace(client)
}

func preflightConversionTrace(
	spec execution.AttemptSpec,
	failure execution.AttemptResult,
	client *parametertrace.Snapshot,
) *parametertrace.Trace {
	if spec.RouteMode != execution.RouteConverted {
		return nil
	}
	if failure.Error == nil || failure.Error.Kind != execution.ErrorKindConversionUnsupported {
		return defaultConversionTrace(client)
	}
	trace := parametertrace.PreflightBlocked()
	return &trace
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
