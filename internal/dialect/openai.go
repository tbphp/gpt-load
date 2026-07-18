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
	openAIChatCompletionsPath = "/v1/chat/completions"
	openAIModelsPath          = "/v1/models"
)

var openAIRetryableErrorMarkers = []string{
	"invalid_api_key",
	"invalid api key",
	"incorrect api key",
	"rate_limit",
	"rate limit",
	"quota",
	"model_not_found",
	"model not found",
	"model_not_supported",
	"model not supported",
	"no access to model",
	"disabled",
	"banned",
}

type OpenAI struct {
	client *http.Client
}

var _ Dialect = (*OpenAI)(nil)

func NewOpenAI(client *http.Client) *OpenAI {
	return &OpenAI{client: client}
}

func (d *OpenAI) Protocol() protocol.Protocol {
	return protocol.OpenAI
}

func (d *OpenAI) InjectCredential(headers http.Header, apiKey string) {
	if headers == nil {
		return
	}
	headers.Set("Authorization", "Bearer "+apiKey)
}

func (d *OpenAI) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	return buildUpstreamURL(base, req)
}

func (d *OpenAI) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	requestURL, err := buildUpstreamURL(baseURL, &ParsedRequest{Path: openAIModelsPath})
	if err != nil {
		return nil, fmt.Errorf("build OpenAI model-list URL: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		requestURL,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create OpenAI model-list request: %w", err)
	}
	ApplyCredential(d, req.Header, apiKey, rules)

	resp, err := d.client.Do(req)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("request OpenAI model list: %w", contextErr)
		}
		return nil, fmt.Errorf("request OpenAI model list failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf(
			"request OpenAI model list: upstream status %d",
			resp.StatusCode,
		)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode OpenAI model list: %w", err)
	}

	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		models = append(models, model.ID)
	}
	return models, nil
}

func (d *OpenAI) ExtractModel(req *ParsedRequest) (string, bool, error) {
	if req == nil {
		return "", false, fmt.Errorf("parsed request is required")
	}

	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return "", false, fmt.Errorf("decode OpenAI request: %w", err)
	}

	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", false, fmt.Errorf("OpenAI model is required")
	}
	if model != payload.Model {
		return "", false, fmt.Errorf("OpenAI model must not contain boundary whitespace")
	}
	return model, payload.Stream, nil
}

func (d *OpenAI) ClassifyStatus(status int, body []byte) health.ErrorClass {
	switch status {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusTooManyRequests:
		return health.ErrorClassRetryable
	}
	if status >= http.StatusInternalServerError {
		return health.ErrorClassRetryable
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return health.ErrorClassNonRetryable
	}

	lowered := strings.ToLower(string(body))
	for _, marker := range openAIRetryableErrorMarkers {
		if strings.Contains(lowered, marker) {
			return health.ErrorClassRetryable
		}
	}
	return health.ErrorClassNonRetryable
}

func buildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("parsed request is required")
	}
	if req.Path == "" || !strings.HasPrefix(req.Path, "/") {
		return "", fmt.Errorf("request path must be absolute")
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse upstream base URL: %w", err)
	}
	if parsed.Host == "" ||
		(!strings.EqualFold(parsed.Scheme, "http") &&
			!strings.EqualFold(parsed.Scheme, "https")) {
		return "", fmt.Errorf("upstream base URL must use http or https")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + req.Path
	parsed.RawPath = ""
	if parsed.RawQuery == "" {
		parsed.RawQuery = req.RawQuery
	} else if req.RawQuery != "" {
		parsed.RawQuery += "&" + req.RawQuery
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}
