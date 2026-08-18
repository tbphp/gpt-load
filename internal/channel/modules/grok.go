package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	GrokSubscriptionDriver spec.SubscriptionDriverID = "grok"
	GrokModelDiscovery     spec.UtilityID            = "grok_models"
	GrokQuotaObservation   spec.UtilityID            = "grok_quota"
)

// Grok declares the subscription-backed xAI OAuth channel.
func Grok() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID:          spec.Grok,
		Name:        "Grok",
		Mark:        "GR",
		Icon:        "xai",
		SearchTerms: []string{"subscription", "oauth", "xai", "grok"},
		Description: "xAI Grok subscription",
		Connection: spec.Connection{
			Type:            spec.ConnectionSubscription,
			CredentialInput: "authorization",
			AuthorizationMethods: []spec.AuthorizationMethod{
				spec.AuthorizationDeviceOAuth,
				spec.AuthorizationOAuthFile,
			},
		},
		Params:      []spec.Field{},
		Credentials: []spec.Field{},
		Provider: spec.ProviderBinding{
			ProviderKind:   spec.ProviderGrok,
			EndpointPolicy: spec.EndpointNone,
		},
		Routes: []spec.Route{
			spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteNative),
			spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteNative),
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteConverted),
		},
		Capabilities: spec.CapabilityBindings{
			SubscriptionDriver: GrokSubscriptionDriver,
			ModelDiscovery:     GrokModelDiscovery,
			QuotaObservation:   GrokQuotaObservation,
		},
		Scheduling: spec.SchedulingPolicy{QuotaPriority: true},
	}}
}
