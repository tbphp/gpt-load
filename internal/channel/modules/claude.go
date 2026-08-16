package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const ClaudeSubscriptionDriver spec.SubscriptionDriverID = "claude"

// Claude declares the subscription-backed Anthropic OAuth channel.
func Claude() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Claude,
			Name:        "Claude",
			Mark:        "CL",
			Icon:        "anthropic",
			SearchTerms: []string{"subscription", "oauth", "claude", "anthropic"},
			Description: "Anthropic Claude subscription",
			Notices: []spec.Notice{{
				ID:   spec.NoticeClaudeOAuthRisk,
				Tone: spec.NoticeToneWarning,
			}},
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
				ProviderKind:      spec.ProviderClaude,
				CatalogProviderID: "anthropic",
				EndpointPolicy:    spec.EndpointNone,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: ClaudeSubscriptionDriver,
			},
			Scheduling: spec.SchedulingPolicy{QuotaPriority: false},
		},
	}
}
