package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	KiroSubscriptionDriver spec.SubscriptionDriverID = "kiro"
	KiroModelDiscovery     spec.UtilityID            = "kiro_models"
	KiroQuotaObservation   spec.UtilityID            = "kiro_quota"
)

// Kiro declares the subscription-backed Kiro coding channel. Kiro is a
// CodeWhisperer-streaming variant whose executor natively speaks the
// Anthropic (Claude) request/SSE contract, so Anthropic messaging and token
// counting are the native trajectories.
func Kiro() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID:          spec.Kiro,
		Name:        "Kiro",
		Mark:        "KI",
		Icon:        "kiro",
		SearchTerms: []string{"subscription", "oauth", "kiro", "coding", "amazonq"},
		Description: "Kiro AI coding subscription",
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
			ProviderKind:   spec.ProviderKiro,
			EndpointPolicy: spec.EndpointNone,
		},
		Routes: []spec.Route{
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteNative),
			spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
		},
		Capabilities: spec.CapabilityBindings{
			SubscriptionDriver: KiroSubscriptionDriver,
			ModelDiscovery:     KiroModelDiscovery,
			QuotaObservation:   KiroQuotaObservation,
		},
	}}
}
