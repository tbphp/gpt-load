package channel

import (
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type compiledExtensions struct {
	paramsNormalizers    map[spec.ParamsNormalizerID]spec.ParamsNormalizer
	credentialValidators map[spec.CredentialValidatorID]spec.CredentialValidator
	routeResolvers       map[spec.RouteResolverID]spec.RouteResolver
}

func compileBuiltInModules(channelModules []spec.Module) ([]definition, error) {
	definitions := make([]definition, 0, len(channelModules))
	for _, channelModule := range channelModules {
		extensions, err := compileModuleExtensions(channelModule)
		if err != nil {
			return nil, err
		}
		compiled, err := compileModule(channelModule.Definition, extensions)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, compiled)
	}
	return definitions, nil
}

func compileModuleExtensions(channelModule spec.Module) (compiledExtensions, error) {
	result := compiledExtensions{
		paramsNormalizers:    make(map[spec.ParamsNormalizerID]spec.ParamsNormalizer),
		credentialValidators: make(map[spec.CredentialValidatorID]spec.CredentialValidator),
		routeResolvers:       make(map[spec.RouteResolverID]spec.RouteResolver),
	}
	for id, extension := range channelModule.Extensions.ParamsNormalizers {
		if id == "" || extension == nil {
			return compiledExtensions{}, fmt.Errorf("channel %q registers an invalid params normalizer", channelModule.Definition.ID)
		}
		if channelModule.Definition.ParamsNormalizer != id {
			return compiledExtensions{}, fmt.Errorf("channel %q registers unreferenced params normalizer %q", channelModule.Definition.ID, id)
		}
		result.paramsNormalizers[id] = extension
	}
	for id, extension := range channelModule.Extensions.CredentialValidators {
		if id == "" || extension == nil {
			return compiledExtensions{}, fmt.Errorf("channel %q registers an invalid credential validator", channelModule.Definition.ID)
		}
		if channelModule.Definition.CredentialValidator != id {
			return compiledExtensions{}, fmt.Errorf("channel %q registers unreferenced credential validator %q", channelModule.Definition.ID, id)
		}
		result.credentialValidators[id] = extension
	}
	referencedResolvers := make(map[spec.RouteResolverID]struct{})
	for _, route := range channelModule.Definition.Routes {
		if route.Resolver != "" {
			referencedResolvers[route.Resolver] = struct{}{}
		}
	}
	for id, extension := range channelModule.Extensions.RouteResolvers {
		if id == "" || extension == nil {
			return compiledExtensions{}, fmt.Errorf("channel %q registers an invalid route resolver", channelModule.Definition.ID)
		}
		if _, referenced := referencedResolvers[id]; !referenced {
			return compiledExtensions{}, fmt.Errorf("channel %q registers unreferenced route resolver %q", channelModule.Definition.ID, id)
		}
		result.routeResolvers[id] = extension
	}
	return result, nil
}

