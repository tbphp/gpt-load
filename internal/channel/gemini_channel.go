package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func init() {
	Register("gemini", newGeminiChannel)
}

type GeminiChannel struct {
	*BaseChannel
}

func newGeminiChannel(f *Factory, group *models.Group) (ChannelProxy, error) {
	base, err := f.newBaseChannel("gemini", group)
	if err != nil {
		return nil, err
	}

	return &GeminiChannel{
		BaseChannel: base,
	}, nil
}

// BuildUpstreamURL constructs the target URL for Gemini requests.
func (ch *GeminiChannel) BuildUpstreamURL(originalURL *url.URL, groupName string) (string, error) {
	base := ch.getUpstreamURL()
	if base == nil {
		return "", fmt.Errorf("no upstream URL configured for channel %s", ch.Name)
	}

	finalURL := *base
	requestPath := trimProxyGroupPrefix(originalURL.Path, groupName)

	if publisherBasePath, ok := vertexPublisherBasePath(base); ok {
		finalURL.Path = buildVertexPublisherPath(publisherBasePath, requestPath)
	} else {
		finalURL.Path = joinURLPath(base.Path, requestPath)
	}

	finalURL.RawQuery = originalURL.RawQuery

	return finalURL.String(), nil
}

// ModifyRequest adds the API key as a query parameter for Gemini requests.
func (ch *GeminiChannel) ModifyRequest(req *http.Request, apiKey *models.APIKey, group *models.Group) {
	if strings.Contains(req.URL.Path, "v1beta/openai") {
		req.Header.Set("Authorization", "Bearer "+apiKey.KeyValue)
	} else {
		q := req.URL.Query()
		q.Set("key", apiKey.KeyValue)
		req.URL.RawQuery = q.Encode()
	}
}

// IsStreamRequest checks if the request is for a streaming response.
func (ch *GeminiChannel) IsStreamRequest(c *gin.Context, bodyBytes []byte) bool {
	path := c.Request.URL.Path
	if strings.HasSuffix(path, ":streamGenerateContent") {
		return true
	}

	// Also check for standard streaming indicators as a fallback.
	if strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
		return true
	}
	if c.Query("stream") == "true" {
		return true
	}

	type streamPayload struct {
		Stream bool `json:"stream"`
	}
	var p streamPayload
	if err := json.Unmarshal(bodyBytes, &p); err == nil {
		return p.Stream
	}

	return false
}

func (ch *GeminiChannel) ExtractModel(c *gin.Context, bodyBytes []byte) string {
	// gemini format
	path := c.Request.URL.Path
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "models" && i+1 < len(parts) {
			modelPart := parts[i+1]
			return strings.Split(modelPart, ":")[0]
		}
	}

	// openai format
	type modelPayload struct {
		Model string `json:"model"`
	}
	var p modelPayload
	if err := json.Unmarshal(bodyBytes, &p); err == nil && p.Model != "" {
		return p.Model
	}

	return ""
}

