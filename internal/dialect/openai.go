package dialect

import (
	"context"
	"fmt"
	"net/http"

	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	openAICompletionsPath         = "/v1/chat/completions"
	openAICompletionsResourcePath = "/chat/completions"
	openAIModelsResourcePath      = "/models"
)

var openAIFailureMarkers = failureMarkers{
	rateLimited:      []string{"rate_limit", "rate limit", "insufficient_quota", "quota_exceeded", "quota exceeded"},
	modelUnavailable: []string{"model_not_found", "model not found", "model_not_supported", "model not supported", "no access to model"},
	invalidKey:       []string{"invalid_api_key", "invalid api key", "incorrect api key", "api_key_disabled", "api key disabled", "account_deactivated", "api_key_banned", "api key banned"},
	upstreamHost:     []string{"overloaded_error", "server_overloaded"},
}

type OpenAI struct {
	client *http.Client
}

var _ Dialect = (*OpenAI)(nil)

func NewOpenAI(client *http.Client) *OpenAI {
	return &OpenAI{client: client}
}

func (d *OpenAI) Protocol() protocol.Protocol {
	return protocol.OpenAICompletions
}

func (d *OpenAI) InjectCredential(headers http.Header, apiKey string) {
	if headers == nil {
		return
	}
	headers.Set("Authorization", "Bearer "+apiKey)
}

func (d *OpenAI) CredentialHeaderNames() []string {
	return []string{"Authorization"}
}

func (d *OpenAI) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("parsed request is required")
	}
	if req.Path != openAICompletionsPath {
		return "", fmt.Errorf("invalid OpenAI Completions request path")
	}
	return resolveUpstreamURL(base, openAICompletionsResourcePath, req.RawQuery)
}

func (d *OpenAI) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	requestURL, err := resolveUpstreamURL(baseURL, openAIModelsResourcePath, "")
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
	req.Header.Set("Accept-Encoding", "identity")

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
	if err := decodeModelListPage(resp, &payload); err != nil {
		return nil, fmt.Errorf("decode OpenAI model list: %w", err)
	}

	pageModels := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		pageModels = append(pageModels, model.ID)
	}
	collector := newModelListCollector()
	if err := collector.Add(pageModels); err != nil {
		return nil, err
	}
	return collector.Result(), nil
}

func (d *OpenAI) Probe(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
	validationModel string,
) error {
	if err := validateProbeModel(validationModel); err != nil {
		return err
	}
	requestURL, err := d.BuildUpstreamURL(baseURL, &ParsedRequest{Method: http.MethodPost, Path: openAICompletionsPath})
	if err != nil {
		return fmt.Errorf("build %s probe URL failed", d.Protocol())
	}
	return executeProbe(ctx, d.client, d, requestURL, apiKey, rules, struct {
		Model     string         `json:"model"`
		Messages  []probeMessage `json:"messages"`
		MaxTokens int            `json:"max_tokens"`
	}{
		Model:     validationModel,
		Messages:  []probeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
}

func (d *OpenAI) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata, err := inspectJSONRequestFields(req.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.ObserveUsage = true
	return metadata, nil
}

func (d *OpenAI) ClassifyStatus(status int, body []byte) health.FailureCategory {
	return classifyStatusWithMarkers(status, body, openAIFailureMarkers)
}

func (d *OpenAI) ClassifyProviderError(body []byte) health.FailureCategory {
	return classifyProviderErrorWithMarkers(body, openAIFailureMarkers)
}
