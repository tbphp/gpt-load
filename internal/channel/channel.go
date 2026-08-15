// Package channel defines the code-owned directory of supported upstream channels.
package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// ID is a stable product channel identifier.
type ID = modules.ID

const (
	OpenAI           = modules.OpenAI
	Codex            = modules.Codex
	Anthropic        = modules.Anthropic
	Gemini           = modules.Gemini
	AzureOpenAI      = modules.AzureOpenAI
	AWSBedrock       = modules.AWSBedrock
	GoogleVertex     = modules.GoogleVertex
	OpenAICompatible = modules.OpenAICompatible
	DeepSeek         = modules.DeepSeek
	MoonshotAI       = modules.MoonshotAI
	SiliconFlow      = modules.SiliconFlow
	ZhipuAI          = modules.ZhipuAI
	Alibaba          = modules.Alibaba
	Volcengine       = modules.Volcengine
	OpenRouter       = modules.OpenRouter
	Groq             = modules.Groq
	XAI              = modules.XAI
)

// InputKind describes how a field is collected without exposing its value.
type InputKind = modules.InputKind

const (
	InputText   = modules.InputText
	InputURL    = modules.InputURL
	InputSecret = modules.InputSecret
)

// FieldDescriptor is the public, value-free schema for one channel field.
type FieldDescriptor struct {
	Key          string    `json:"key"`
	Label        string    `json:"label"`
	InputKind    InputKind `json:"input_kind"`
	Required     bool      `json:"required"`
	Sensitive    bool      `json:"sensitive"`
	DefaultValue *string   `json:"default_value"`
}

// ConnectionDescriptor describes the single code-owned credential flow used
// by a channel. Connection type never selects an execution adapter.
type ConnectionDescriptor struct {
	Type                 string   `json:"type"`
	CredentialInput      string   `json:"credential_input"`
	AuthorizationMethods []string `json:"authorization_methods,omitempty"`
}

// RouteDescriptor is the safe, compiled route projection exposed to the UI.
type RouteDescriptor struct {
	ClientProtocol protocol.Protocol   `json:"client_protocol"`
	Operation      execution.Operation `json:"operation"`
	RouteMode      RouteMode           `json:"route_mode"`
	ModelDependent bool                `json:"model_dependent"`
	PossibleModes  []RouteMode         `json:"possible_modes,omitempty"`
}

// Descriptor contains only information safe to expose to the management UI.
type Descriptor struct {
	ID   ID     `json:"channel_id"`
	Name string `json:"name"`
	Mark string `json:"mark"`
	// Icon names a frontend-owned icon resource. The frontend falls back to
	// Mark when Icon is empty or the resource is not bundled.
	Icon             string               `json:"icon"`
	SearchTerms      []string             `json:"search_terms"`
	Description      string               `json:"description"`
	ParamFields      []FieldDescriptor    `json:"param_fields"`
	CredentialFields []FieldDescriptor    `json:"credential_fields"`
	Connection       ConnectionDescriptor `json:"connection"`
	Routes           []RouteDescriptor    `json:"routes"`
	ClientProtocols  []protocol.Protocol  `json:"client_protocols"`
}

// Params is one validated, canonical channel parameter object.
type Params struct {
	values map[string]string
}

// Value returns one normalized parameter without exposing the backing map.
func (p Params) Value(key string) (string, bool) {
	value, ok := p.values[key]
	return value, ok
}

// CanonicalJSON returns an independent canonical JSON object.
func (p Params) CanonicalJSON() json.RawMessage { return canonicalJSON(p.values) }

// Credential is one validated credential object. Its fields are never marshaled implicitly.
type Credential struct {
	values map[string]string
}

// Value returns one normalized credential field without exposing the backing map.
func (c Credential) Value(key string) (string, bool) {
	value, ok := c.values[key]
	return value, ok
}

// CanonicalJSON explicitly returns the credential object for encryption or execution.
func (c Credential) CanonicalJSON() json.RawMessage { return canonicalJSON(c.values) }

// MarshalJSON prevents accidental serialization of credential names or values.
func (c Credential) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

// RouteMode is the neutral execution route decision exposed by a channel.
type RouteMode = execution.RouteMode

