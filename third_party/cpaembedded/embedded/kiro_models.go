package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	kiroListModelsTarget = "AmazonCodeWhispererService.ListAvailableModels"
	kiroListModelsOrigin = "AI_EDITOR"
	maxKiroModelsBytes   = 1 << 20
	maxKiroModelCount    = 256
	maxKiroModelIDLength = 256
)

// DiscoverKiroModels queries the Kiro management plane for the account's
// available models. OAuth (social / OIDC) credentials carry a profileArn that
// the management endpoint requires; API-key credentials cannot resolve one and
// therefore report that model discovery is unavailable. This is intentionally
// best-effort: Kiro's management contract is not public and may change.
func DiscoverKiroModels(ctx context.Context, credential KiroCredential, options KiroOptions) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizeKiroCredential(&credential)
	if err := validateKiroCredentialWithOptions(credential, options); err != nil {
		return nil, err
	}
	if strings.TrimSpace(credential.ProfileARN) == "" {
		return nil, ErrKiroModelDiscoveryUnavailable
	}
	host := strings.TrimSpace(options.ManagementHost)
	if host == "" {
		var err error
		host, err = KiroManagementURL(credential.Region)
		if err != nil {
			return nil, err
		}
	}
	body, err := json.Marshal(map[string]any{
		"profileArn": strings.TrimSpace(credential.ProfileARN),
		"origin":     kiroListModelsOrigin,
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, host, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.0")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("X-Amz-Target", kiroListModelsTarget)
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credential.AccessToken))
	request.Header.Set("User-Agent", kiroUserAgent)
	request.Header.Set("x-amz-user-agent", kiroAMZUserAgent)
	request.Header.Set("amz-sdk-invocation-id", randomKiroHex(16))
	response, err := kiroHTTPClient(options).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxKiroModelsBytes+1))
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	if len(raw) > maxKiroModelsBytes {
		return nil, fmt.Errorf("Kiro models response is too large")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, kiroHTTPErrorFromResponse(response)
	}
	var payload struct {
		Models []struct {
			ID string `json:"modelId"`
		} `json:"models"`
		ModelList []struct {
			ModelID string `json:"modelId"`
		} `json:"modelList"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Kiro models: %w", err)
	}
	seen := make(map[string]struct{}, maxKiroModelCount)
	models := make([]string, 0, maxKiroModelCount)
	collect := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > maxKiroModelIDLength || strings.ContainsAny(id, "\r\n\x00") {
			return
		}
		if _, duplicate := seen[id]; duplicate {
			return
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	for _, item := range payload.Models {
		collect(item.ID)
	}
	for _, item := range payload.ModelList {
		collect(item.ModelID)
	}
	if len(models) == 0 {
		return nil, ErrKiroModelDiscoveryUnavailable
	}
	sort.Strings(models)
	return models, nil
}
