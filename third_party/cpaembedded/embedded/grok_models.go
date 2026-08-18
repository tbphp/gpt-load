package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	xaiauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/xai"
)

const (
	grokClientVersion = "0.2.120"
	grokModelsURL     = xaiauth.CLIChatProxyBaseURL + "/models"
)

func DiscoverGrokModels(ctx context.Context, credential GrokCredential, options GrokOptions) ([]string, error) {
	endpoint := strings.TrimSpace(options.ModelsURL)
	if endpoint == "" {
		endpoint = grokModelsURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credential.AccessToken))
	request.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	request.Header.Set("x-grok-client-version", grokClientVersion)
	request.Header.Set("User-Agent", "xai-grok-workspace/"+grokClientVersion)
	request.Header.Set("x-grok-client-identifier", "grok-shell")
	request.Header.Set("x-authenticateresponse", "authenticate-response")
	response, err := grokHTTPClient(options).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponse+1))
	if err != nil {
		return nil, err
	}
	defer clear(body)
	if len(body) > maxTokenResponse {
		return nil, fmt.Errorf("Grok models response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &GrokUpstreamHTTPError{Operation: "models", StatusCode: response.StatusCode}
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode Grok models: %w", err)
	}
	seen := make(map[string]struct{}, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Grok models response has no usable model IDs")
	}
	sort.Strings(models)
	return models, nil
}