const (
	RouteNative    = execution.RouteNative
	RouteConverted = execution.RouteConverted
)

// ProviderKind identifies the upstream API family selected by a code-owned
// channel preset. It is runtime metadata, not a persisted SDK identifier.
type ProviderKind = modules.ProviderKind

const (
	ProviderOpenAI           = modules.ProviderOpenAI
	ProviderCodex            = modules.ProviderCodex
	ProviderAnthropic        = modules.ProviderAnthropic
	ProviderGemini           = modules.ProviderGemini
	ProviderOpenAICompatible = modules.ProviderOpenAICompatible
	ProviderAzureOpenAI      = modules.ProviderAzureOpenAI
	ProviderAWSBedrock       = modules.ProviderAWSBedrock
	ProviderGoogleVertex     = modules.ProviderGoogleVertex
	ProviderDeepSeek         = modules.ProviderDeepSeek
	ProviderOpenRouter       = modules.ProviderOpenRouter
	ProviderGroq             = modules.ProviderGroq
	ProviderXAI              = modules.ProviderXAI
)

// ResolvedTarget is the provider-neutral execution target derived from a channel preset.
type ResolvedTarget struct {
	ChannelID         ID              `json:"channel_id"`
	ProviderKind      ProviderKind    `json:"-"`
	TargetConfig      json.RawMessage `json:"-"`
	CatalogProviderID string          `json:"-"`

	modes     map[protocol.Protocol]map[execution.Operation]RouteMode
	resolvers map[routeKey]modules.RouteResolver
}

type routeKey struct {
	clientProtocol protocol.Protocol
	operation      execution.Operation
}

// Mode returns how an operation reaches this channel.
func (t ResolvedTarget) Mode(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
) (RouteMode, bool) {
	byOperation, ok := t.modes[clientProtocol]
	if !ok {
		return "", false
	}
	mode, ok := byOperation[operation]
	return mode, ok
}

// ModeForModel returns the route mode after applying model-specific channel behavior.
func (t ResolvedTarget) ModeForModel(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	upstreamModel string,
) (RouteMode, bool) {
	mode, ok := t.Mode(clientProtocol, operation)
	if !ok {
		return "", false
	}
	if resolver := t.resolvers[routeKey{clientProtocol: clientProtocol, operation: operation}]; resolver != nil {
		resolved := resolver(upstreamModel, mode)
		if !resolved.Valid() {
			return "", false
		}
		return resolved, true
	}
	return mode, true
}

// NormalizeVertexGeminiModel returns the Vertex resource ID for a Gemini,
// Gemma, or numeric custom endpoint that can preserve the native Gemini wire format.
func NormalizeVertexGeminiModel(model string) (string, bool) {
	return modules.NormalizeVertexGeminiModel(model)
}

// Operations returns the stable operation set declared for a client protocol.
func (t ResolvedTarget) Operations(clientProtocol protocol.Protocol) []execution.Operation {
	byOperation := t.modes[clientProtocol]
	operations := make([]execution.Operation, 0, len(byOperation))
	for operation := range byOperation {
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i] < operations[j] })
	return operations
}

// SupportsResponsesLifecycle reports whether stored Responses resources can
// be retrieved and managed through this target.
func (t ResolvedTarget) SupportsResponsesLifecycle() bool {
	for _, operation := range []execution.Operation{
		execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
	} {
		mode, ok := t.Mode(protocol.OpenAIResponses, operation)
		if !ok || mode != RouteNative {
			return false
		}
	}
	return true
}

// Registry is an immutable code-owned channel directory.
type Registry struct {
	byID  map[ID]definition
	order []ID
}

