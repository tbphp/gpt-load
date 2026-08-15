package modules

import (
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func deepSeekModule() Module {
	return Module{Definition: Definition{
		ID:          DeepSeek,
		Name:        "DeepSeek",
		Mark:        "DS",
		Icon:        "deepseek",
		SearchTerms: []string{"deep seek"},
		Description: "Managed API preset",
		Connection:  apiKeyConnection(),
		Params:      optionalBaseURLFields(),
		Credentials: apiKeyFields(),
		Provider: ProviderBinding{
			ProviderKind:      ProviderDeepSeek,
			CatalogProviderID: "deepseek",
			EndpointPolicy:    EndpointSDKDefault,
		},
		Routes: []Route{
			route(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteNative),
			route(protocol.OpenAICompletions, execution.OperationListModels, execution.RouteNative),
			route(protocol.OpenAICompletions, execution.OperationProbe, execution.RouteNative),
			route(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteNative),
			route(protocol.OpenAIResponses, execution.OperationListModels, execution.RouteNative),
			route(protocol.OpenAIResponses, execution.OperationProbe, execution.RouteNative),
			route(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
			route(protocol.Anthropic, execution.OperationListModels, execution.RouteConverted),
			route(protocol.Anthropic, execution.OperationProbe, execution.RouteNative),
			route(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			route(protocol.Gemini, execution.OperationListModels, execution.RouteConverted),
			route(protocol.Gemini, execution.OperationProbe, execution.RouteConverted),
		},
	}}
}
