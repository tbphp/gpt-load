package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	ClaudeSubscriptionDriver spec.SubscriptionDriverID = "claude"
	ClaudeModelDiscovery     spec.UtilityID            = "claude_models"
	ClaudeQuotaObservation   spec.UtilityID            = "claude_quota"
)

// Claude declares the subscription-backed Anthropic OAuth channel.
func Claude() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Claude,
			Name:        "Claude",
			Mark:        "CL",
			Icon:        "claude",
			SearchTerms: []string{"subscription", "oauth", "claude", "anthropic"},
			Description: "Anthropic Claude subscription",
			Connection: spec.Connection{
				Type:            spec.ConnectionSubscription,
				CredentialInput: "authorization",
				AuthorizationMethods: []spec.AuthorizationMethod{
					spec.AuthorizationBrowserOAuth,
					spec.AuthorizationOAuthFile,
				},
			},
			Params:      []spec.Field{},
			Credentials: []spec.Field{},
			Provider: spec.ProviderBinding{
				ProviderKind:   spec.ProviderClaude,
				EndpointPolicy: spec.EndpointNone,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: ClaudeSubscriptionDriver,
				ModelDiscovery:     ClaudeModelDiscovery,
				QuotaObservation:   ClaudeQuotaObservation,
			},
		},
	}
}
