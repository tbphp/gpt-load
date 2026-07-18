package dialect

import (
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
	anthropicMessagesPath   = "/v1/messages"
	anthropicModelsPath     = "/v1/models"
	anthropicDefaultVersion = "2023-06-01"
)

var anthropicRetryableErrorMarkers = []string{
	"authentication_error",
	"permission_error",
	"rate_limit_error",
	"overloaded_error",
	"model_not_found",
	"model not found",
	"model_not_supported",
	"model not supported",
	"no access to model",
}

type Anthropic struct {
	client *http.Client
}

var _ Dialect = (*Anthropic)(nil)

func NewAnthropic(client *http.Client) *Anthropic {
	return &Anthropic{client: client}
}

func (d *Anthropic) Protocol() protocol.Protocol {
	return protocol.Anthropic
}

func (d *Anthropic) InjectCredential(headers http.Header, apiKey string) {
	if headers == nil {
		return
	}
	headers.Set("X-Api-Key", apiKey)
	if strings.TrimSpace(headers.Get("Anthropic-Version")) == "" {
		headers.Set("Anthropic-Version", anthropicDefaultVersion)
	}
}

func (d *Anthropic) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	return buildUpstreamURL(base, req)
}

func (d *Anthropic) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	requestURL, err := buildUpstreamURL(baseURL, &ParsedRequest{Path: anthropicModelsPath})
	if err != nil {
		return nil, fmt.Errorf("build Anthropic model-list URL: %w", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return nil, fmt.Errorf("parse Anthropic model-list URL: %w", err)
	}
	query := parsed.Query()
	query.Set("limit", "1000")
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Anthropic model-list request: %w", err)
	}
	ApplyCredential(d, request.Header, apiKey, rules)

	response, err := d.client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("request Anthropic model list: %w", contextErr)
		}
		return nil, fmt.Errorf("request Anthropic model list failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("request Anthropic model list: upstream status %d", response.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Anthropic model list: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		models = append(models, model.ID)
	}
	return models, nil
}

func (d *Anthropic) ExtractModel(req *ParsedRequest) (string, bool, error) {
	if req == nil {
		return "", false, fmt.Errorf("parsed request is required")
	}

	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return "", false, fmt.Errorf("decode Anthropic request: %w", err)
	}

	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", false, fmt.Errorf("Anthropic model is required")
	}
	if model != payload.Model {
		return "", false, fmt.Errorf("Anthropic model must not contain boundary whitespace")
	}
	return model, payload.Stream, nil
}

func (d *Anthropic) ClassifyStatus(status int, body []byte) health.ErrorClass {
	return classifyStatusWithMarkers(status, body, anthropicRetryableErrorMarkers)
}
