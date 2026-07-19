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
	geminiGenerationPrefix = "/v1beta/models/"
	geminiGenerateSuffix   = ":generateContent"
	geminiStreamSuffix     = ":streamGenerateContent"
	geminiModelsPath       = "/v1beta/models"
)

var geminiRetryableErrorMarkers = []string{
	"api_key_invalid",
	"permission_denied",
	"resource_exhausted",
	"model_not_found",
	"model not found",
	"model_not_supported",
	"model not supported",
	"no access to model",
}

type Gemini struct {
	client *http.Client
}

type geminiModelPage struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
	NextPageToken string `json:"nextPageToken"`
}

var _ Dialect = (*Gemini)(nil)

func NewGemini(client *http.Client) *Gemini {
	return &Gemini{client: client}
}

func (d *Gemini) Protocol() protocol.Protocol {
	return protocol.Gemini
}

func (d *Gemini) InjectCredential(headers http.Header, apiKey string) {
	if headers == nil {
		return
	}
	headers.Set("X-Goog-Api-Key", apiKey)
}

func (d *Gemini) ExtractModel(req *ParsedRequest) (string, bool, error) {
	if req == nil {
		return "", false, fmt.Errorf("parsed request is required")
	}
	return parseGeminiGenerationPath(req.Path)
}

func (d *Gemini) BuildUpstreamURL(base string, req *ParsedRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("parsed request is required")
	}
	_, stream, err := parseGeminiGenerationPath(req.Path)
	if err != nil {
		return "", err
	}
	upstream, err := buildUpstreamURL(base, req)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		return "", fmt.Errorf("parse Gemini upstream URL: %w", err)
	}
	query := parsed.Query()
	query.Del("alt")
	if stream {
		query.Set("alt", "sse")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (d *Gemini) ListModels(
	ctx context.Context,
	baseURL, apiKey string,
	rules state.HeaderRules,
) ([]string, error) {
	requestURL, err := buildUpstreamURL(baseURL, &ParsedRequest{Path: geminiModelsPath})
	if err != nil {
		return nil, fmt.Errorf("build Gemini model-list URL: %w", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return nil, fmt.Errorf("parse Gemini model-list URL: %w", err)
	}

	collector := newModelListCollector()
	seenPageTokens := make(map[string]struct{})
	pageToken := ""
	for pageNumber := 1; pageNumber <= maxModelListPages; pageNumber++ {
		page, err := d.listModelsPage(ctx, parsed, apiKey, rules, pageToken)
		if err != nil {
			return nil, err
		}
		pageModels := make([]string, 0, len(page.Models))
		for _, item := range page.Models {
			name := strings.TrimPrefix(item.Name, "models/")
			if name != "" {
				pageModels = append(pageModels, name)
			}
		}
		if err := collector.Add(pageModels); err != nil {
			return nil, err
		}
		if page.NextPageToken == "" {
			return collector.Result(), nil
		}
		if strings.TrimSpace(page.NextPageToken) == "" {
			return nil, fmt.Errorf("Gemini model-list page token is empty")
		}
		if _, repeated := seenPageTokens[page.NextPageToken]; repeated {
			return nil, fmt.Errorf("Gemini model-list page token repeated")
		}
		if pageNumber == maxModelListPages || collector.Full() {
			return nil, fmt.Errorf("Gemini model-list pagination limit exceeded")
		}
		seenPageTokens[page.NextPageToken] = struct{}{}
		pageToken = page.NextPageToken
	}
	return nil, fmt.Errorf("Gemini model-list pagination limit exceeded")
}

func (d *Gemini) listModelsPage(
	ctx context.Context,
	endpoint *url.URL,
	apiKey string,
	rules state.HeaderRules,
	pageToken string,
) (geminiModelPage, error) {
	pageEndpoint := *endpoint
	query := pageEndpoint.Query()
	query.Del("pageSize")
	query.Set("pageSize", "1000")
	query.Del("pageToken")
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	pageEndpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageEndpoint.String(), nil)
	if err != nil {
		return geminiModelPage{}, fmt.Errorf("create Gemini model-list request: %w", err)
	}
	ApplyCredential(d, request.Header, apiKey, rules)

	response, err := d.client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return geminiModelPage{}, fmt.Errorf("request Gemini model list: %w", contextErr)
		}
		return geminiModelPage{}, fmt.Errorf("request Gemini model list failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return geminiModelPage{}, fmt.Errorf("request Gemini model list: upstream status %d", response.StatusCode)
	}

	var page geminiModelPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		return geminiModelPage{}, fmt.Errorf("decode Gemini model list: %w", err)
	}
	return page, nil
}

func (d *Gemini) ClassifyStatus(status int, body []byte) health.ErrorClass {
	return classifyStatusWithMarkers(status, body, geminiRetryableErrorMarkers)
}

func parseGeminiGenerationPath(path string) (model string, stream bool, err error) {
	if !strings.HasPrefix(path, geminiGenerationPrefix) {
		return "", false, fmt.Errorf("invalid Gemini generation path")
	}
	modelAndMethod := strings.TrimPrefix(path, geminiGenerationPrefix)
	switch {
	case strings.HasSuffix(modelAndMethod, geminiStreamSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiStreamSuffix)
		stream = true
	case strings.HasSuffix(modelAndMethod, geminiGenerateSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiGenerateSuffix)
	default:
		return "", false, fmt.Errorf("invalid Gemini generation method")
	}
	if model == "" || strings.Contains(model, "/") || strings.TrimSpace(model) != model {
		return "", false, fmt.Errorf("invalid Gemini model")
	}
	return model, stream, nil
}
