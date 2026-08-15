package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	CodexSubscriptionDriver spec.SubscriptionDriverID = "codex"
	CodexModelDiscovery     spec.UtilityID            = "codex_models"
	CodexQuotaObservation   spec.UtilityID            = "codex_quota"
	CodexResetCreditAction  spec.ActionID             = "codex_reset_credit"
)

func Codex() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Codex,
			Name:        "Codex",
			Mark:        "CX",
			Icon:        "openai",
			SearchTerms: []string{"subscription", "oauth", "chatgpt"},
			Description: "OpenAI Codex subscription",
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
				ProviderKind:   spec.ProviderCodex,
				EndpointPolicy: spec.EndpointNone,
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: CodexSubscriptionDriver,
				ModelDiscovery:     CodexModelDiscovery,
				QuotaObservation:   CodexQuotaObservation,
				ResetCreditAction:  CodexResetCreditAction,
			},
			Scheduling: spec.SchedulingPolicy{QuotaPriority: true},
		},
	}
}