// CompileRegistry validates every built-in module and constructs the immutable
// runtime registry. Production startup propagates any invalid module as an
// initialization error instead of publishing a partial registry.
func CompileRegistry() (*Registry, error) {
	definitions, err := compileBuiltInModules(modules.All())
	if err != nil {
		return nil, fmt.Errorf("compile channel modules: %w", err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		return nil, fmt.Errorf("build channel registry: %w", err)
	}
	return registry, nil
}

// NewRegistry constructs the built-in, read-only channel registry for compact
// tests and helpers whose signature cannot return an error.
func NewRegistry() *Registry {
	registry, err := CompileRegistry()
	if err != nil {
		panic(err)
	}
	return registry
}

// Get returns an independent public descriptor.
func (r *Registry) Get(id ID) (Descriptor, bool) {
	definition, ok := r.lookup(id)
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(definition.descriptor), true
}

// List returns every built-in preset in stable product order.
func (r *Registry) List() []Descriptor {
	if r == nil {
		return nil
	}
	result := make([]Descriptor, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, cloneDescriptor(r.byID[id].descriptor))
	}
	return result
}

// Search returns case-insensitive ID/name/keyword matches in stable product order.
func (r *Registry) Search(query string) []Descriptor {
	if r == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(query))
	result := make([]Descriptor, 0, len(r.order))
	for _, id := range r.order {
		definition := r.byID[id]
		if normalized != "" && !definition.matches(normalized) {
			continue
		}
		result = append(result, cloneDescriptor(definition.descriptor))
	}
	return result
}

// CatalogProviderID returns the exact Models.dev provider mapping for a known
// channel. A known compatible channel intentionally returns an empty mapping.
func (r *Registry) CatalogProviderID(id ID) (string, bool) {
	definition, ok := r.lookup(id)
	if !ok {
		return "", false
	}
	return definition.catalogProviderID, true
}

// ProviderKind returns the runtime provider family for a known channel.
// It is internal metadata and is not part of the serialized descriptor.
func (r *Registry) ProviderKind(id ID) (ProviderKind, bool) {
	definition, ok := r.lookup(id)
	if !ok {
		return "", false
	}
	return definition.providerKind, true
}

// SupportsConnectionType reports whether a channel accepts one connection type.
func (r *Registry) SupportsConnectionType(id ID, connectionType string) bool {
	descriptor, ok := r.Get(id)
	return ok && descriptor.Connection.Type == strings.TrimSpace(connectionType)
}

// ConnectionType returns the single code-owned connection type for a channel.
// Connection type remains runtime metadata; callers must not treat it as a
// user-selectable option.
func (r *Registry) ConnectionType(id ID) (string, bool) {
	descriptor, ok := r.Get(id)
	if !ok || descriptor.Connection.Type == "" {
		return "", false
	}
	return descriptor.Connection.Type, true
}

// CapabilityBindings returns an independent copy of the compiled optional
// channel capabilities. Runtime consumers resolve these bindings at startup.
func (r *Registry) CapabilityBindings(id ID) (modules.CapabilityBindings, bool) {
	definition, ok := r.lookup(id)
	if !ok {
		return modules.CapabilityBindings{}, false
	}
	result := definition.capabilities
	result.Actions = append([]modules.ActionID(nil), result.Actions...)
	return result, true
}

// SchedulingPolicy returns immutable scheduling metadata for one channel.
func (r *Registry) SchedulingPolicy(id ID) (modules.SchedulingPolicy, bool) {
	definition, ok := r.lookup(id)
	if !ok {
		return modules.SchedulingPolicy{}, false
	}
	return definition.scheduling, true
}

// FixedBaseURL returns the code-owned endpoint for a fixed preset.
func (r *Registry) FixedBaseURL(id ID) (string, bool) {
	definition, ok := r.lookup(id)
	if !ok || definition.endpointPolicy != modules.EndpointFixedWithOverride || definition.fixedBaseURL == "" {
		return "", false
	}
	return definition.fixedBaseURL, true
}

// ValidateParams validates and normalizes the channel parameter object.
func (r *Registry) ValidateParams(id ID, raw json.RawMessage) (Params, error) {
	definition, ok := r.lookup(id)
	if !ok {
		return Params{}, &ValidationError{Field: "channel_id", Reason: "unknown channel"}
	}
	var values map[string]string
	var err error
	if definition.validateParams != nil {
		values, err = definition.validateParams(raw)
	} else {
		values, err = definition.params.validate("params", raw)
	}
	if err != nil {
		return Params{}, err
	}
	return Params{values: values}, nil
}

