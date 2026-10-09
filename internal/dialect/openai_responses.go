package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	openAIResponsesPath        = "/v1/responses"
	openAIResponsesCompactPath = "/v1/responses/compact"
)

type OpenAIResponses struct{}

var (
	_ Dialect               = (*OpenAIResponses)(nil)
	_ ModelRewriter         = (*OpenAIResponses)(nil)
	_ StreamEventClassifier = (*OpenAIResponses)(nil)
)

func NewOpenAIResponses() *OpenAIResponses {
	return &OpenAIResponses{}
}

func (d *OpenAIResponses) Protocol() protocol.Protocol {
	return protocol.OpenAIResponses
}

func (d *OpenAIResponses) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	if req.Path == "/v1/alpha/search" {
		return inspectCodexSearchRequest(req)
	}

	metadata := RequestMetadata{}
	if len(req.Body) > 0 {
		parsed, err := inspectJSONRequestFields(req.Body, false, req.Method == http.MethodPost && req.Path == openAIResponsesPath)
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
		}
		metadata = parsed
		if req.Method == http.MethodPost && req.Path == openAIResponsesPath {
			metadata.PromptCacheKey = inspectPromptCacheKey(req.Body)
		}
	}
	if req.Method == http.MethodGet {
		stream, present, err := inspectResponsesStreamQuery(req.RawQuery)
		if err != nil {
			return RequestMetadata{}, err
		}
		if present {
			metadata.Stream = stream
		}
	}
	metadata.ObserveUsage = req.Method == http.MethodPost &&
		(req.Path == openAIResponsesPath || req.Path == openAIResponsesCompactPath)
	if len(req.Body) > 0 {
		if metadata.PreviousResponseID == "" {
			metadata.AffinityPrefix = inspectPromptAffinityPrefix(d.Protocol(), req.Body)
		}
		pricingMode, diagnostics, err := openAIRequestPricing(req.Body)
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("inspect %s request pricing: %w", d.Protocol(), err)
		}
		metadata.PricingMode = pricingMode
		metadata.UsageDiagnostics = diagnostics
		metadata.Reasoning = inspectOpenAIResponsesReasoning(req.Body)
	}
	metadata.Operation, metadata.RouteRequirement, metadata.ResponsesStorePreference =
		responsesExecutionMetadata(req)
	return metadata, nil
}

func inspectCodexSearchRequest(req *ParsedRequest) (RequestMetadata, error) {
	metadata, err := inspectJSONRequestFields(req.Body, true, false)
	if err != nil {
		return RequestMetadata{}, err
	}
	if req.Method != http.MethodPost || metadata.Stream {
		return RequestMetadata{}, fmt.Errorf("Codex search requires a non-streaming POST")
	}
	var fields struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Body, &fields); err != nil || fields.ID == "" {
		return RequestMetadata{}, fmt.Errorf("Codex search id must be a non-empty string")
	}
	metadata.Operation = execution.OperationWebSearch
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.AffinityPrefix = []byte("codex-search\x00" + fields.ID)
	return metadata, nil
}

func inspectResponsesStreamQuery(rawQuery string) (bool, bool, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false, false, fmt.Errorf("decode Responses query: %w", err)
	}
	streamValues, exists := values["stream"]
	if !exists {
		return false, false, nil
	}
	if len(streamValues) != 1 {
		return false, false, fmt.Errorf("stream query parameter must be unique")
	}
	switch streamValues[0] {
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("stream query parameter must be true or false")
	}
}

func (*OpenAIResponses) RequiresTerminalEvent() bool {
	return true
}

func (*OpenAIResponses) ClassifyStreamEvent(
	event StreamEvent,
) (StreamEventClassification, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &object); err != nil || object == nil {
		return StreamEventClassification{}, fmt.Errorf(
			"decode OpenAI Responses stream event",
		)
	}

	payloadType := ""
	if raw, exists := object["type"]; exists {
		if err := json.Unmarshal(raw, &payloadType); err != nil ||
			payloadType == "" {
			return StreamEventClassification{}, fmt.Errorf(
				"decode OpenAI Responses stream event type",
			)
		}
	}
	if event.Name != "" && payloadType != "" && event.Name != payloadType {
		return StreamEventClassification{}, fmt.Errorf(
			"OpenAI Responses stream event name %q conflicts with payload type %q",
			event.Name,
			payloadType,
		)
	}

	eventType := event.Name
	if eventType == "" {
		eventType = payloadType
	}
	switch eventType {
	case "response.interrupt.failed":
		// This error belongs to the control operation, not the model response.
		// The upstream continues emitting the original response and its usage.
		return StreamEventClassification{Disposition: StreamEventContinue}, nil
	case "response.completed":
		return StreamEventClassification{
			Disposition: StreamEventCompleted,
		}, nil
	case "response.incomplete":
		if responsesInterrupted(event.Payload) {
			return StreamEventClassification{Disposition: StreamEventCompleted}, nil
		}
		return StreamEventClassification{
			Disposition: StreamEventIncomplete,
		}, nil
	case "response.failed", "error":
		return StreamEventClassification{
			Disposition: StreamEventFailed,
		}, nil
	}

	if raw, exists := object["error"]; exists {
		value := bytes.TrimSpace(raw)
		if len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			return StreamEventClassification{
				Disposition: StreamEventFailed,
			}, nil
		}
	}
	return StreamEventClassification{
		Disposition: StreamEventContinue,
	}, nil
}

// Responses Lite ends an accepted interrupt with a genuine incomplete terminal.
// Only this explicit reason is a normal, reusable end of the turn.
func responsesInterrupted(payload []byte) bool {
	var event struct {
		Type     string `json:"type"`
		Response struct {
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	return json.Unmarshal(payload, &event) == nil && event.Type == "response.incomplete" && event.Response.IncompleteDetails.Reason == "interrupted"
}
