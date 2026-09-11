package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func OpenAICompatible() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.OpenAICompatible,
			Name:        "OpenAI Compatible",
			Mark:        "OC",
			Icon:        "compatible",
			SearchTerms: []string{"custom", "proxy", "gateway"},
			Description: "Custom OpenAI-compatible API",
			Connection: spec.Connection{
				Type:            spec.ConnectionAPIKey,
				CredentialInput: "batch_text",
			},
			Params: []spec.Field{
				{
					Key: "base_url", Label: "Base URL", InputKind: spec.InputURL,
					Required: true, Normalizer: spec.NormalizeBaseURL,
				},
				// Key names are part of the stored group params contract;
				// renaming them invalidates persisted rows.
				{
					Key: "reasoning_content_alias", Label: "Response Reasoning Alias",
					InputKind: spec.InputSelect, Options: spec.ReasoningAliasResponseOptions,
					Normalizer: spec.NormalizeResponseReasoningAlias,
				},
				{
					Key: "request_reasoning_alias", Label: "Request Reasoning Alias",
					InputKind: spec.InputSelect, Options: spec.ReasoningAliasOptions,
					Normalizer: spec.NormalizeReasoningAlias,
				},
			},
			Credentials: []spec.Field{{
				Key: "api_key", Label: "API Key", InputKind: spec.InputSecret,
				Required: true, Sensitive: true, Normalizer: spec.NormalizeNonEmpty,
			}},
			Provider: spec.ProviderBinding{
				ProviderKind:   spec.ProviderOpenAICompatible,
				EndpointPolicy: spec.EndpointRequiredBaseURL,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Rerank, execution.OperationRerank, execution.RouteNative),
				spec.NewRoute(protocol.Rerank, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIEmbeddings, execution.OperationProbe, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteNative),
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