// ValidateCredential validates and normalizes one credential object.
func (r *Registry) ValidateCredential(id ID, raw json.RawMessage) (Credential, error) {
	definition, ok := r.lookup(id)
	if !ok {
		return Credential{}, &ValidationError{Field: "channel_id", Reason: "unknown channel"}
	}
	values, err := definition.credentials.validate("credential", raw)
	if err != nil {
		return Credential{}, err
	}
	if definition.validateCredential != nil {
		if err := definition.validateCredential(values); err != nil {
			return Credential{}, err
		}
	}
	return Credential{values: values}, nil
}

// Resolve validates params and produces a provider-neutral execution target.
func (r *Registry) Resolve(id ID, raw json.RawMessage) (ResolvedTarget, error) {
	definition, ok := r.lookup(id)
	if !ok {
		return ResolvedTarget{}, &ValidationError{Field: "channel_id", Reason: "unknown channel"}
	}
	params, err := r.ValidateParams(id, raw)
	if err != nil {
		return ResolvedTarget{}, err
	}
	targetConfig := resolvedTargetConfig(definition, params)
	return ResolvedTarget{
		ChannelID:         id,
		ProviderKind:      definition.providerKind,
		TargetConfig:      append(json.RawMessage(nil), targetConfig...),
		CatalogProviderID: definition.catalogProviderID,
		modes:             cloneRouteModes(definition.modes),
		resolvers:         cloneRouteResolvers(definition.resolvers),
	}, nil
}

// ResolveExecutionTarget validates a frozen runtime target produced by Resolve.
// Fixed presets must match their code-owned target exactly; configurable
// channels re-validate the target through their parameter schema.
func (r *Registry) ResolveExecutionTarget(id ID, raw json.RawMessage) (ResolvedTarget, error) {
	definition, ok := r.lookup(id)
	if !ok {
		return ResolvedTarget{}, &ValidationError{Field: "channel_id", Reason: "unknown channel"}
	}
	if len(definition.fixedTargetConfig) > 0 && bytes.Equal(bytes.TrimSpace(raw), definition.fixedTargetConfig) {
		return r.Resolve(id, nil)
	}
	if len(definition.fixedTargetConfig) > 0 {
		params, err := r.ValidateParams(id, raw)
		if err != nil {
			return ResolvedTarget{}, err
		}
		if _, overridden := params.Value("base_url"); !overridden {
			return ResolvedTarget{}, &ValidationError{Field: "target_config", Reason: "does not match channel preset"}
		}
		return r.Resolve(id, params.CanonicalJSON())
	}
	return r.Resolve(id, raw)
}

func resolvedTargetConfig(definition definition, params Params) json.RawMessage {
	if len(definition.fixedTargetConfig) > 0 {
		if baseURL, overridden := params.Value("base_url"); overridden {
			return canonicalJSON(map[string]string{"base_url": baseURL})
		}
		return append(json.RawMessage(nil), definition.fixedTargetConfig...)
	}
	if len(definition.params) > 0 {
		return params.CanonicalJSON()
	}
	return json.RawMessage(`{}`)
}

// ValidationError describes one invalid field without embedding its value.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

type normalizer func(string) (string, error)

type fieldSpec struct {
	descriptor   FieldDescriptor
	defaultValue string
	normalize    normalizer
}

type objectSchema []fieldSpec

func (s objectSchema) descriptors() []FieldDescriptor {
	descriptors := make([]FieldDescriptor, len(s))
	for index, field := range s {
		descriptors[index] = field.descriptor
	}
	return descriptors
}

