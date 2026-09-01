package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func NewAPI() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.NewAPI,
			Name:        "New API",
			Mark:        "NA",
			Icon:        "new-api",
			Description: "New API multi-protocol gateway",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
				Required: true, Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:   spec.ProviderNewAPI,
				EndpointPolicy: spec.EndpointRequiredBaseURL,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCompact, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
			},
		},
	}
}
