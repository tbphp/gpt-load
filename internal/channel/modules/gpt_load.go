package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func GPTLoad() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.GPTLoad,
			Name:        "GPT-Load",
			Mark:        "GL",
			Icon:        "gpt-load",
			SearchTerms: []string{"gpt-load", "gptload", "gpt load", "gl", "gpt-load v2"},
			Description: "GPT-Load multi-protocol gateway",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
				Required: true, Normalizer: spec.NormalizeBaseURL,
			}},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "AccessKey", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:   spec.ProviderMultiProtocolGateway,
				EndpointPolicy: spec.EndpointRequiredBaseURL,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewResponsesCreateRoute(execution.RouteNative, spec.ResponsesStoreHandlingUpstreamManaged),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesRetrieve, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesDelete, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCancel, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputItems, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCompact, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesPassthrough, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationListModels, execution.RouteNative),
			},
		},
	}
}
