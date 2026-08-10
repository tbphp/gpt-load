// Package channel defines the code-owned directory of supported upstream channels.
package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// ID is a stable product channel identifier.
type ID string

const (
	OpenAI           ID = "openai"
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

// InputKind describes how a field is collected without exposing its value.
type InputKind string

const (
	InputText   InputKind = "text"
	InputURL    InputKind = "url"
	InputSecret InputKind = "secret"
)

// FieldDescriptor is the public, value-free schema for one channel field.
type FieldDescriptor struct {
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	InputKind InputKind `json:"input_kind"`
	Required  bool      `json:"required"`
	Sensitive bool      `json:"sensitive"`
}

// Descriptor contains only information safe to expose to the management UI.
type Descriptor struct {
	ID               ID                  `json:"channel_id"`
	Name             string              `json:"name"`
	Mark             string              `json:"mark"`
	Description      string              `json:"description"`
	ParamFields      []FieldDescriptor   `json:"param_fields"`
	CredentialFields []FieldDescriptor   `json:"credential_fields"`
	ClientProtocols  []protocol.Protocol `json:"client_protocols"`
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
type ProviderKind string

const (
	ProviderOpenAI           ProviderKind = "openai"
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

// Valid reports whether the provider kind is recognized.
func (k ProviderKind) Valid() bool {
	switch k {
	case ProviderOpenAI,
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

// ResolvedTarget is the provider-neutral execution target derived from a channel preset.
type ResolvedTarget struct {
	ChannelID         ID              `json:"channel_id"`
	ProviderKind      ProviderKind    `json:"-"`
	TargetConfig      json.RawMessage `json:"-"`
	CatalogProviderID string          `json:"-"`

	capabilities map[protocol.Protocol]execution.CapabilitySet
	modes        map[protocol.Protocol]map[execution.Operation]RouteMode
}

// Supports reports whether one client protocol can execute the operation with
// every required feature through this target.
func (t ResolvedTarget) Supports(
	clientProtocol protocol.Protocol,
	operation execution.Operation,
	required execution.FeatureSet,
) bool {
	capabilities, ok := t.capabilities[clientProtocol]
	return ok && capabilities.Supports(operation, required)
}

// Capabilities returns an independent capability set for one client protocol.
func (t ResolvedTarget) Capabilities(clientProtocol protocol.Protocol) execution.CapabilitySet {
	return t.capabilities[clientProtocol].Clone()
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

// Registry is an immutable code-owned channel directory.
type Registry struct {
	byID  map[ID]definition
	order []ID
}

// NewRegistry constructs the built-in, read-only channel registry.
func NewRegistry() *Registry {
	registry, err := newRegistry(builtinDefinitions())
	if err != nil {
		panic(fmt.Sprintf("build channel registry: %v", err))
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

// ValidateParams validates and normalizes the channel parameter object.
func (r *Registry) ValidateParams(id ID, raw json.RawMessage) (Params, error) {
	definition, ok := r.lookup(id)
	if !ok {
		return Params{}, &ValidationError{Field: "channel_id", Reason: "unknown channel"}
	}
	values, err := definition.params.validate("params", raw)
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
		capabilities:      cloneCapabilityMatrix(definition.capabilities),
		modes:             cloneRouteModes(definition.modes),
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
	descriptor FieldDescriptor
	normalize  normalizer
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
			if field.descriptor.Required {
				return nil, &ValidationError{Field: prefix + "." + field.descriptor.Key, Reason: "required"}
			}
			continue
		}
		var value string
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, &ValidationError{Field: prefix + "." + field.descriptor.Key, Reason: "must be a string"}
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
	searchTerms        []string
	params             objectSchema
	credentials        objectSchema
	validateCredential func(map[string]string) error
	catalogProviderID  string
	providerKind       ProviderKind
	compatible         bool
	fixedTargetConfig  json.RawMessage
	capabilities       map[protocol.Protocol]execution.CapabilitySet
	modes              map[protocol.Protocol]map[execution.Operation]RouteMode
}

func (d definition) matches(query string) bool {
	values := []string{string(d.descriptor.ID), d.descriptor.Name, d.descriptor.Description}
	values = append(values, d.searchTerms...)
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
		definition.searchTerms = append([]string(nil), definition.searchTerms...)
		definition.capabilities = cloneCapabilityMatrix(definition.capabilities)
		definition.modes = cloneRouteModes(definition.modes)
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
	if len(definition.capabilities) == 0 {
		return fmt.Errorf("channel %q has no client protocols", id)
	}
	for clientProtocol, capabilities := range definition.capabilities {
		if !clientProtocol.Valid() {
			return fmt.Errorf("channel %q has invalid client protocol %q", id, clientProtocol)
		}
		if err := capabilities.Validate(); err != nil {
			return fmt.Errorf("channel %q capabilities: %w", id, err)
		}
		modes := definition.modes[clientProtocol]
		for _, operation := range capabilities.Operations() {
			mode, exists := modes[operation]
			if !exists || !mode.Valid() {
				return fmt.Errorf("channel %q has no valid route mode for %q/%q", id, clientProtocol, operation)
			}
		}
		for operation := range modes {
			if !capabilities.Has(operation) {
				return fmt.Errorf("channel %q has route mode for unsupported %q/%q", id, clientProtocol, operation)
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
	source.ParamFields = append([]FieldDescriptor{}, source.ParamFields...)
	source.CredentialFields = append([]FieldDescriptor{}, source.CredentialFields...)
	source.ClientProtocols = append([]protocol.Protocol{}, source.ClientProtocols...)
	return source
}

func cloneCapabilityMatrix(source map[protocol.Protocol]execution.CapabilitySet) map[protocol.Protocol]execution.CapabilitySet {
	clone := make(map[protocol.Protocol]execution.CapabilitySet, len(source))
	for clientProtocol, capabilities := range source {
		clone[clientProtocol] = capabilities.Clone()
	}
	return clone
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
