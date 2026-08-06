package dialect

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"gpt-load/internal/health"
	platformheader "gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

const (
	anthropicMessagesPath         = "/v1/messages"
	anthropicMessagesResourcePath = "/messages"
	anthropicModelsResourcePath   = "/models"
	anthropicDefaultVersion       = "2023-06-01"
)

var anthropicFailureMarkers = failureMarkers{
	rateLimited:      []string{"rate_limit_error", "rate limit"},
	modelUnavailable: []string{"model_not_found", "model not found", "model_not_supported", "model not supported", "no access to model"},
	invalidKey:       []string{"authentication_error", "permission_error", "invalid x-api-key", "api key disabled", "api key banned"},
	upstreamHost:     []string{"overloaded_error"},
}

type Anthropic struct {
	client *http.Client
}

type anthropicModelPage struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
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

func (d *Anthropic) CredentialHeaderNames() []string {
	return []string{"X-Api-Key"}
}

func (d *Anthropic) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("parsed request is required")
	}
	if req.Path != anthropicMessagesPath {
		return "", fmt.Errorf("invalid Anthropic Messages request path")
	}
	return resolveUpstreamURL(base, anthropicMessagesResourcePath, req.RawQuery)
}

func (d *Anthropic) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	requestURL, err := resolveUpstreamURL(baseURL, anthropicModelsResourcePath, "")
	if err != nil {
		return nil, fmt.Errorf("build Anthropic model-list URL: %w", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return nil, fmt.Errorf("parse Anthropic model-list URL: %w", err)
	}

	collector := newModelListCollector()
	seenCursors := make(map[string]struct{})
	afterID := ""
	for pageNumber := 1; pageNumber <= maxModelListPages; pageNumber++ {
		page, err := d.listModelsPage(ctx, parsed, apiKey, rules, afterID)
		if err != nil {
			return nil, err
		}
		pageModels := make([]string, 0, len(page.Data))
		for _, item := range page.Data {
			pageModels = append(pageModels, item.ID)
		}
		if err := collector.Add(pageModels); err != nil {
			return nil, err
		}
		if !page.HasMore {
			return collector.Result(), nil
		}
		if strings.TrimSpace(page.LastID) == "" {
			return nil, fmt.Errorf("Anthropic model-list cursor is empty")
		}
		if _, repeated := seenCursors[page.LastID]; repeated {
			return nil, fmt.Errorf("Anthropic model-list cursor repeated")
		}
		if pageNumber == maxModelListPages || collector.Full() {
			return nil, fmt.Errorf("Anthropic model-list pagination limit exceeded")
		}
		seenCursors[page.LastID] = struct{}{}
		afterID = page.LastID
	}
	return nil, fmt.Errorf("Anthropic model-list pagination limit exceeded")
}

func (d *Anthropic) listModelsPage(
	ctx context.Context,
	endpoint *url.URL,
	apiKey string,
	rules state.HeaderRules,
	afterID string,
) (anthropicModelPage, error) {
	pageEndpoint := *endpoint
	query := pageEndpoint.Query()
	query.Del("limit")
	query.Set("limit", "1000")
	query.Del("after_id")
	if afterID != "" {
		query.Set("after_id", afterID)
	}
	pageEndpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageEndpoint.String(), nil)
	if err != nil {
		return anthropicModelPage{}, fmt.Errorf("create Anthropic model-list request: %w", err)
	}
	ApplyCredential(d, request.Header, apiKey, rules)
	platformheader.NormalizeUpstreamRequestRepresentation(request, 0)

	response, err := d.client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return anthropicModelPage{}, fmt.Errorf("request Anthropic model list: %w", contextErr)
		}
		return anthropicModelPage{}, fmt.Errorf("request Anthropic model list failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return anthropicModelPage{}, fmt.Errorf("request Anthropic model list: upstream status %d", response.StatusCode)
	}

	var page anthropicModelPage
	if err := decodeModelListPage(response, &page); err != nil {
		return anthropicModelPage{}, fmt.Errorf("decode Anthropic model list: %w", err)
	}
	return page, nil
}

func (d *Anthropic) Probe(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
	validationModel string,
) error {
	if err := validateProbeModel(validationModel); err != nil {
		return err
	}
	requestURL, err := d.BuildUpstreamURL(baseURL, &ParsedRequest{Method: http.MethodPost, Path: anthropicMessagesPath})
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

func (d *Anthropic) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata, err := inspectJSONRequestFields(req.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.ObserveUsage = true
	metadata.Reasoning = inspectAnthropicReasoning(req.Body)
	return metadata, nil
}

func (d *Anthropic) ClassifyStatus(status int, body []byte) health.FailureCategory {
	return classifyStatusWithMarkers(status, body, anthropicFailureMarkers)
}

func (d *Anthropic) ClassifyProviderError(body []byte) health.FailureCategory {
	return classifyProviderErrorWithMarkers(body, anthropicFailureMarkers)
}
