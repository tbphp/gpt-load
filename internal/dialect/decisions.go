package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const decisionsPath = "/v1/systemone"

// Decisions implements the native Jev Decisions request contract.
type Decisions struct{}

func NewDecisions() *Decisions { return &Decisions{} }

func (*Decisions) Protocol() protocol.Protocol { return protocol.Decisions }

func (d *Decisions) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil || request.Method != http.MethodPost || request.Path != decisionsPath {
		return RequestMetadata{}, fmt.Errorf("decisions requires POST %s", decisionsPath)
	}
	if value := request.Header.Get("Content-Type"); strings.TrimSpace(value) != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			return RequestMetadata{}, fmt.Errorf("decisions requires application/json")
		}
	}
	metadata, err := inspectJSONRequestFields(request.Body, true, false)
	if err != nil {
		return RequestMetadata{}, err
	}
	if metadata.Stream {
		return RequestMetadata{}, fmt.Errorf("decisions does not support streaming")
	}
	if err := validateDecisionsInput(request.Body); err != nil {
		return RequestMetadata{}, err
	}
	metadata.Operation = execution.OperationDecisionsCreate
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.ObserveUsage = true
	return metadata, nil
}

func validateDecisionsInput(body []byte) error {
	object, err := decodeJSONObject(body)
	if err != nil {
		return fmt.Errorf("decode decisions request")
	}
	state, exists := object["state"]
	if !exists || !validDecisionsState(state) {
		return fmt.Errorf("decisions state must be a string, object, or array")
	}
	rawQuestions, exists := object["questions"]
	if !exists {
		return fmt.Errorf("decisions questions are required")
	}
	questions, err := decodeJSONObject(rawQuestions)
	if err != nil || len(questions) == 0 {
		return fmt.Errorf("decisions questions must be a non-empty object")
	}
	for name, raw := range questions {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("decisions question name must not be empty")
		}
		if err := validateDecisionQuestion(raw); err != nil {
			return fmt.Errorf("decisions question %q: %w", name, err)
		}
	}
	return nil
}

func validDecisionsState(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '"':
		var value string
		return json.Unmarshal(trimmed, &value) == nil
	case '{':
		var value map[string]json.RawMessage
		return json.Unmarshal(trimmed, &value) == nil && value != nil
	case '[':
		var value []json.RawMessage
		return json.Unmarshal(trimmed, &value) == nil && value != nil
	default:
		return false
	}
}

func validateDecisionQuestion(raw json.RawMessage) error {
	question, err := decodeJSONObject(raw)
	if err != nil {
		return fmt.Errorf("must be an object")
	}
	questionType, ok := jsonStringField(question, "type")
	if !ok {
		return fmt.Errorf("type is required")
	}
	instructions, ok := question["instructions"]
	if !ok || !validDecisionContent(instructions, false) {
		return fmt.Errorf("instructions must be a string, object, or array")
	}
	criteria, hasCriteria := question["criteria"]
	switch questionType {
	case "choice":
		if !hasCriteria {
			return fmt.Errorf("criteria are required")
		}
		options, err := decodeJSONObject(criteria)
		if err != nil || len(options) == 0 || len(options) > 255 {
			return fmt.Errorf("choice criteria must contain 1 to 255 options")
		}
		for id, rawOption := range options {
			if strings.TrimSpace(id) == "" || !validDecisionContent(rawOption, true) {
				return fmt.Errorf("choice criteria contain an invalid option")
			}
		}
	case "score":
		if !hasCriteria {
			return fmt.Errorf("criteria are required")
		}
		var levels []json.RawMessage
		if json.Unmarshal(criteria, &levels) != nil || len(levels) < 2 || len(levels) > 10 {
			return fmt.Errorf("score criteria must contain 2 to 10 levels")
		}
		for _, level := range levels {
			if !validDecisionContent(level, false) {
				return fmt.Errorf("score criteria contain an invalid level")
			}
		}
	case "noul":
		if !hasCriteria {
			return nil
		}
		options, err := decodeJSONObject(criteria)
		if err != nil {
			return fmt.Errorf("noul criteria must be an object")
		}
		for _, id := range []string{"true", "false"} {
			if rawOption, exists := options[id]; exists && !validDecisionContent(rawOption, false) {
				return fmt.Errorf("noul criteria contain an invalid description")
			}
		}
	default:
		return fmt.Errorf("unsupported type %q", questionType)
	}
	return nil
}

func validDecisionContent(raw json.RawMessage, allowNull bool) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	if allowNull && bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	switch trimmed[0] {
	case '"':
		var value string
		return json.Unmarshal(trimmed, &value) == nil && strings.TrimSpace(value) != ""
	case '{':
		var value map[string]json.RawMessage
		return json.Unmarshal(trimmed, &value) == nil && value != nil
	case '[':
		var value []json.RawMessage
		return json.Unmarshal(trimmed, &value) == nil && value != nil
	default:
		return false
	}
}

func (d *Decisions) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(request.Body)
	if err != nil {
		return nil, err
	}
	for field := range object {
		if decisionsControlField(field) || field == "stream" {
			delete(object, field)
		}
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode decisions model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode decisions request: %w", err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if clone.Header.Get("Content-Type") == "" {
		clone.Header.Set("Content-Type", "application/json")
	}
	return clone, nil
}

func decisionsControlField(name string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(name), "-", "_")
	switch normalized {
	case "fallback", "fallbacks", "authorization", "proxy_authorization",
		"api_key", "apikey", "x_api_key", "x_goog_api_key":
		return true
	default:
		return false
	}
}

func (*Decisions) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "model", model)
}
