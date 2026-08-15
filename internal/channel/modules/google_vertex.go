package modules

import (
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const vertexGeminiRouteResolver RouteResolverID = "vertex_gemini_native"

func googleVertexModule() Module {
	return Module{
		Definition: Definition{
			ID:          GoogleVertex,
			Name:        "Google Vertex AI",
			Mark:        "VX",
			Icon:        "vertexai",
			SearchTerms: []string{"google", "gcp", "vertex"},
			Description: "Google Cloud Vertex AI",
			Connection:  apiKeyConnection(),
			Params: []Field{{
				Key: "location", Label: "Vertex location", InputKind: InputText,
				Default: "global", Normalizer: normalizeCloudIdentifier,
			}},
			Credentials: []Field{{
				Key: "service_account_json", Label: "Service account JSON", InputKind: InputSecret,
				Required: true, Sensitive: true, Normalizer: normalizeServiceAccountJSON,
			}},
			Provider: ProviderBinding{
				ProviderKind:      ProviderGoogleVertex,
				CatalogProviderID: "google-vertex",
				EndpointPolicy:    EndpointCloudParams,
			},
			Routes: []Route{
				route(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				route(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteConverted),
				route(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteConverted),
				route(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteConverted),
				route(protocol.OpenAIResponses, execution.OperationListModels, execution.RouteConverted),
				route(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				route(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				route(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
				route(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
				{
					ClientProtocol: protocol.Gemini,
					Operation:      execution.OperationChatCompletion,
					Mode:           execution.RouteConverted,
					Resolver:       vertexGeminiRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteConverted, execution.RouteNative},
				},
				route(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				{
					ClientProtocol: protocol.Gemini,
					Operation:      execution.OperationProbe,
					Mode:           execution.RouteConverted,
					Resolver:       vertexGeminiRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteConverted, execution.RouteNative},
				},
			},
		},
		Extensions: Extensions{RouteResolvers: map[RouteResolverID]RouteResolver{
			vertexGeminiRouteResolver: resolveVertexGeminiRoute,
		}},
	}
}

func resolveVertexGeminiRoute(upstreamModel string, defaultMode execution.RouteMode) execution.RouteMode {
	_, native := NormalizeVertexGeminiModel(upstreamModel)
	if native {
		return execution.RouteNative
	}
	return defaultMode
}

// NormalizeVertexGeminiModel returns the Vertex resource ID for a Gemini,
// Gemma, or numeric custom endpoint that can preserve the Gemini wire format.
func NormalizeVertexGeminiModel(upstreamModel string) (string, bool) {
	model := strings.TrimSpace(upstreamModel)
	lower := strings.ToLower(model)
	for _, prefix := range []string{"publishers/google/models/", "google/"} {
		if strings.HasPrefix(lower, prefix) {
			model = model[len(prefix):]
			lower = strings.ToLower(model)
			break
		}
	}
	if model == "" || strings.ContainsAny(model, "/\\:?#") {
		return "", false
	}
	allDigits := true
	for index := 0; index < len(model); index++ {
		if model[index] < '0' || model[index] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits || strings.Contains(lower, "gemini") || strings.Contains(lower, "gemma") {
		return model, true
	}
	return "", false
}
