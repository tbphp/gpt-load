package modules

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func apiKeyConnection() Connection {
	return Connection{Type: ConnectionAPIKey, CredentialInput: "batch_text"}
}

func subscriptionConnection(methods ...string) Connection {
	return Connection{
		Type:                 ConnectionSubscription,
		CredentialInput:      "authorization",
		AuthorizationMethods: append([]string(nil), methods...),
	}
}

func apiKeyFields() []Field {
	return []Field{{
		Key: "api_key", Label: "API Key", InputKind: InputSecret,
		Required: true, Sensitive: true, Normalizer: normalizeNonEmpty,
	}}
}

func optionalBaseURLFields() []Field {
	return []Field{{
		Key: "base_url", Label: "Base URL", InputKind: InputURL,
		Normalizer: normalizeBaseURL,
	}}
}

func requiredBaseURLFields() []Field {
	fields := optionalBaseURLFields()
	fields[0].Required = true
	return fields
}

func secretField(key, label string) Field {
	return Field{
		Key: key, Label: label, InputKind: InputSecret,
		Sensitive: true, Normalizer: normalizeNonEmpty,
	}
}

func normalizeNonEmpty(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("must not be empty")
	}
	return value, nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("must not be empty")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("must not contain query parameters")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if (parsed.Scheme == "https" && parsed.Port() == "443") ||
		(parsed.Scheme == "http" && parsed.Port() == "80") {
		hostname := parsed.Hostname()
		if strings.Contains(hostname, ":") {
			hostname = "[" + hostname + "]"
		}
		parsed.Host = hostname
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.ForceQuery = false
	return parsed.String(), nil
}

func normalizeCloudIdentifier(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return "", fmt.Errorf("must be between 1 and 255 bytes")
	}
	for _, character := range value {
		if character <= ' ' || character == 0x7f {
			return "", fmt.Errorf("must not contain whitespace or control characters")
		}
	}
	return value, nil
}

func normalizeServiceAccountJSON(value string) (string, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	for _, key := range []string{"type", "project_id", "client_email", "private_key"} {
		raw, exists := object[key]
		if !exists {
			return "", fmt.Errorf("must contain %s", key)
		}
		var field string
		if json.Unmarshal(raw, &field) != nil || strings.TrimSpace(field) == "" {
			return "", fmt.Errorf("must contain non-empty %s", key)
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	return string(encoded), nil
}

func route(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	mode execution.RouteMode,
) Route {
	return Route{ClientProtocol: clientProtocol, Operation: operation, Mode: mode}
}

func standardRoutes(
	completionsMode execution.RouteMode,
	responsesMode execution.RouteMode,
	anthropicMode execution.RouteMode,
	geminiMode execution.RouteMode,
) []Route {
	return []Route{
		route(protocol.OpenAICompletions, execution.OperationChatCompletion, completionsMode),
		route(protocol.OpenAICompletions, execution.OperationListModels, completionsMode),
		route(protocol.OpenAICompletions, execution.OperationProbe, completionsMode),
		route(protocol.OpenAIResponses, execution.OperationResponsesCreate, responsesMode),
		route(protocol.OpenAIResponses, execution.OperationListModels, responsesMode),
		route(protocol.OpenAIResponses, execution.OperationProbe, responsesMode),
		route(protocol.Anthropic, execution.OperationChatCompletion, anthropicMode),
		route(protocol.Anthropic, execution.OperationListModels, anthropicMode),
		route(protocol.Anthropic, execution.OperationProbe, anthropicMode),
		route(protocol.Gemini, execution.OperationChatCompletion, geminiMode),
		route(protocol.Gemini, execution.OperationListModels, geminiMode),
		route(protocol.Gemini, execution.OperationProbe, geminiMode),
	}
}

func openAIOfficialRoutes() []Route {
	routes := standardRoutes(
		execution.RouteNative,
		execution.RouteNative,
		execution.RouteConverted,
		execution.RouteConverted,
	)
	for _, operation := range []execution.Operation{
		execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough,
	} {
		routes = append(routes, route(protocol.OpenAIResponses, operation, execution.RouteNative))
	}
	return routes
}

func anthropicOfficialRoutes() []Route {
	return standardRoutes(
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteNative,
		execution.RouteConverted,
	)
}

func geminiOfficialRoutes() []Route {
	return standardRoutes(
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteNative,
	)
}

func allConvertedRoutes() []Route {
	return standardRoutes(
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteConverted,
	)
}

func openAICompatibleRoutes() []Route {
	return standardRoutes(
		execution.RouteNative,
		execution.RouteConverted,
		execution.RouteConverted,
		execution.RouteConverted,
	)
}

func nativeOpenAIRoutes(nativeResponses bool) []Route {
	responsesMode := execution.RouteConverted
	if nativeResponses {
		responsesMode = execution.RouteNative
	}
	return standardRoutes(
		execution.RouteNative,
		responsesMode,
		execution.RouteConverted,
		execution.RouteConverted,
	)
}