func (s objectSchema) validate(prefix string, raw json.RawMessage) (map[string]string, error) {
	object, err := decodeStrictObject(raw)
	if err != nil {
		return nil, &ValidationError{Field: prefix, Reason: err.Error()}
	}
	byKey := make(map[string]fieldSpec, len(s))
	for _, field := range s {
		byKey[field.descriptor.Key] = field
	}
	for key := range object {
		if _, ok := byKey[key]; !ok {
			return nil, &ValidationError{Field: prefix + "." + key, Reason: "unknown field"}
		}
	}
	values := make(map[string]string, len(object))
	for _, field := range s {
		rawValue, exists := object[field.descriptor.Key]
		if !exists {
			if field.defaultValue != "" {
				values[field.descriptor.Key] = field.defaultValue
				continue
			}
			if field.descriptor.Required {
				return nil, &ValidationError{Field: prefix + "." + field.descriptor.Key, Reason: "required"}
			}
			continue
		}
		var value string
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, &ValidationError{Field: prefix + "." + field.descriptor.Key, Reason: "must be a string"}
		}
		if strings.TrimSpace(value) == "" && field.defaultValue != "" {
			values[field.descriptor.Key] = field.defaultValue
			continue
		}
		normalized, err := field.normalize(value)
		if err != nil {
			return nil, &ValidationError{Field: prefix + "." + field.descriptor.Key, Reason: err.Error()}
		}
		values[field.descriptor.Key] = normalized
	}
	return values, nil
}

type definition struct {
	descriptor         Descriptor
	params             objectSchema
	validateParams     func(json.RawMessage) (map[string]string, error)
	credentials        objectSchema
	validateCredential func(map[string]string) error
	catalogProviderID  string
	providerKind       ProviderKind
	connection         modules.Connection
	capabilities       modules.CapabilityBindings
	scheduling         modules.SchedulingPolicy
	endpointPolicy     modules.EndpointPolicy
	fixedBaseURL       string
	fixedTargetConfig  json.RawMessage
	modes              map[protocol.Protocol]map[execution.Operation]RouteMode
	resolvers          map[routeKey]modules.RouteResolver
}

