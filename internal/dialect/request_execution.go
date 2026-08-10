package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/reasoning"
)

func chatExecutionMetadata(
	body []byte,
	stream bool,
	reasoningConfig reasoning.Config,
) (execution.Operation, execution.FeatureSet) {
	return execution.OperationChatCompletion,
		inspectRequiredFeatures(body, stream, reasoningConfig.RequiresCapability(), false)
}

func responsesExecutionMetadata(
	request *ParsedRequest,
	stream bool,
	reasoningConfig reasoning.Config,
) (execution.Operation, execution.FeatureSet) {
	operation := responsesOperation(request)
	nativeResource := operation == execution.OperationResponsesRetrieve ||
		operation == execution.OperationResponsesDelete ||
		operation == execution.OperationResponsesCancel ||
		operation == execution.OperationResponsesInputItems ||
		operation == execution.OperationResponsesPassthrough
	if operation == execution.OperationResponsesCreate {
		nativeResource = responsesCreateStoresResource(request.Body)
	}
	return operation, inspectRequiredFeatures(
		request.Body,
		stream,
		reasoningConfig.RequiresCapability(),
		nativeResource,
	)
}

func responsesOperation(request *ParsedRequest) execution.Operation {
	if request == nil {
		return execution.OperationResponsesPassthrough
	}
	switch {
	case request.Method == http.MethodPost && request.Path == openAIResponsesPath:
		return execution.OperationResponsesCreate
	case request.Method == http.MethodPost && request.Path == openAIResponsesCompactPath:
		return execution.OperationResponsesCompact
	case request.Method == http.MethodPost && request.Path == openAIResponsesPath+"/input_tokens":
		return execution.OperationResponsesInputTokens
	}

	resourcePath := strings.TrimPrefix(request.Path, openAIResponsesPath+"/")
	segments := strings.Split(resourcePath, "/")
	if resourcePath == request.Path || len(segments) == 0 || segments[0] == "" {
		return execution.OperationResponsesPassthrough
	}
	switch {
	case len(segments) == 1 && request.Method == http.MethodGet:
		return execution.OperationResponsesRetrieve
	case len(segments) == 1 && request.Method == http.MethodDelete:
		return execution.OperationResponsesDelete
	case len(segments) == 2 && segments[1] == "cancel" && request.Method == http.MethodPost:
		return execution.OperationResponsesCancel
	case len(segments) == 2 && segments[1] == "input_items" && request.Method == http.MethodGet:
		return execution.OperationResponsesInputItems
	default:
		return execution.OperationResponsesPassthrough
	}
}

func inspectRequiredFeatures(
	body []byte,
	stream bool,
	reasoningPresent bool,
	nativeResource bool,
) execution.FeatureSet {
	features := make([]execution.Feature, 0, 6)
	if stream {
		features = append(features, execution.FeatureStreaming)
	}
	if reasoningPresent {
		features = append(features, execution.FeatureReasoning)
	}

	root, ok := decodeExecutionFeatureObject(body)
	if ok {
		if hasMeaningfulField(root, "tools") ||
			hasMeaningfulField(root, "tool_choice") ||
			hasMeaningfulField(root, "toolChoice") {
			features = append(features, execution.FeatureTools)
		}
		if containsMultimodalValue(root) {
			features = append(features, execution.FeatureMultimodal)
		}
		if containsStructuredOutput(root) {
			features = append(features, execution.FeatureStructuredOutput)
		}
	}
	if nativeResource {
		features = append(features, execution.FeatureNativeResourceSemantics)
	}
	result, err := execution.NewFeatureSet(features...)
	if err != nil {
		return execution.FeatureSet{}
	}
	return result
}

func decodeExecutionFeatureObject(body []byte) (map[string]any, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

func hasMeaningfulField(object map[string]any, field string) bool {
	value, ok := object[field]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	case string:
		return typed != ""
	default:
		return true
	}
}

func containsMultimodalValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalizedKey := normalizeFeatureName(key)
			if normalizedKey == "inlinedata" ||
				normalizedKey == "filedata" ||
				normalizedKey == "imageurl" {
				return true
			}
			if normalizedKey == "type" {
				if text, ok := child.(string); ok && multimodalType(text) {
					return true
				}
			}
			if normalizedKey == "modalities" && containsNonTextModality(child) {
				return true
			}
			if containsMultimodalValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsMultimodalValue(child) {
				return true
			}
		}
	}
	return false
}

func multimodalType(value string) bool {
	switch normalizeFeatureName(value) {
	case "image", "imageurl", "inputimage", "inputaudio", "file", "inputfile", "document":
		return true
	default:
		return false
	}
}

func containsNonTextModality(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		text, ok := item.(string)
		if ok && !strings.EqualFold(strings.TrimSpace(text), "text") {
			return true
		}
	}
	return false
}

func containsStructuredOutput(root map[string]any) bool {
	if hasMeaningfulField(root, "response_format") {
		return true
	}
	for _, parentName := range []string{
		"text",
		"output_config",
		"outputConfig",
		"generation_config",
		"generationConfig",
	} {
		parent, ok := root[parentName].(map[string]any)
		if !ok {
			continue
		}
		for key, value := range parent {
			switch normalizeFeatureName(key) {
			case "format", "responseschema", "responsejsonschema", "responsemimetype":
				if value != nil {
					return true
				}
			}
		}
	}
	return false
}

func responsesCreateStoresResource(body []byte) bool {
	root, ok := decodeExecutionFeatureObject(body)
	if !ok {
		return true
	}
	value, exists := root["store"]
	if !exists || value == nil {
		return true
	}
	store, ok := value.(bool)
	return !ok || store
}

func normalizeFeatureName(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	return strings.ReplaceAll(value, "-", "")
}