func compileModule(source spec.Definition, extensions compiledExtensions) (definition, error) {
	id := string(source.ID)
	if id == "" || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" ||
		strings.HasPrefix(id, "_") || strings.HasSuffix(id, "_") {
		return definition{}, fmt.Errorf("invalid channel ID %q", id)
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Icon) == "" {
		return definition{}, fmt.Errorf("channel %q has incomplete metadata", id)
	}
	if !source.Provider.ProviderKind.Valid() {
		return definition{}, fmt.Errorf("channel %q has invalid provider kind %q", id, source.Provider.ProviderKind)
	}
	if err := validateConnection(source); err != nil {
		return definition{}, err
	}
	params, err := compileSchema(id, "params", source.Params)
	if err != nil {
		return definition{}, err
	}
	credentials, err := compileSchema(id, "credential", source.Credentials)
	if err != nil {
		return definition{}, err
	}
	validateParams, err := resolveParamsNormalizer(id, source.ParamsNormalizer, extensions)
	if err != nil {
		return definition{}, err
	}
	validateCredential, err := resolveCredentialValidator(id, source.CredentialValidator, extensions)
	if err != nil {
		return definition{}, err
	}
	modes, resolvers, publicRoutes, err := compileRoutes(id, source.Routes, extensions)
	if err != nil {
		return definition{}, err
	}
	if err := validateTargetPolicy(source, params); err != nil {
		return definition{}, err
	}

	fixedBaseURL := ""
	var fixedTargetConfig json.RawMessage
	if source.Provider.FixedBaseURL != "" {
		fixedBaseURL, err = normalizeBaseURL(source.Provider.FixedBaseURL)
		if err != nil {
			return definition{}, fmt.Errorf("channel %q has invalid fixed base URL: %w", id, err)
		}
		fixedTargetConfig = canonicalJSON(map[string]string{"base_url": fixedBaseURL})
	}

	return definition{
		descriptor: Descriptor{
			ID:               source.ID,
			Name:             source.Name,
			Mark:             source.Mark,
			Icon:             source.Icon,
			SearchTerms:      append([]string(nil), source.SearchTerms...),
			Description:      source.Description,
			ParamFields:      params.descriptors(),
			CredentialFields: credentials.descriptors(),
			Connection: ConnectionDescriptor{
				Type:                 string(source.Connection.Type),
				CredentialInput:      source.Connection.CredentialInput,
				AuthorizationMethods: append([]string(nil), source.Connection.AuthorizationMethods...),
			},
			Routes:          publicRoutes,
			ClientProtocols: orderedProtocols(modes),
		},
		params:             params,
		validateParams:     validateParams,
		credentials:        credentials,
		validateCredential: validateCredential,
		catalogProviderID:  source.Provider.CatalogProviderID,
		providerKind:       source.Provider.ProviderKind,
		connection:         cloneConnection(source.Connection),
		capabilities:       cloneCapabilityBindings(source.Capabilities),
		scheduling:         source.Scheduling,
		endpointPolicy:     source.Provider.EndpointPolicy,
		fixedBaseURL:       fixedBaseURL,
		fixedTargetConfig:  fixedTargetConfig,
		modes:              modes,
		resolvers:          resolvers,
	}, nil
}

