package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	ChatGPTSubscriptionDriver spec.SubscriptionDriverID = "chatgpt"
	ChatGPTDefaultBaseURL                               = "https://bps.openai.com/basispoints/api"
)

// ChatGPT declares the ChatGPT-subscription Responses API (basispoints).
// Credentials are the same Codex OAuth files; browser login stays on Codex
// because the two channels cannot share localhost:1455.
func ChatGPT() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID:          spec.ChatGPT,
		Name:        "ChatGPT",
		Mark:        "CG",
		Icon:        "openai",
		SearchTerms: []string{"subscription", "oauth", "chatgpt", "basispoints", "bps"},
		Description: "ChatGPT subscription Responses API (basispoints)",
		Connection: spec.Connection{
			Type:            spec.ConnectionSubscription,
			CredentialInput: "authorization",
			AuthorizationMethods: []spec.AuthorizationMethod{
				spec.AuthorizationOAuthFile,
			},
		},
		Params: []spec.Field{{
			Key: "base_url", Label: "API root URL", InputKind: spec.InputURL,
			Normalizer: spec.NormalizeOptionalHTTPSBaseURL,
		}},
		Credentials: []spec.Field{},
		Provider: spec.ProviderBinding{
			ProviderKind:    spec.ProviderChatGPT,
			EndpointPolicy:  spec.EndpointSDKDefault,
			DefaultBaseURLs: []string{ChatGPTDefaultBaseURL},
		},
		Routes: []spec.Route{
			spec.NewResponsesCreateRoute(execution.RouteNative, spec.ResponsesStoreHandlingStateless),
			spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteNative),
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteConverted),
		},
		Capabilities: spec.CapabilityBindings{
			SubscriptionDriver: ChatGPTSubscriptionDriver,
		},
	}}
}
