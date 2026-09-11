package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// RequestyDefaultBaseURL is the global Requesty router. Regional routers
// (EU, US, AP) accept the same API key and are exposed as URL hints so a
// group can pin a region through the Base URL override.
const RequestyDefaultBaseURL = "https://router.requesty.ai/v1"

func Requesty() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Requesty,
			Name:        "Requesty",
			Mark:        "RQ",
			Icon:        "requesty",
			SearchTerms: []string{"router", "requesty ai"},
			Description: "Managed API preset",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
				Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:      spec.ProviderOpenAICompatible,
				CatalogProviderID: "requesty",
				EndpointPolicy:    spec.EndpointFixedWithOverride,
				FixedBaseURL:      RequestyDefaultBaseURL,
				DefaultBaseURLs: []string{
					RequestyDefaultBaseURL,
					"https://router.eu.requesty.ai/v1",
					"https://router.us.requesty.ai/v1",
					"https://router.ap.requesty.ai/v1",
				},
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
			},
		},
	}
}
