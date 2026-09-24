package modules

import (
	"strings"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	MirasimSubscriptionDriver spec.SubscriptionDriverID = "mirasim"
	MirasimModelDiscovery     spec.UtilityID            = "mirasim_models"
	MirasimQuotaObservation   spec.UtilityID            = "mirasim_quota"
)

const (
	mirasimAnthropicRouteResolver spec.RouteResolverID = "mirasim_anthropic_wire"
	mirasimResponsesRouteResolver spec.RouteResolverID = "mirasim_responses_wire"
)

// Mirasim declares the subscription-backed Mirasim OAuth channel.
func Mirasim() spec.Module {
	return spec.Module{
		Definition: spec.Definition{
			ID:          spec.Mirasim,
			Name:        "Mirasim",
			Mark:        "MS",
			SearchTerms: []string{"subscription", "oauth", "mirasim"},
			Description: "Mirasim subscription",
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
			Provider: spec.ProviderBinding{
				ProviderKind:    spec.ProviderMirasim,
				EndpointPolicy:  spec.EndpointSDKDefault,
				DefaultBaseURLs: []string{"https://relay.mirasim.ai"},
			},
			Routes: []spec.Route{
				{
					ClientProtocol: protocol.Anthropic,
					Operation:      execution.OperationChatCompletion,
					Mode:           execution.RouteNative,
					Resolver:       mirasimAnthropicRouteResolver,
					PossibleModes:  []execution.RouteMode{execution.RouteNative, execution.RouteConverted},
				},
				spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteNative),
				{
					ClientProtocol:         protocol.OpenAIResponses,
					Operation:              execution.OperationResponsesCreate,
					Mode:                   execution.RouteNative,
					ResponsesStoreHandling: spec.ResponsesStoreHandlingStateless,
					Resolver:               mirasimResponsesRouteResolver,
					PossibleModes:          []execution.RouteMode{execution.RouteNative, execution.RouteConverted},
				},
				spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteConverted),
				spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
				spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteConverted),
			},
			Capabilities: spec.CapabilityBindings{
				SubscriptionDriver: MirasimSubscriptionDriver,
				ModelDiscovery:     MirasimModelDiscovery,
				QuotaObservation:   MirasimQuotaObservation,
			},
		},
		Extensions: spec.Extensions{RouteResolvers: map[spec.RouteResolverID]spec.RouteResolver{
			mirasimAnthropicRouteResolver: resolveMirasimAnthropicRoute,
			mirasimResponsesRouteResolver: resolveMirasimResponsesRoute,
		}},
	}
}

func resolveMirasimAnthropicRoute(upstreamModel string, defaultMode execution.RouteMode) execution.RouteMode {
	switch mirasimModelFamily(upstreamModel) {
	case "claude":
		return execution.RouteNative
	case "gpt":
		return execution.RouteConverted
	default:
		return defaultMode
	}
}

func resolveMirasimResponsesRoute(upstreamModel string, defaultMode execution.RouteMode) execution.RouteMode {
	switch mirasimModelFamily(upstreamModel) {
	case "gpt":
		return execution.RouteNative
	case "claude":
		return execution.RouteConverted
	default:
		return defaultMode
	}
}

func mirasimModelFamily(upstreamModel string) string {
	model := strings.ToLower(strings.TrimSpace(upstreamModel))
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "claude"
	case strings.HasPrefix(model, "gpt-"):
		return "gpt"
	default:
		return ""
	}
}
