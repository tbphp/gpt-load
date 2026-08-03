package dialect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	openAIResponsesPath         = "/v1/responses"
	openAIResponsesCompactPath  = "/v1/responses/compact"
	openAIResponsesResourceRoot = "/responses"
)

type OpenAIResponses struct {
	openAI *OpenAI
}

var (
	_ Dialect               = (*OpenAIResponses)(nil)
	_ ModelRewriter         = (*OpenAIResponses)(nil)
	_ StreamEventClassifier = (*OpenAIResponses)(nil)
)

func NewOpenAIResponses(client *http.Client) *OpenAIResponses {
	return &OpenAIResponses{openAI: NewOpenAI(client)}
}

func (d *OpenAIResponses) Protocol() protocol.Protocol {
	return protocol.OpenAIResponses
}

func (d *OpenAIResponses) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata := RequestMetadata{}
	if len(req.Body) > 0 {
		parsed, err := inspectJSONRequestFields(req.Body, false)
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
		}
		metadata = parsed
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
		metadata.UsageDiagnostics = openAIRequestPricingDiagnostics(req.Body)
	}
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

func (d *OpenAIResponses) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("parsed request is required")
	}
	resourcePath, err := openAIResponsesResourcePath(req.Path)
	if err != nil {
		return "", err
	}
	return resolveUpstreamURL(base, resourcePath, req.RawQuery)
}

func openAIResponsesResourcePath(path string) (string, error) {
	switch {
	case path == openAIResponsesPath:
		return openAIResponsesResourceRoot, nil
	case strings.HasPrefix(path, openAIResponsesPath+"/"):
		return strings.TrimPrefix(path, "/v1"), nil
	default:
		return "", fmt.Errorf("invalid OpenAI Responses request path")
	}
}

func (d *OpenAIResponses) InjectCredential(headers http.Header, apiKey string) {
	d.openAI.InjectCredential(headers, apiKey)
}

func (d *OpenAIResponses) CredentialHeaderNames() []string {
	return d.openAI.CredentialHeaderNames()
}

func (d *OpenAIResponses) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	return d.openAI.ListModels(ctx, baseURL, apiKey, rules)
}

func (d *OpenAIResponses) Probe(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
	validationModel string,
) error {
	if err := validateProbeModel(validationModel); err != nil {
		return err
	}
	requestURL, err := d.BuildUpstreamURL(baseURL, &ParsedRequest{Method: http.MethodPost, Path: openAIResponsesPath})
	if err != nil {
		return fmt.Errorf("build %s probe URL failed", d.Protocol())
	}
	return executeProbe(
		ctx,
		d.openAI.client,
		d,
		requestURL,
		apiKey,
		rules,
		struct {
			Model           string `json:"model"`
			Input           string `json:"input"`
			MaxOutputTokens int    `json:"max_output_tokens"`
			Store           bool   `json:"store"`
		}{
			Model:           validationModel,
			Input:           "ping",
			MaxOutputTokens: 16,
			Store:           false,
		},
	)
}

func (d *OpenAIResponses) ClassifyStatus(
	status int,
	body []byte,
) health.FailureCategory {
	if status == http.StatusNotFound {
		lowered := strings.ToLower(string(body))
		switch {
		case containsFailureMarker(lowered, openAIFailureMarkers.rateLimited):
			return health.FailureCategoryRateLimited
		case containsFailureMarker(lowered, openAIFailureMarkers.modelUnavailable):
			return health.FailureCategoryModelUnavailable
		case containsFailureMarker(lowered, openAIFailureMarkers.invalidKey):
			return health.FailureCategoryInvalidKey
		case containsFailureMarker(lowered, openAIFailureMarkers.upstreamHost):
			return health.FailureCategoryUpstreamHostError
		default:
			return health.FailureCategoryClientError
		}
	}
	return d.openAI.ClassifyStatus(status, body)
}

func (d *OpenAIResponses) ClassifyProviderError(body []byte) health.FailureCategory {
	return d.openAI.ClassifyProviderError(body)
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
	case "response.completed":
		return StreamEventClassification{
			Disposition: StreamEventCompleted,
		}, nil
	case "response.incomplete":
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
