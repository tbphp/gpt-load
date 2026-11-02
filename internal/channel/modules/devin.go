package modules

import (
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	DevinSubscriptionDriver spec.SubscriptionDriverID = "devin"
	DevinModelDiscovery     spec.UtilityID            = "devin_models"
	DevinQuotaObservation   spec.UtilityID            = "devin_quota"
)

func Devin() spec.Module {
	return spec.Module{Definition: spec.Definition{
		ID: spec.Devin, Name: "Devin", Mark: "DV", Icon: "devin",
		SearchTerms: []string{"devin", "cognition", "subscription", "oauth"},
		Description: "Devin subscription",
		Connection:  spec.Connection{Type: spec.ConnectionSubscription, CredentialInput: "authorization", AuthorizationMethods: []spec.AuthorizationMethod{spec.AuthorizationBrowserOAuth, spec.AuthorizationOAuthFile}},
		Params:      []spec.Field{{Key: "base_url", Label: "API root URL", InputKind: spec.InputURL, Normalizer: spec.NormalizeOptionalHTTPSBaseURL}},
		Credentials: []spec.Field{},
		Provider:    spec.ProviderBinding{ProviderKind: spec.ProviderDevin, EndpointPolicy: spec.EndpointSDKDefault, DefaultBaseURLs: []string{"https://server.codeium.com"}},
		// 用户确认的例外：私有 Connect-RPC 只由 CPA 编解码，不公开私有协议入口。
		Routes: []spec.Route{
			spec.NewRoute(protocol.OpenAICompletions, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewResponsesCreateRoute(execution.RouteConverted, spec.ResponsesStoreHandlingStateless),
			spec.NewRoute(protocol.Anthropic, execution.OperationChatCompletion, execution.RouteConverted),
			spec.NewRoute(protocol.Gemini, execution.OperationChatCompletion, execution.RouteConverted),
		},
		Capabilities: spec.CapabilityBindings{SubscriptionDriver: DevinSubscriptionDriver, ModelDiscovery: DevinModelDiscovery, QuotaObservation: DevinQuotaObservation},
	}}
}
