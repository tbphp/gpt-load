package dialect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gpt-load/internal/state"
)

type probeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func validateProbeModel(validationModel string) error {
	if strings.TrimSpace(validationModel) == "" {
		return fmt.Errorf("probe validation model is required")
	}
	return nil
}

func executeProbe(
	ctx context.Context,
	client *http.Client,
	dialect Dialect,
	baseURL, apiKey string,
	rules state.HeaderRules,
	path string,
	payload any,
) error {
	if ctx == nil {
		return fmt.Errorf("probe context is required")
	}
	if client == nil {
		return fmt.Errorf("request %s probe failed", dialect.Protocol())
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode %s probe request failed", dialect.Protocol())
	}
	requestURL, err := dialect.BuildUpstreamURL(baseURL, &ParsedRequest{
		Method: http.MethodPost,
		Path:   path,
	})
	if err != nil {
		return fmt.Errorf("build %s probe URL failed", dialect.Protocol())
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s probe request failed", dialect.Protocol())
	}
	request.Header.Set("Content-Type", "application/json")
	ApplyCredential(dialect, request.Header, apiKey, rules)
	request.Header.Set("Accept-Encoding", "identity")

	response, err := client.Do(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("request %s probe failed", dialect.Protocol())
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("request %s probe failed", dialect.Protocol())
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request %s probe: upstream status %d", dialect.Protocol(), response.StatusCode)
	}
	return nil
}