func (d definition) matches(query string) bool {
	values := []string{string(d.descriptor.ID), d.descriptor.Name, d.descriptor.Description}
	values = append(values, d.descriptor.SearchTerms...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func newRegistry(definitions []definition) (*Registry, error) {
	registry := &Registry{byID: make(map[ID]definition, len(definitions)), order: make([]ID, 0, len(definitions))}
	for _, definition := range definitions {
		if err := validateDefinition(definition); err != nil {
			return nil, err
		}
		id := definition.descriptor.ID
		if _, duplicate := registry.byID[id]; duplicate {
			return nil, fmt.Errorf("duplicate channel ID %q", id)
		}
		definition.descriptor = cloneDescriptor(definition.descriptor)
		definition.modes = cloneRouteModes(definition.modes)
		definition.resolvers = cloneRouteResolvers(definition.resolvers)
		definition.connection.AuthorizationMethods = append([]string(nil), definition.connection.AuthorizationMethods...)
		definition.capabilities.Actions = append([]modules.ActionID(nil), definition.capabilities.Actions...)
		definition.fixedTargetConfig = append(json.RawMessage(nil), definition.fixedTargetConfig...)
		registry.byID[id] = definition
		registry.order = append(registry.order, id)
	}
	return registry, nil
}

func validateDefinition(definition definition) error {
	id := string(definition.descriptor.ID)
	if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" || strings.HasPrefix(id, "_") || strings.HasSuffix(id, "_") {
		return fmt.Errorf("invalid channel ID %q", id)
	}
	if strings.TrimSpace(definition.descriptor.Name) == "" {
		return fmt.Errorf("channel %q has no name", id)
	}
	if !definition.providerKind.Valid() {
		return fmt.Errorf("channel %q has invalid provider kind", id)
	}
	if definition.connection.Type != modules.ConnectionAPIKey && definition.connection.Type != modules.ConnectionSubscription {
		return fmt.Errorf("channel %q has invalid connection type", id)
	}
	if definition.connection.CredentialInput == "" {
		return fmt.Errorf("channel %q has no credential input", id)
	}
	if len(definition.fixedTargetConfig) > 0 && !json.Valid(definition.fixedTargetConfig) {
		return fmt.Errorf("channel %q has invalid fixed target config", id)
	}
	for name, schema := range map[string]objectSchema{"params": definition.params, "credential": definition.credentials} {
		seen := make(map[string]struct{}, len(schema))
		for _, field := range schema {
			key := field.descriptor.Key
			if key == "" || field.normalize == nil || (field.descriptor.InputKind != InputText && field.descriptor.InputKind != InputURL && field.descriptor.InputKind != InputSecret) {
				return fmt.Errorf("channel %q has invalid %s field", id, name)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("channel %q has duplicate %s field %q", id, name, key)
			}
			seen[key] = struct{}{}
		}
	}
	if len(definition.modes) == 0 {
		return fmt.Errorf("channel %q has no client protocols", id)
	}
	for clientProtocol, modes := range definition.modes {
		if !clientProtocol.Valid() {
			return fmt.Errorf("channel %q has invalid client protocol %q", id, clientProtocol)
		}
		if len(modes) == 0 {
			return fmt.Errorf("channel %q has no operations for %q", id, clientProtocol)
		}
		for operation, mode := range modes {
			if !operation.Valid() || !mode.Valid() {
				return fmt.Errorf("channel %q has no valid route mode for %q/%q", id, clientProtocol, operation)
			}
		}
	}
	return nil
}

func (r *Registry) lookup(id ID) (definition, bool) {
	if r == nil {
		return definition{}, false
	}
	definition, ok := r.byID[id]
	return definition, ok
}

func cloneDescriptor(source Descriptor) Descriptor {
	source.SearchTerms = append([]string{}, source.SearchTerms...)
	source.ParamFields = append([]FieldDescriptor{}, source.ParamFields...)
	source.CredentialFields = append([]FieldDescriptor{}, source.CredentialFields...)
	for index := range source.ParamFields {
		source.ParamFields[index].DefaultValue = cloneStringPointer(source.ParamFields[index].DefaultValue)
	}
	for index := range source.CredentialFields {
		source.CredentialFields[index].DefaultValue = cloneStringPointer(source.CredentialFields[index].DefaultValue)
	}
	source.Connection.AuthorizationMethods = append([]string{}, source.Connection.AuthorizationMethods...)
	source.Routes = append([]RouteDescriptor{}, source.Routes...)
	for index := range source.Routes {
		source.Routes[index].PossibleModes = append([]RouteMode{}, source.Routes[index].PossibleModes...)
	}
	source.ClientProtocols = append([]protocol.Protocol{}, source.ClientProtocols...)
	return source
}

func cloneStringPointer(source *string) *string {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func cloneRouteModes(source map[protocol.Protocol]map[execution.Operation]RouteMode) map[protocol.Protocol]map[execution.Operation]RouteMode {
	clone := make(map[protocol.Protocol]map[execution.Operation]RouteMode, len(source))
	for clientProtocol, modes := range source {
		clonedModes := make(map[execution.Operation]RouteMode, len(modes))
		for operation, mode := range modes {
			clonedModes[operation] = mode
		}
		clone[clientProtocol] = clonedModes
	}
	return clone
}

func cloneRouteResolvers(source map[routeKey]modules.RouteResolver) map[routeKey]modules.RouteResolver {
	clone := make(map[routeKey]modules.RouteResolver, len(source))
	for key, resolver := range source {
		clone[key] = resolver
	}
	return clone
}

func canonicalJSON(values map[string]string) json.RawMessage {
	if len(values) == 0 {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		panic(fmt.Sprintf("encode validated channel values: %v", err))
	}
	return append(json.RawMessage(nil), encoded...)
}

func decodeStrictObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte(`{}`)
	}
	if trimmed[0] != '{' {
		return nil, errors.New("must be a JSON object")
	}
	if err := rejectDuplicateFields(trimmed); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	object := make(map[string]json.RawMessage)
	if err := decoder.Decode(&object); err != nil {
		return nil, errors.New("invalid JSON object")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("must contain one JSON object")
	}
	return object, nil
}

func rejectDuplicateFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("invalid JSON object")
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return errors.New("invalid JSON object")
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("invalid JSON object")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("invalid JSON object")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("must contain one JSON object")
	}
	return nil
}

func normalizeNonEmpty(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("must not be empty")
	}
	return value, nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" {
		return "", errors.New("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", errors.New("must not contain query parameters")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if (parsed.Scheme == "https" && parsed.Port() == "443") ||
		(parsed.Scheme == "http" && parsed.Port() == "80") {
		hostname := parsed.Hostname()
		if strings.Contains(hostname, ":") {
			hostname = "[" + hostname + "]"
		}
		parsed.Host = hostname
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.ForceQuery = false
	return parsed.String(), nil
}
