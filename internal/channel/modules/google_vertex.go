package modules

import (
	"strings"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	vertexGeminiRouteResolver     spec.RouteResolverID = "vertex_gemini_native"
	vertexEmbeddingsRouteResolver spec.RouteResolverID = "vertex_embeddings"
)

func GoogleVertex() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.GoogleVertex,
			Name:        "Google Vertex AI",
			Mark:        "VX",
			Icon:        "vertexai",
			SearchTerms: []string{"google", "gcp", "vertex"},
			Description: "Google Cloud Vertex AI",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "location", Label: "Vertex location", InputKind: spec.InputText,
				Default: "global", Normalizer: spec.NormalizeCloudIdentifier,
			}},
			Credentials: []spec.Field{{
				Key: "service_account_json", Label: "Service account JSON", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeServiceAccountJSON,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:      spec.ProviderGoogleVertex,
				CatalogProviderID: "google-vertex",
				EndpointPolicy:    spec.EndpointCloudParams,
			},
			Routes: []spec.Route{
				{
					ClientProtocol: protocol.OpenAIEmbeddings,
					Operation:      execution.OperationEmbeddingsCreate,
					Mode:           execution.RouteConverted,
					Resolver:       vertexEmbeddingsRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteConverted},
				},
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteConverted),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
				{
					ClientProtocol: protocol.Gemini,
					Operation:      execution.OperationChatCompletion,
					Mode:           execution.RouteConverted,
					Resolver:       vertexGeminiRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteConverted, execution.RouteNative},
				},
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				{
					ClientProtocol: protocol.Gemini,
					Operation:      execution.OperationProbe,
					Mode:           execution.RouteConverted,
					Resolver:       vertexGeminiRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteConverted, execution.RouteNative},
				},
				{
					ClientProtocol: protocol.GeminiEmbeddings,
					Operation:      execution.OperationEmbeddingsCreate,
					Mode:           execution.RouteNative,
					Resolver:       vertexEmbeddingsRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteNative},
				},
				{
					ClientProtocol: protocol.GeminiEmbeddings,
					Operation:      execution.OperationProbe,
					Mode:           execution.RouteNative,
					Resolver:       vertexEmbeddingsRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteNative},
				},
			},
		},
		Extensions: spec.Extensions{RouteResolvers: map[spec.RouteResolverID]spec.RouteResolver{
			vertexGeminiRouteResolver:     resolveVertexGeminiRoute,
			vertexEmbeddingsRouteResolver: resolveVertexEmbeddingsRoute,
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

func resolveVertexEmbeddingsRoute(upstreamModel string, defaultMode execution.RouteMode) execution.RouteMode {
	if _, supported := NormalizeVertexEmbeddingsModel(upstreamModel); !supported {
		return ""
	}
	return defaultMode
}

// NormalizeVertexEmbeddingsModel 沿用 Gemini 模型格式，但向量操作不支持数字自定义端点。
func NormalizeVertexEmbeddingsModel(upstreamModel string) (string, bool) {
	model, ok := NormalizeVertexGeminiModel(upstreamModel)
	if !ok || strings.Trim(model, "0123456789") == "" {
		return "", false
	}
	return model, true
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