// ValidateKey checks if the given API key is valid by making a generateContent request.
func (ch *GeminiChannel) ValidateKey(ctx context.Context, apiKey *models.APIKey, group *models.Group) (bool, error) {
	reqURL, err := ch.BuildUpstreamURL(&url.URL{
		Path: "/proxy/" + group.Name + "/v1beta/models/" + ch.TestModel + ":generateContent",
	}, group.Name)
	if err != nil {
		return false, fmt.Errorf("failed to create gemini validation path: %w", err)
	}

	payload := gin.H{
		"contents": []gin.H{
			{
				"role": "user",
				"parts": []gin.H{
					{"text": "hi"},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal validation payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create validation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	ch.ModifyRequest(req, apiKey, group)

	// Apply custom header rules if available
	if len(group.HeaderRuleList) > 0 {
		headerCtx := utils.NewHeaderVariableContext(group, apiKey)
		utils.ApplyHeaderRules(req, group.HeaderRuleList, headerCtx)
	}

	resp, err := ch.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send validation request: %w", err)
	}
	defer resp.Body.Close()

	// Any 2xx status code indicates the key is valid.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}

	// For non-200 responses, parse the body to provide a more specific error reason.
	errorBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("key is invalid (status %d), but failed to read error body: %w", resp.StatusCode, err)
	}

	// Use the new parser to extract a clean error message.
	parsedError := app_errors.ParseUpstreamError(errorBody)

	return false, fmt.Errorf("[status %d] %s", resp.StatusCode, parsedError)
}

// ApplyModelRedirect overrides the default implementation for Gemini channel.
func (ch *GeminiChannel) ApplyModelRedirect(req *http.Request, bodyBytes []byte, group *models.Group) ([]byte, error) {
	if len(group.ModelRedirectMap) == 0 {
		return bodyBytes, nil
	}

	if strings.Contains(req.URL.Path, "v1beta/openai") {
		return ch.BaseChannel.ApplyModelRedirect(req, bodyBytes, group)
	}

	return ch.applyNativeFormatRedirect(req, bodyBytes, group)
}

// applyNativeFormatRedirect handles model redirection for Gemini native format.
func (ch *GeminiChannel) applyNativeFormatRedirect(req *http.Request, bodyBytes []byte, group *models.Group) ([]byte, error) {
	path := req.URL.Path
	parts := strings.Split(path, "/")

	for i, part := range parts {
		if part == "models" && i+1 < len(parts) {
			modelPart := parts[i+1]
			originalModel := strings.Split(modelPart, ":")[0]

			if targetModel, found := group.ModelRedirectMap[originalModel]; found {
				suffix := ""
				if colonIndex := strings.Index(modelPart, ":"); colonIndex != -1 {
					suffix = modelPart[colonIndex:]
				}
				parts[i+1] = targetModel + suffix
				req.URL.Path = strings.Join(parts, "/")

				logrus.WithFields(logrus.Fields{
					"group":          group.Name,
					"original_model": originalModel,
					"target_model":   targetModel,
					"channel":        "gemini_native",
					"original_path":  path,
					"new_path":       req.URL.Path,
				}).Debug("Model redirected")

				return bodyBytes, nil
			}

			if group.ModelRedirectStrict {
				return nil, fmt.Errorf("model '%s' is not configured in redirect rules", originalModel)
			}
			return bodyBytes, nil
		}
	}

	return bodyBytes, nil
}

// TransformModelList transforms the model list response based on redirect rules.
func (ch *GeminiChannel) TransformModelList(req *http.Request, bodyBytes []byte, group *models.Group) (map[string]any, error) {
	var response map[string]any
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		logrus.WithError(err).Debug("Failed to parse model list response, returning empty")
		return nil, err
	}

	if modelsInterface, hasModels := response["models"]; hasModels {
		return ch.transformGeminiNativeFormat(req, response, modelsInterface, group), nil
	}

	if _, hasData := response["data"]; hasData {
		return ch.BaseChannel.TransformModelList(req, bodyBytes, group)
	}

	return response, nil
}

// transformGeminiNativeFormat transforms Gemini native format model list
func (ch *GeminiChannel) transformGeminiNativeFormat(req *http.Request, response map[string]any, modelsInterface any, group *models.Group) map[string]any {
	upstreamModels, ok := modelsInterface.([]any)
	if !ok {
		return response
	}

	configuredModels := buildConfiguredGeminiModels(group.ModelRedirectMap)

	// Strict mode: return only configured models (whitelist)
	if group.ModelRedirectStrict {
		response["models"] = configuredModels
		delete(response, "nextPageToken")

		logrus.WithFields(logrus.Fields{
			"group":       group.Name,
			"model_count": len(configuredModels),
			"strict_mode": true,
			"format":      "gemini_native",
		}).Debug("Model list returned (strict mode - configured models only)")

		return response
	}

	// Non-strict mode: merge upstream + configured models (upstream priority)
	var merged []any
	if isFirstPage(req) {
		merged = mergeGeminiModelLists(upstreamModels, configuredModels)
		logrus.WithFields(logrus.Fields{
			"group":            group.Name,
			"upstream_count":   len(upstreamModels),
			"configured_count": len(configuredModels),
			"merged_count":     len(merged),
			"strict_mode":      false,
			"format":           "gemini_native",
			"page":             "first",
		}).Debug("Model list merged (non-strict mode - first page)")
	} else {
		merged = upstreamModels
		logrus.WithFields(logrus.Fields{
			"group":          group.Name,
			"upstream_count": len(upstreamModels),
			"strict_mode":    false,
			"format":         "gemini_native",
			"page":           "subsequent",
		}).Debug("Model list returned (non-strict mode - subsequent page)")
	}

	response["models"] = merged
	return response
}

// buildConfiguredGeminiModels builds a list of models from redirect rules for Gemini format
func buildConfiguredGeminiModels(redirectMap map[string]string) []any {
	if len(redirectMap) == 0 {
		return []any{}
	}

	models := make([]any, 0, len(redirectMap))
	for sourceModel := range redirectMap {
		modelName := sourceModel
		if !strings.HasPrefix(sourceModel, "models/") {
			modelName = "models/" + sourceModel
		}

		models = append(models, map[string]any{
			"name":                       modelName,
			"displayName":                sourceModel,
			"supportedGenerationMethods": []string{"generateContent"},
		})
	}
	return models
}

// mergeGeminiModelLists merges upstream and configured model lists for Gemini format
func mergeGeminiModelLists(upstream []any, configured []any) []any {
	upstreamNames := make(map[string]bool)
	for _, item := range upstream {
		if modelObj, ok := item.(map[string]any); ok {
			if modelName, ok := modelObj["name"].(string); ok {
				upstreamNames[modelName] = true
				cleanName := strings.TrimPrefix(modelName, "models/")
				upstreamNames[cleanName] = true
			}
		}
	}

	// Start with all upstream models
	result := make([]any, len(upstream))
	copy(result, upstream)

	// Add configured models that don't exist in upstream
	for _, item := range configured {
		if modelObj, ok := item.(map[string]any); ok {
			if modelName, ok := modelObj["name"].(string); ok {
				cleanName := strings.TrimPrefix(modelName, "models/")
				if !upstreamNames[modelName] && !upstreamNames[cleanName] {
					result = append(result, item)
				}
			}
		}
	}

	return result
}

// isFirstPage checks if this is the first page of a Gemini paginated request
func isFirstPage(req *http.Request) bool {
	pageToken := req.URL.Query().Get("pageToken")
	return pageToken == ""
}

func trimProxyGroupPrefix(requestPath, groupName string) string {
	proxyPrefix := "/proxy/" + groupName
	return strings.TrimPrefix(requestPath, proxyPrefix)
}

func vertexPublisherBasePath(base *url.URL) (string, bool) {
	basePath := normalizeBasePath(base.Path)
	if basePath == "/v1/publishers/google" || basePath == "/v1beta1/publishers/google" {
		return basePath, true
	}

	if !strings.EqualFold(base.Hostname(), "aiplatform.googleapis.com") {
		return "", false
	}

	switch basePath {
	case "/", "/v1":
		return "/v1/publishers/google", true
	case "/v1beta1":
		return "/v1beta1/publishers/google", true
	default:
		return "", false
	}
}

func buildVertexPublisherPath(basePath, requestPath string) string {
	if hasPathPrefix(requestPath, basePath) {
		return ensureLeadingSlash(requestPath)
	}

	if modelPath, ok := geminiNativeModelPath(requestPath); ok {
		return joinURLPath(basePath, modelPath)
	}

	return joinURLPath(basePath, requestPath)
}

func geminiNativeModelPath(requestPath string) (string, bool) {
	parts := strings.Split(strings.TrimLeft(requestPath, "/"), "/")
	if len(parts) == 0 {
		return "", false
	}

	if parts[0] == "models" {
		return strings.Join(parts, "/"), true
	}

	if len(parts) >= 2 && isGeminiNativeVersion(parts[0]) && parts[1] == "models" {
		return strings.Join(parts[1:], "/"), true
	}

	return "", false
}

func isGeminiNativeVersion(segment string) bool {
	return segment == "v1" || segment == "v1beta" || segment == "v1beta1"
}

func joinURLPath(basePath, requestPath string) string {
	basePath = ensureLeadingSlash(basePath)
	requestPath = strings.TrimLeft(requestPath, "/")
	if requestPath == "" {
		return basePath
	}
	if basePath == "/" {
		return "/" + requestPath
	}
	return strings.TrimRight(basePath, "/") + "/" + requestPath
}

func hasPathPrefix(pathValue, prefix string) bool {
	pathValue = ensureLeadingSlash(pathValue)
	prefix = strings.TrimRight(ensureLeadingSlash(prefix), "/")
	return pathValue == prefix || strings.HasPrefix(pathValue, prefix+"/")
}

func ensureLeadingSlash(pathValue string) string {
	if pathValue == "" {
		return "/"
	}
	if strings.HasPrefix(pathValue, "/") {
		return pathValue
	}
	return "/" + pathValue
}

func normalizeBasePath(pathValue string) string {
	normalized := strings.TrimRight(ensureLeadingSlash(pathValue), "/")
	if normalized == "" {
		return "/"
	}
	return normalized
}
