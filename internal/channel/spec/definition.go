// Package spec defines the code-owned contract shared by channel modules and
// the channel compiler.
package spec

import (
	"encoding/json"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// ID is a stable product channel identifier.
type ID string

const (
	OpenAI           ID = "openai"
	Codex            ID = "codex"
	Claude           ID = "claude"
	Antigravity      ID = "antigravity"
	Grok             ID = "grok"
	Anthropic        ID = "anthropic"
	Gemini           ID = "gemini"
	AzureOpenAI      ID = "azure_openai"
	AWSBedrock       ID = "aws_bedrock"
	GoogleVertex     ID = "google_vertex"
	GPTLoad          ID = "gpt_load"
	NewAPI           ID = "newapi"
	CLIProxyAPI      ID = "cliproxyapi"
	OpenAICompatible ID = "openai_compatible"
	DeepSeek         ID = "deepseek"
	MoonshotAI       ID = "moonshotai"
	SiliconFlow      ID = "siliconflow"
	ZhipuAI          ID = "zhipuai"
	Alibaba          ID = "alibaba"
	Volcengine       ID = "volcengine"
	OpenRouter       ID = "openrouter"
	Groq             ID = "groq"
	XAI              ID = "xai"
)

// ProviderKind is the single dispatch key used to resolve a ProviderAdapter.
type ProviderKind string

const (
	ProviderOpenAI               ProviderKind = "openai"
	ProviderCodex                ProviderKind = "codex"
	ProviderClaude               ProviderKind = "claude"
	ProviderAntigravity          ProviderKind = "antigravity"
	ProviderGrok                 ProviderKind = "grok"
	ProviderAnthropic            ProviderKind = "anthropic"
	ProviderGemini               ProviderKind = "gemini"
	ProviderMultiProtocolGateway ProviderKind = "multi_protocol_gateway"
	ProviderOpenAICompatible     ProviderKind = "openai_compatible"
	ProviderAzureOpenAI          ProviderKind = "azure_openai"
	ProviderAWSBedrock           ProviderKind = "aws_bedrock"
	ProviderGoogleVertex         ProviderKind = "google_vertex"
	ProviderDeepSeek             ProviderKind = "deepseek"
	ProviderOpenRouter           ProviderKind = "openrouter"
	ProviderGroq                 ProviderKind = "groq"
	ProviderXAI                  ProviderKind = "xai"
)

// NoticeID identifies one code-owned, frontend-localized channel notice.
type NoticeID string

const (
	NoticeClaudeOAuthRisk      NoticeID = "claude_oauth_risk"
	NoticeAntigravityOAuthRisk NoticeID = "antigravity_oauth_risk"
)

// Valid reports whether the notice ID is part of the public channel contract.
func (id NoticeID) Valid() bool {
	return id == NoticeClaudeOAuthRisk || id == NoticeAntigravityOAuthRisk
}

// NoticeTone controls the bounded presentation style of one channel notice.
type NoticeTone string

const NoticeToneWarning NoticeTone = "warning"

// Valid reports whether the notice tone is safe for public projection.
func (tone NoticeTone) Valid() bool {
	return tone == NoticeToneWarning
}

// Notice declares safe, non-executable channel guidance.
type Notice struct {
	ID   NoticeID
	Tone NoticeTone
}

// Valid reports whether the provider adapter key is recognized.
func (kind ProviderKind) Valid() bool {
	switch kind {
	case ProviderOpenAI,
		ProviderCodex,
		ProviderClaude,
		ProviderAntigravity,
		ProviderGrok,
		ProviderAnthropic,
		ProviderGemini,
		ProviderMultiProtocolGateway,
		ProviderOpenAICompatible,
		ProviderAzureOpenAI,
		ProviderAWSBedrock,
		ProviderGoogleVertex,
		ProviderDeepSeek,
		ProviderOpenRouter,
		ProviderGroq,
		ProviderXAI:
		return true
	default:
		return false
	}
}

// SupportsOutboundProxy reports whether GPT-Load may inject its managed proxy
// configuration into this provider. Provider SDK-native environment handling
// remains outside this capability contract.
func (kind ProviderKind) SupportsOutboundProxy() bool {
	if !kind.Valid() {
		return false
	}
	switch kind {
	case ProviderAzureOpenAI, ProviderAWSBedrock, ProviderGoogleVertex:
		return false
	default:
		return true
	}
}

// InputKind describes how the management UI collects one field.
type InputKind string

const (
	InputText   InputKind = "text"
	InputURL    InputKind = "url"
	InputSecret InputKind = "secret"
)

// ValueNormalizer canonicalizes one field without retaining its input.
type ValueNormalizer func(string) (string, error)

// Field is one code-owned parameter or credential field.
type Field struct {
	Key        string
	Label      string
	InputKind  InputKind
	Required   bool
	Sensitive  bool
	Default    string
	Normalizer ValueNormalizer
}

// ConnectionType describes credential acquisition and lifecycle only. It is
// never an execution dispatch key.
type ConnectionType string

const (
	ConnectionAPIKey       ConnectionType = "api_key"
	ConnectionSubscription ConnectionType = "subscription"
)

// AuthorizationMethod is one supported subscription credential entry flow.
type AuthorizationMethod string

const (
	AuthorizationBrowserOAuth AuthorizationMethod = "browser_oauth"
	AuthorizationDeviceOAuth  AuthorizationMethod = "device_oauth"
	AuthorizationOAuthFile    AuthorizationMethod = "oauth_file"
)

// Valid reports whether the authorization method is part of the public channel contract.
func (method AuthorizationMethod) Valid() bool {
	switch method {
	case AuthorizationBrowserOAuth, AuthorizationDeviceOAuth, AuthorizationOAuthFile:
		return true
	default:
		return false
	}
}

// Connection is the single connection contract for a channel.
type Connection struct {
	Type                 ConnectionType
	CredentialInput      string
	AuthorizationMethods []AuthorizationMethod
}

// EndpointPolicy declares where non-secret target configuration comes from.
type EndpointPolicy string

const (
	EndpointSDKDefault        EndpointPolicy = "sdk_default"
	EndpointFixedWithOverride EndpointPolicy = "fixed_with_override"
	EndpointRequiredBaseURL   EndpointPolicy = "required_base_url"
	EndpointCloudParams       EndpointPolicy = "cloud_params"
	EndpointNone              EndpointPolicy = "none"
)

// ProviderBinding identifies the adapter and its non-secret target policy.
type ProviderBinding struct {
	ProviderKind      ProviderKind
	CatalogProviderID string
	EndpointPolicy    EndpointPolicy
	FixedBaseURL      string
}

// ExtensionID is a strongly typed code-owned extension binding.
type ExtensionID string

// RouteResolverID selects a model-aware route resolver.
type RouteResolverID ExtensionID

// ParamsNormalizerID selects an object-level parameter normalizer.
type ParamsNormalizerID ExtensionID

// CredentialValidatorID selects an object-level credential validator.
type CredentialValidatorID ExtensionID

// SubscriptionDriverID selects a subscription credential lifecycle driver.
type SubscriptionDriverID ExtensionID

// UtilityID selects a narrow control-plane capability implementation.
type UtilityID ExtensionID

// ActionID selects one explicitly supported credential/account action.
type ActionID ExtensionID

// ResponsesStoreHandling declares how one Responses Create route handles the
// client's store intent without implying complete Responses lifecycle support.
type ResponsesStoreHandling string

const (
	ResponsesStoreHandlingNone            ResponsesStoreHandling = ""
	ResponsesStoreHandlingUpstreamManaged ResponsesStoreHandling = "upstream_managed"
	ResponsesStoreHandlingStateless       ResponsesStoreHandling = "stateless"
)

// Valid reports whether the Responses store handling value is recognized.
func (handling ResponsesStoreHandling) Valid() bool {
	return handling == ResponsesStoreHandlingNone ||
		handling == ResponsesStoreHandlingUpstreamManaged ||
		handling == ResponsesStoreHandlingStateless
}

// Route is one explicit channel execution capability.
type Route struct {
	ClientProtocol         protocol.Protocol
	Operation              execution.Operation
	Mode                   execution.RouteMode
	ResponsesStoreHandling ResponsesStoreHandling
	Resolver               RouteResolverID
	PossibleModes          []execution.RouteMode
}

// NewRoute constructs one explicit static route declaration.
func NewRoute(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	mode execution.RouteMode,
) Route {
	return Route{ClientProtocol: clientProtocol, Operation: operation, Mode: mode}
}

// NewResponsesCreateRoute constructs one Responses Create route with an
// explicit store-handling contract.
func NewResponsesCreateRoute(
	mode execution.RouteMode,
	handling ResponsesStoreHandling,
) Route {
	return Route{
		ClientProtocol:         protocol.OpenAIResponses,
		Operation:              execution.OperationResponsesCreate,
		Mode:                   mode,
		ResponsesStoreHandling: handling,
	}
}

// CapabilityBindings names optional implementations without embedding runtime
// objects in public manifests.
type CapabilityBindings struct {
	SubscriptionDriver SubscriptionDriverID
	ModelDiscovery     UtilityID
	QuotaObservation   UtilityID
	ResetCreditAction  ActionID
}

// Definition is the complete, code-owned declaration for one channel. Every
// built-in module declares its schema, provider, routes, and bindings inline;
// shared helpers may normalize a field or construct one Route, but never hide
// a complete channel definition, schema, or route set.
type Definition struct {
	ID                  ID
	Name                string
	Mark                string
	Icon                string
	SearchTerms         []string
	Description         string
	Notices             []Notice
	Connection          Connection
	Params              []Field
	ParamsNormalizer    ParamsNormalizerID
	Credentials         []Field
	CredentialValidator CredentialValidatorID
	Provider            ProviderBinding
	Routes              []Route
	Capabilities        CapabilityBindings
}

// ParamsNormalizer applies object-level defaults or cross-field rules.
type ParamsNormalizer func(json.RawMessage) (map[string]string, error)

// CredentialValidator validates a canonical credential object.
type CredentialValidator func(map[string]string) error

// RouteResolver applies one declared model-dependent mode policy.
type RouteResolver func(upstreamModel string, defaultMode execution.RouteMode) execution.RouteMode

// Extensions are implementations owned by the same file as their channel.
type Extensions struct {
	ParamsNormalizers    map[ParamsNormalizerID]ParamsNormalizer
	CredentialValidators map[CredentialValidatorID]CredentialValidator
	RouteResolvers       map[RouteResolverID]RouteResolver
}

// Module keeps a channel declaration and its optional custom code together.
type Module struct {
	Definition Definition
	Extensions Extensions
}
