package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
)

func (ps *ProxyServer) applyParamOverrides(bodyBytes []byte, group *models.Group) ([]byte, error) {
	if len(group.ParamOverrides) == 0 || len(bodyBytes) == 0 {
		return bodyBytes, nil
	}

	var requestData map[string]any
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		logrus.Warnf("failed to unmarshal request body for param override, passing through: %v", err)
		return bodyBytes, nil
	}

	for key, value := range group.ParamOverrides {
		requestData[key] = value
	}

	return json.Marshal(requestData)
}

func applyAnthropicSystemPromptCount(bodyBytes []byte, group *models.Group) []byte {
	if group == nil || !strings.EqualFold(group.ChannelType, "anthropic") {
		return bodyBytes
	}
	targetCount := group.AnthropicSystemPromptCount
	if targetCount <= 0 {
		return bodyBytes
	}
	if targetCount > models.MaxAnthropicSystemPromptCount {
		targetCount = models.MaxAnthropicSystemPromptCount
	}

	var requestData map[string]any
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		logrus.Warnf("failed to unmarshal anthropic request body for system prompt count, passing through: %v", err)
		return bodyBytes
	}

	systemBlocks := make([]any, 0, targetCount)
	if system, exists := requestData["system"]; exists {
		if items, ok := asAnyArray(system); ok {
			systemBlocks = append(systemBlocks, items...)
		} else {
			systemBlocks = append(systemBlocks, system)
		}
	}

	textValues := make([]string, 0, len(systemBlocks))
	for _, block := range systemBlocks {
		if text, ok := extractSystemText(block); ok {
			textValues = append(textValues, text)
		}
	}

	if len(textValues) >= targetCount {
		mergedText := strings.Join(textValues[targetCount-1:], "\n\n")
		merged := make([]any, 0, len(systemBlocks)-(len(textValues)-targetCount))
		textIndex := 0
		for _, block := range systemBlocks {
			if _, ok := extractSystemText(block); !ok {
				merged = append(merged, block)
				continue
			}
			if textIndex < targetCount-1 {
				merged = append(merged, block)
			} else if textIndex == targetCount-1 {
				merged = append(merged, setSystemText(block, mergedText))
			}
			textIndex++
		}
		requestData["system"] = merged
	} else {
		for len(textValues) < targetCount {
			systemBlocks = append(systemBlocks, map[string]any{
				"type": "text",
				"text": "",
			})
			textValues = append(textValues, "")
		}
		requestData["system"] = systemBlocks
	}

	out, err := json.Marshal(requestData)
	if err != nil {
		logrus.Warnf("failed to marshal anthropic request body for system prompt count, passing through: %v", err)
		return bodyBytes
	}
	return out
}

func asAnyArray(value any) ([]any, bool) {
	items, ok := value.([]any)
	return items, ok
}

func extractSystemText(block any) (string, bool) {
	switch v := block.(type) {
	case string:
		return v, true
	case map[string]any:
		if blockType, ok := v["type"].(string); ok && blockType != "" && !strings.EqualFold(blockType, "text") {
			return "", false
		}
		text, ok := v["text"].(string)
		if !ok {
			return "", false
		}
		return text, true
	default:
		return "", false
	}
}

func setSystemText(block any, text string) any {
	switch v := block.(type) {
	case string:
		return text
	case map[string]any:
		updated := make(map[string]any, len(v))
		for key, value := range v {
			updated[key] = value
		}
		updated["text"] = text
		return updated
	default:
		return map[string]any{
			"type": "text",
			"text": text,
		}
	}
}

// logUpstreamError provides a centralized way to log errors from upstream interactions.
func logUpstreamError(context string, err error) {
	if err == nil {
		return
	}
	if app_errors.IsIgnorableError(err) {
		logrus.Debugf("Ignorable upstream error in %s: %v", context, err)
	} else {
		logrus.Errorf("Upstream error in %s: %v", context, err)
	}
}

// handleGzipCompression checks for gzip encoding and decompresses the body if necessary.
func handleGzipCompression(resp *http.Response, bodyBytes []byte) []byte {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		reader, gzipErr := gzip.NewReader(bytes.NewReader(bodyBytes))
		if gzipErr != nil {
			logrus.Warnf("Failed to create gzip reader for error body: %v", gzipErr)
			return bodyBytes
		}
		defer reader.Close()

		decompressedBody, readAllErr := io.ReadAll(reader)
		if readAllErr != nil {
			logrus.Warnf("Failed to decompress gzip error body: %v", readAllErr)
			return bodyBytes
		}
		return decompressedBody
	}
	return bodyBytes
}