func validateConnection(source spec.Definition) error {
	id := source.ID
	connection := source.Connection
	switch connection.Type {
	case spec.ConnectionAPIKey:
		if connection.CredentialInput != "batch_text" || len(connection.AuthorizationMethods) != 0 {
			return fmt.Errorf("channel %q has invalid API key connection", id)
		}
		if source.Capabilities.SubscriptionDriver != "" {
			return fmt.Errorf("channel %q binds a subscription driver to an API key connection", id)
		}
	case spec.ConnectionSubscription:
		if connection.CredentialInput != "authorization" || len(connection.AuthorizationMethods) == 0 {
			return fmt.Errorf("channel %q has invalid subscription connection", id)
		}
		if source.Capabilities.SubscriptionDriver == "" {
			return fmt.Errorf("channel %q has no subscription driver", id)
		}
	default:
		return fmt.Errorf("channel %q has invalid connection type %q", id, connection.Type)
	}
	seen := make(map[string]struct{}, len(connection.AuthorizationMethods))
	for _, method := range connection.AuthorizationMethods {
		if strings.TrimSpace(method) == "" {
			return fmt.Errorf("channel %q has an empty authorization method", id)
		}
		if _, duplicate := seen[method]; duplicate {
			return fmt.Errorf("channel %q has duplicate authorization method %q", id, method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

func compileSchema(channelID string, name string, fields []spec.Field) (objectSchema, error) {
	result := make(objectSchema, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Key == "" || field.Normalizer == nil {
			return nil, fmt.Errorf("channel %q has invalid %s field", channelID, name)
		}
		if _, duplicate := seen[field.Key]; duplicate {
			return nil, fmt.Errorf("channel %q has duplicate %s field %q", channelID, name, field.Key)
		}
		seen[field.Key] = struct{}{}
		if field.InputKind != spec.InputText && field.InputKind != spec.InputURL && field.InputKind != spec.InputSecret {
			return nil, fmt.Errorf("channel %q has invalid %s field %q input kind", channelID, name, field.Key)
		}
		if field.Sensitive != (field.InputKind == spec.InputSecret) {
			return nil, fmt.Errorf("channel %q has inconsistent %s field %q sensitivity", channelID, name, field.Key)
		}
		if field.Sensitive && field.Default != "" {
			return nil, fmt.Errorf("channel %q has a secret default for %s field %q", channelID, name, field.Key)
		}
		defaultValue := ""
		var publicDefault *string
		if field.Default != "" {
			var err error
			defaultValue, err = field.Normalizer(field.Default)
			if err != nil {
				return nil, fmt.Errorf("channel %q has invalid default for %s field %q: %w", channelID, name, field.Key, err)
			}
			value := defaultValue
			publicDefault = &value
		}
		result = append(result, fieldSpec{
			descriptor: FieldDescriptor{
				Key: field.Key, Label: field.Label, InputKind: field.InputKind,
				Required: field.Required, Sensitive: field.Sensitive, DefaultValue: publicDefault,
			},
			defaultValue: defaultValue,
			normalize:    normalizer(field.Normalizer),
		})
	}
	return result, nil
}

func resolveParamsNormalizer(
	channelID string,
	id spec.ParamsNormalizerID,
	extensions compiledExtensions,
) (func(json.RawMessage) (map[string]string, error), error) {
	if id == "" {
		return nil, nil
	}
	normalizer, ok := extensions.paramsNormalizers[id]
	if !ok {
		return nil, fmt.Errorf("channel %q references unknown params normalizer %q", channelID, id)
	}
	return func(raw json.RawMessage) (map[string]string, error) {
		values, err := normalizer(raw)
		if err != nil {
			return nil, &ValidationError{Field: "params", Reason: err.Error()}
		}
		return values, nil
	}, nil
}

func resolveCredentialValidator(
	channelID string,
	id spec.CredentialValidatorID,
	extensions compiledExtensions,
) (func(map[string]string) error, error) {
	if id == "" {
		return nil, nil
	}
	validator, ok := extensions.credentialValidators[id]
	if !ok {
		return nil, fmt.Errorf("channel %q references unknown credential validator %q", channelID, id)
	}
	return func(values map[string]string) error {
		if err := validator(values); err != nil {
			return &ValidationError{Field: "credential", Reason: err.Error()}
		}
		return nil
	}, nil
}

func compileRoutes(
	channelID string,
	routes []spec.Route,
	extensions compiledExtensions,
) (
	map[protocol.Protocol]map[execution.Operation]RouteMode,
	map[routeKey]spec.RouteResolver,
	[]RouteDescriptor,
	error,
) {
	if len(routes) == 0 {
		return nil, nil, nil, fmt.Errorf("channel %q has no routes", channelID)
	}
	modes := make(map[protocol.Protocol]map[execution.Operation]RouteMode)
	resolvers := make(map[routeKey]spec.RouteResolver)
	public := make([]RouteDescriptor, 0, len(routes))
	seen := make(map[routeKey]struct{}, len(routes))
	for _, candidate := range routes {
		key := routeKey{clientProtocol: candidate.ClientProtocol, operation: candidate.Operation}
		if !candidate.ClientProtocol.Valid() || !candidate.Operation.Valid() || !candidate.Mode.Valid() {
			return nil, nil, nil, fmt.Errorf("channel %q has invalid route %q/%q", channelID, candidate.ClientProtocol, candidate.Operation)
		}
		if !validProtocolOperation(candidate.ClientProtocol, candidate.Operation) {
			return nil, nil, nil, fmt.Errorf("channel %q has incompatible route %q/%q", channelID, candidate.ClientProtocol, candidate.Operation)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, nil, fmt.Errorf("channel %q has duplicate route %q/%q", channelID, candidate.ClientProtocol, candidate.Operation)
		}
		seen[key] = struct{}{}
		if isResponsesLifecycle(candidate.Operation) && candidate.Mode != execution.RouteNative {
			return nil, nil, nil, fmt.Errorf("channel %q has converted Responses lifecycle route %q", channelID, candidate.Operation)
		}
		possibleModes := append([]RouteMode(nil), candidate.PossibleModes...)
		if candidate.Resolver != "" {
			resolver, ok := extensions.routeResolvers[candidate.Resolver]
			if !ok {
				return nil, nil, nil, fmt.Errorf("channel %q references unknown route resolver %q", channelID, candidate.Resolver)
			}
			if len(possibleModes) == 0 {
				return nil, nil, nil, fmt.Errorf("channel %q route resolver %q has no possible modes", channelID, candidate.Resolver)
			}
			seenModes := make(map[RouteMode]struct{}, len(possibleModes))
			for _, possible := range possibleModes {
				if !possible.Valid() {
					return nil, nil, nil, fmt.Errorf("channel %q route resolver %q has invalid possible mode", channelID, candidate.Resolver)
				}
				seenModes[possible] = struct{}{}
			}
			if _, includesDefault := seenModes[candidate.Mode]; !includesDefault {
				return nil, nil, nil, fmt.Errorf("channel %q route resolver %q omits its default mode", channelID, candidate.Resolver)
			}
			resolvers[key] = resolver
		} else if len(possibleModes) != 0 {
			return nil, nil, nil, fmt.Errorf("channel %q static route declares possible modes", channelID)
		}
		if modes[candidate.ClientProtocol] == nil {
			modes[candidate.ClientProtocol] = make(map[execution.Operation]RouteMode)
		}
		modes[candidate.ClientProtocol][candidate.Operation] = candidate.Mode
		public = append(public, RouteDescriptor{
			ClientProtocol: candidate.ClientProtocol,
			Operation:      candidate.Operation,
			RouteMode:      candidate.Mode,
			ModelDependent: candidate.Resolver != "",
			PossibleModes:  possibleModes,
		})
	}
	return modes, resolvers, public, nil
}

func validProtocolOperation(clientProtocol protocol.Protocol, operation execution.Operation) bool {
	switch operation {
	case execution.OperationChatCompletion:
		return clientProtocol != protocol.OpenAIResponses
	case execution.OperationResponsesCreate,
		execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough:
		return clientProtocol == protocol.OpenAIResponses
	case execution.OperationListModels, execution.OperationProbe:
		return true
	default:
		return false
	}
}

func isResponsesLifecycle(operation execution.Operation) bool {
	switch operation {
	case execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationResponsesPassthrough:
		return true
	default:
		return false
	}
}

func validateTargetPolicy(source spec.Definition, params objectSchema) error {
	id := source.ID
	switch source.Provider.EndpointPolicy {
	case spec.EndpointSDKDefault:
		if source.Provider.FixedBaseURL != "" {
			return fmt.Errorf("channel %q combines SDK and fixed endpoints", id)
		}
	case spec.EndpointFixedWithOverride:
		if source.Provider.FixedBaseURL == "" || !schemaHasField(params, "base_url", false) {
			return fmt.Errorf("channel %q has an invalid fixed endpoint policy", id)
		}
	case spec.EndpointRequiredBaseURL:
		if source.Provider.FixedBaseURL != "" || !schemaHasField(params, "base_url", true) {
			return fmt.Errorf("channel %q has an invalid required endpoint policy", id)
		}
	case spec.EndpointCloudParams:
		if source.Provider.FixedBaseURL != "" || len(params) == 0 {
			return fmt.Errorf("channel %q has an invalid cloud endpoint policy", id)
		}
	case spec.EndpointNone:
		if source.Provider.FixedBaseURL != "" || len(params) != 0 {
			return fmt.Errorf("channel %q has target params for an endpoint-less provider", id)
		}
	default:
		return fmt.Errorf("channel %q has invalid endpoint policy %q", id, source.Provider.EndpointPolicy)
	}
	return nil
}

func schemaHasField(schema objectSchema, key string, required bool) bool {
	for _, field := range schema {
		if field.descriptor.Key == key && field.descriptor.Required == required {
			return true
		}
	}
	return false
}

func cloneConnection(source spec.Connection) spec.Connection {
	source.AuthorizationMethods = append([]string(nil), source.AuthorizationMethods...)
	return source
}

func cloneCapabilityBindings(source spec.CapabilityBindings) spec.CapabilityBindings {
	source.Actions = append([]spec.ActionID(nil), source.Actions...)
	return source
}

func orderedProtocols(modes map[protocol.Protocol]map[execution.Operation]RouteMode) []protocol.Protocol {
	ordered := make([]protocol.Protocol, 0, len(modes))
	for _, clientProtocol := range protocol.DataPlaneProtocols() {
		if _, ok := modes[clientProtocol]; ok {
			ordered = append(ordered, clientProtocol)
		}
	}
	return ordered
}
