package dialect

import (
	"context"
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
	request.Header.Set("Accept-Encoding", "identity")

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

func (d *Anthropic) ExtractModel(req *ParsedRequest) (string, bool, error) {
	if req == nil {
		return "", false, fmt.Errorf("parsed request is required")
	}

	model, stream, err := extractJSONRequestFields(req.Body)
	if err != nil {
		return "", false, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	return model, stream, nil
}

func (d *Anthropic) ClassifyStatus(status int, body []byte) health.FailureCategory {
	return classifyStatusWithMarkers(status, body, anthropicFailureMarkers)
}
