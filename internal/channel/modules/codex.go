package modules

import (
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	CodexSubscriptionDriver SubscriptionDriverID = "codex"
	CodexModelDiscovery     UtilityID            = "codex_models"
	CodexQuotaObservation   UtilityID            = "codex_quota"
	CodexResetCreditAction  ActionID             = "codex_reset_credit"
)

func codexModule() Module {
	return Module{Definition: Definition{
		ID:          Codex,
		Name:        "Codex",
		Mark:        "CX",
		Icon:        "openai",
		SearchTerms: []string{"subscription", "oauth", "chatgpt"},
		Description: "OpenAI Codex subscription",
		Connection:  subscriptionConnection("browser_oauth", "oauth_file"),
		Provider: ProviderBinding{
			ProviderKind:   ProviderCodex,
			EndpointPolicy: EndpointNone,
		},
		Routes: []Route{
			route(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
			route(protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.RouteNative),
			route(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			route(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
		},
		Capabilities: CapabilityBindings{
			SubscriptionDriver: CodexSubscriptionDriver,
			ModelDiscovery:     CodexModelDiscovery,
			QuotaObservation:   CodexQuotaObservation,
			Actions:            []ActionID{CodexResetCreditAction},
		},
		Scheduling: SchedulingPolicy{QuotaPriority: true},
	}}
}
