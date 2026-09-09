package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	AntigravitySubscriptionDriver spec.SubscriptionDriverID = "antigravity"
	AntigravityModelDiscovery     spec.UtilityID            = "antigravity_models"
	AntigravityQuotaObservation   spec.UtilityID            = "antigravity_quota"
)

// Antigravity declares the Google Antigravity subscription channel.
func Antigravity() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Antigravity,
			Name:        "Antigravity",
			Mark:        "AG",
			Icon:        "antigravity",
			SearchTerms: []string{"subscription", "oauth", "google", "antigravity"},
			Description: "Google Antigravity subscription",
			Connection: spec.Connection{
				Type:            spec.ConnectionSubscription,
				CredentialInput: "authorization",
				AuthorizationMethods: []spec.AuthorizationMethod{
					spec.AuthorizationBrowserOAuth,
					spec.AuthorizationOAuthFile,
				},
			},
			Params: []spec.Field{{
				Key: "base_url", Label: "API root URL", InputKind: spec.InputURL,
				Normalizer: spec.NormalizeOptionalHTTPSBaseURL,
			}},
			Credentials: []spec.Field{},
			Provider: spec.ProviderBinding{
				ProviderKind:    spec.ProviderAntigravity,
				EndpointPolicy:  spec.EndpointSDKDefault,
				DefaultBaseURLs: []string{"https://daily-cloudcode-pa.googleapis.com", "https://cloudcode-pa.googleapis.com"},
			},
			Routes: []spec.Route{
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteNative),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteNative),
				spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAIImages, execution.OperationImagesGenerate, execution.RouteConverted),
				spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: AntigravitySubscriptionDriver,
				ModelDiscovery:     AntigravityModelDiscovery,
				QuotaObservation:   AntigravityQuotaObservation,
			},
		},
	}
}
