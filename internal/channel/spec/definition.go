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
	Anthropic        ID = "anthropic"
	Gemini           ID = "gemini"
	AzureOpenAI      ID = "azure_openai"
	AWSBedrock       ID = "aws_bedrock"
	GoogleVertex     ID = "google_vertex"
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
	ProviderOpenAI           ProviderKind = "openai"
	ProviderCodex            ProviderKind = "codex"
	ProviderAnthropic        ProviderKind = "anthropic"
	ProviderGemini           ProviderKind = "gemini"
	ProviderOpenAICompatible ProviderKind = "openai_compatible"
	ProviderAzureOpenAI      ProviderKind = "azure_openai"
	ProviderAWSBedrock       ProviderKind = "aws_bedrock"
	ProviderGoogleVertex     ProviderKind = "google_vertex"
	ProviderDeepSeek         ProviderKind = "deepseek"
	ProviderOpenRouter       ProviderKind = "openrouter"
	ProviderGroq             ProviderKind = "groq"
	ProviderXAI              ProviderKind = "xai"
)

// Valid reports whether the provider adapter key is recognized.
func (kind ProviderKind) Valid() bool {
	switch kind {
	case ProviderOpenAI,
		ProviderCodex,
		ProviderAnthropic,
		ProviderGemini,
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

// Connection is the single connection contract for a channel.
type Connection struct {
	Type                 ConnectionType
	CredentialInput      string
	AuthorizationMethods []string
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

// Route is one explicit channel execution capability.
type Route struct {
	ClientProtocol protocol.Protocol
	Operation      execution.Operation
	Mode           execution.RouteMode
	Resolver       RouteResolverID
	PossibleModes  []execution.RouteMode
}

// NewRoute constructs one explicit static route declaration.
func NewRoute(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	mode execution.RouteMode,
) Route {
	return Route{ClientProtocol: clientProtocol, Operation: operation, Mode: mode}
}

// CapabilityBindings names optional implementations without embedding runtime
// objects in public manifests.
type CapabilityBindings struct {
	SubscriptionDriver SubscriptionDriverID
	ModelDiscovery     UtilityID
	ModelProbe         UtilityID
	QuotaObservation   UtilityID
	Actions            []ActionID
}

// SchedulingPolicy is immutable channel scheduling metadata.
type SchedulingPolicy struct {
	QuotaPriority bool
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
	Connection          Connection
	Params              []Field
	ParamsNormalizer    ParamsNormalizerID
	Credentials         []Field
	CredentialValidator CredentialValidatorID
	Provider            ProviderBinding
	Routes              []Route
	Capabilities        CapabilityBindings
	Scheduling          SchedulingPolicy
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
