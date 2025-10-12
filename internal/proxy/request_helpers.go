package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
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
		// Check if this is a mapping mode (key ends with @map)
		if strings.HasSuffix(key, "@map") {
			actualKey := strings.TrimSuffix(key, "@map")
			applyMapping(requestData, actualKey, value)
		} else {
			// Direct override mode (backward compatible)
			requestData[key] = value
		}
	}

	return json.Marshal(requestData)
}

// applyMapping applies value mapping for a specific parameter
func applyMapping(requestData map[string]any, key string, mappingValue any) {
	mappings, ok := mappingValue.(map[string]any)
	if !ok {
		logrus.Warnf("invalid mapping config for key %s, expected map", key)
		return
	}

	// Get original value from request
	originalValue, exists := requestData[key]
	if !exists {
		// If parameter doesn't exist and @default is provided, use it
		if defaultVal, hasDefault := mappings["@default"]; hasDefault {
			requestData[key] = defaultVal
			logrus.Debugf("set default for %s: %v", key, defaultVal)
		}
		// Otherwise, do nothing
		return
	}

	// Convert original value to string for matching
	originalStr := fmt.Sprintf("%v", originalValue)

	// Look up mapping
	if newValue, found := mappings[originalStr]; found {
		requestData[key] = newValue
		logrus.Debugf("mapped %s: %s -> %v", key, originalStr, newValue)
	} else if defaultVal, hasDefault := mappings["@default"]; hasDefault {
		// If no match found but @default exists, use it
		requestData[key] = defaultVal
		logrus.Debugf("mapped %s: %s -> %v (default)", key, originalStr, defaultVal)
	}
	// If no match and no @default, keep original value unchanged
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
