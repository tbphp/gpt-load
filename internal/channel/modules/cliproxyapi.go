package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func CLIProxyAPI() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.CLIProxyAPI,
			Name:        "CLIProxyAPI",
			Mark:        "CPA",
			Icon:        "cpa",
			SearchTerms: []string{"cpa", "cli proxy api"},
			Description: "CLIProxyAPI multi-protocol gateway",
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
				ProviderKind:   spec.ProviderMultiProtocolGateway,
				EndpointPolicy: spec.EndpointRequiredBaseURL,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
				spec.NewResponsesCreateRoute(execution.RouteNative, spec.ResponsesStoreHandlingUpstreamManaged),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCompact, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteNative),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesEdit, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteNative),
			},
		},
	}
}
