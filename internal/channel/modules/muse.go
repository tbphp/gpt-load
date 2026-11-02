package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	MuseSubscriptionDriver spec.SubscriptionDriverID = "muse"
	MuseModelDiscovery     spec.UtilityID            = "muse_models"
)

func Muse() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID: spec.Muse, Name: "Muse Code", Mark: "MC", Icon: "meta",
		SearchTerms: []string{"meta", "muse", "subscription", "oauth"},
		Description: "Meta Muse Code subscription",
		Connection: spec.Connection{Type: spec.ConnectionSubscription, CredentialInput: "authorization",
			AuthorizationMethods: []spec.AuthorizationMethod{spec.AuthorizationDeviceOAuth, spec.AuthorizationOAuthFile}},
		Params:      []spec.Field{{Key: "base_url", Label: "API root URL", InputKind: spec.InputURL, Normalizer: spec.NormalizeOptionalHTTPSBaseURL}},
		Credentials: []spec.Field{},
		Provider:    spec.ProviderBinding{ProviderKind: spec.ProviderMuse, EndpointPolicy: spec.EndpointSDKDefault, DefaultBaseURLs: []string{"https://api.meta.ai/v1"}},
		Routes: []spec.Route{
			spec.NewResponsesCreateRoute(execution.RouteNative, spec.ResponsesStoreHandlingStateless),
			spec.NewRoute(protocol.OpenAIResponses, execution.OperationResponsesInputTokens, execution.RouteNative),
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Anthropic, execution.OperationCountTokens, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationCountTokens, execution.RouteConverted),
		},
		Capabilities: spec.CapabilityBindings{SubscriptionDriver: MuseSubscriptionDriver, ModelDiscovery: MuseModelDiscovery},
	}}
}
