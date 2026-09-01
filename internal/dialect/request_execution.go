package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func chatExecutionMetadata(
	clientProtocol protocol.Protocol,
	body []byte,
) (execution.Operation, execution.RouteRequirement) {
	nativeResource := chatRequiresNativeRoute(clientProtocol, body)
	return execution.OperationChatCompletion, routeRequirement(nativeResource)
}

func responsesExecutionMetadata(
	request *ParsedRequest,
) (
	execution.Operation,
	execution.RouteRequirement,
	execution.ResponsesStorePreference,
) {
	operation := responsesOperation(request)
	nativeResource := operation == execution.OperationResponsesRetrieve ||
		operation == execution.OperationResponsesDelete ||
		operation == execution.OperationResponsesCancel ||
		operation == execution.OperationResponsesInputItems ||
		operation == execution.OperationResponsesPassthrough
	if operation == execution.OperationResponsesCreate {
		routeRequirement, storePreference := responsesCreateRequirements(request.Body)
		return operation, routeRequirement, storePreference
	}
	return operation, routeRequirement(nativeResource), execution.ResponsesStorePreferenceNone
}

func routeRequirement(native bool) execution.RouteRequirement {
	if native {
		return execution.RouteRequirementNative
	}
	return execution.RouteRequirementAny
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

func responsesCreateRequirements(
	body []byte,
) (execution.RouteRequirement, execution.ResponsesStorePreference) {
	root, ok := decodeExecutionFeatureObject(body)
	if !ok {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	if hasMeaningfulField(root, "previous_response_id") ||
		hasMeaningfulField(root, "conversation") {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	if background, ok := root["background"].(bool); ok && background {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	if responsesInputReferencesProviderResource(root["input"]) ||
		responsesToolsReferenceProviderResource(root["tools"]) {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	value, exists := root["store"]
	if !exists {
		return execution.RouteRequirementAny, execution.ResponsesStorePreferencePreferStored
	}
	if value == nil {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	store, ok := value.(bool)
	if !ok {
		return execution.RouteRequirementNative, execution.ResponsesStorePreferenceNone
	}
	if store {
		return execution.RouteRequirementAny, execution.ResponsesStorePreferencePreferStored
	}
	return execution.RouteRequirementAny, execution.ResponsesStorePreferenceNone
}

func chatRequiresNativeRoute(clientProtocol protocol.Protocol, body []byte) bool {
	root, ok := decodeExecutionFeatureObject(body)
	if !ok {
		return false
	}
	switch clientProtocol {
	case protocol.OpenAICompletions:
		return openAIChatMessagesReferenceProviderResource(root["messages"])
	case protocol.Gemini:
		return hasMeaningfulField(root, "cachedContent") ||
			hasMeaningfulField(root, "cached_content") ||
			geminiContentsReferenceProviderResource(root["contents"])
	case protocol.Anthropic:
		return hasMeaningfulField(root, "container") ||
			anthropicMessagesReferenceProviderResource(root["messages"])
	default:
		return false
	}
}

func openAIChatMessagesReferenceProviderResource(value any) bool {
	messages, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			continue
		}
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, ok := value.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			file, _ := block["file"].(map[string]any)
			if normalizeFeatureName(blockType) == "file" && hasMeaningfulField(file, "file_id") {
				return true
			}
		}
	}
	return false
}

func geminiContentsReferenceProviderResource(value any) bool {
	contents, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range contents {
		content, ok := value.(map[string]any)
		if !ok {
			continue
		}
		parts, _ := content["parts"].([]any)
		for _, value := range parts {
			part, ok := value.(map[string]any)
			if !ok {
				continue
			}
			fileData := objectField(part, "fileData", "file_data")
			fileURI, _ := stringField(fileData, "fileUri", "file_uri")
			if geminiProviderResourceURI(fileURI) {
				return true
			}
		}
	}
	return false
}

func objectField(object map[string]any, names ...string) map[string]any {
	for _, name := range names {
		if value, ok := object[name].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func stringField(object map[string]any, names ...string) (string, bool) {
	for _, name := range names {
		if value, ok := object[name].(string); ok {
			return value, true
		}
	}
	return "", false
}

func geminiProviderResourceURI(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	parsed, err := url.Parse(value)
	if err == nil {
		if strings.EqualFold(parsed.Scheme, "gs") {
			return true
		}
		if strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") &&
			strings.Contains(strings.ToLower(parsed.Path), "/files/") {
			return true
		}
	}
	relative := strings.TrimPrefix(strings.TrimSpace(value), "/")
	return strings.HasPrefix(strings.ToLower(relative), "files/") ||
		strings.HasPrefix(strings.ToLower(relative), "v1beta/files/")
}

func responsesInputReferencesProviderResource(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := item["type"].(string)
		switch normalizeFeatureName(itemType) {
		case "itemreference":
			if hasMeaningfulField(item, "id") {
				return true
			}
		case "codeinterpretercall", "computer_call", "computercall", "computercalloutput":
			if hasMeaningfulField(item, "container_id") {
				return true
			}
		}
		if responsesContentReferencesProviderResource(item["content"]) {
			return true
		}
	}
	return false
}

func responsesContentReferencesProviderResource(value any) bool {
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := block["type"].(string)
		switch normalizeFeatureName(blockType) {
		case "inputfile", "inputimage":
			if hasMeaningfulField(block, "file_id") {
				return true
			}
		}
	}
	return false
}

func responsesToolsReferenceProviderResource(value any) bool {
	tools, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		switch normalizeFeatureName(toolType) {
		case "filesearch":
			if hasMeaningfulField(tool, "vector_store_ids") {
				return true
			}
		case "codeinterpreter":
			if hasMeaningfulField(tool, "container") || hasMeaningfulField(tool, "container_id") {
				return true
			}
		case "imagegeneration":
			mask, _ := tool["input_image_mask"].(map[string]any)
			if hasMeaningfulField(mask, "file_id") {
				return true
			}
		}
	}
	return false
}

func anthropicMessagesReferenceProviderResource(value any) bool {
	messages, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range messages {
		message, ok := value.(map[string]any)
		if !ok {
			continue
		}
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, ok := value.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if normalizeFeatureName(blockType) == "containerupload" &&
				hasMeaningfulField(block, "file_id") {
				return true
			}
			source, _ := block["source"].(map[string]any)
			sourceType, _ := source["type"].(string)
			if normalizeFeatureName(sourceType) == "file" &&
				hasMeaningfulField(source, "file_id") {
				return true
			}
		}
	}
	return false
}

func normalizeFeatureName(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	return strings.ReplaceAll(value, "-", "")
}
