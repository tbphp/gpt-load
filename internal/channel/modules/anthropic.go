package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func Anthropic() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Anthropic,
			Name:        "Anthropic",
			Mark:        "AN",
			Icon:        "anthropic",
			SearchTerms: []string{"claude"},
			Description: "Anthropic official API",
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
				ProviderKind:      spec.ProviderAnthropic,
				CatalogProviderID: "anthropic",
				EndpointPolicy:    spec.EndpointSDKDefault,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
			},
		},
	}
}
