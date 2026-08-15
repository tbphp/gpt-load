// Package provideradapter resolves code-owned ProviderKind bindings for
// execution and adapter-owned runtime lifecycle.
package provideradapter

import (
	"context"
	"fmt"
	"reflect"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

// Binding associates one ProviderKind with its execution adapter.
type Binding struct {
	ProviderKind channel.ProviderKind
	Adapter      execution.Executor
}

// RouteCapabilityValidator declares the execution shapes an adapter can
// actually implement. Channel definitions decide which subset is enabled;
// registry compilation rejects declarations outside this implementation bound.
type RouteCapabilityValidator interface {
	ValidateRouteCapability(channel.ProviderKind, channel.RouteDescriptor) error
}

type runtimeTargetReconciler interface {
	Reconcile([]channel.ResolvedTarget) error
}

// Registry is an immutable ProviderKind-to-adapter dispatcher.
type Registry struct {
	channels *channel.Registry
	bindings map[channel.ProviderKind]execution.Executor
	unique   []execution.Executor
}

// NewRegistry compiles provider adapter bindings.
func NewRegistry(channels *channel.Registry, bindings []Binding) (*Registry, error) {
	if channels == nil {
		return nil, fmt.Errorf("compile provider adapters: channel registry is required")
	}
	compiled := make(map[channel.ProviderKind]execution.Executor, len(bindings))
	unique := make([]execution.Executor, 0, len(bindings))
	for _, binding := range bindings {
		if !binding.ProviderKind.Valid() || isNilAdapter(binding.Adapter) {
			return nil, fmt.Errorf("compile provider adapters: invalid binding %q", binding.ProviderKind)
		}
		if _, duplicate := compiled[binding.ProviderKind]; duplicate {
			return nil, fmt.Errorf("compile provider adapters: duplicate binding %q", binding.ProviderKind)
		}
		if _, ok := binding.Adapter.(RouteCapabilityValidator); !ok {
			return nil, fmt.Errorf("compile provider adapters: adapter %q has no route capability contract", binding.ProviderKind)
		}
		compiled[binding.ProviderKind] = binding.Adapter
		if !containsAdapter(unique, binding.Adapter) {
			unique = append(unique, binding.Adapter)
		}
	}
	referenced := make(map[channel.ProviderKind]struct{})
	for _, descriptor := range channels.List() {
		providerKind, ok := channels.ProviderKind(descriptor.ID)
		if !ok {
			return nil, fmt.Errorf("compile provider adapters: channel %q has no provider binding", descriptor.ID)
		}
		if _, ok := compiled[providerKind]; !ok {
			return nil, fmt.Errorf("compile provider adapters: channel %q has no adapter for %q", descriptor.ID, providerKind)
		}
		validator := compiled[providerKind].(RouteCapabilityValidator)
		for _, route := range descriptor.Routes {
			for _, candidate := range expandRouteModes(route) {
				if err := validator.ValidateRouteCapability(providerKind, candidate); err != nil {
					return nil, fmt.Errorf(
						"compile provider adapters: channel %q route %q/%q/%q is unsupported by %q: %w",
						descriptor.ID,
						candidate.ClientProtocol,
						candidate.Operation,
						candidate.RouteMode,
						providerKind,
						err,
					)
				}
			}
		}
		referenced[providerKind] = struct{}{}
	}
	for providerKind := range compiled {
		if _, used := referenced[providerKind]; !used {
			return nil, fmt.Errorf("compile provider adapters: unreferenced adapter %q", providerKind)
		}
	}
	return &Registry{channels: channels, bindings: compiled, unique: unique}, nil
}

func expandRouteModes(route channel.RouteDescriptor) []channel.RouteDescriptor {
	modes := route.PossibleModes
	if !route.ModelDependent {
		modes = []channel.RouteMode{route.RouteMode}
	}
	result := make([]channel.RouteDescriptor, 0, len(modes))
	for _, mode := range modes {
		candidate := route
		candidate.RouteMode = mode
		candidate.ModelDependent = false
		candidate.PossibleModes = nil
		result = append(result, candidate)
	}
	return result
}

// Execute dispatches one non-streaming attempt.
func (registry *Registry) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	adapter, failure := registry.resolve(spec)
	if failure != nil {
		return unaryFailure(failure.Kind, failure.Summary)
	}
	return adapter.Execute(ctx, spec)
}

// ExecuteStream dispatches one streaming attempt.
func (registry *Registry) ExecuteStream(ctx context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
	adapter, failure := registry.resolve(spec)
	if failure != nil {
		return streamFailure(failure.Kind, failure.Summary)
	}
	return adapter.ExecuteStream(ctx, spec, sink)
}

func (registry *Registry) resolve(spec execution.AttemptSpec) (execution.Executor, *execution.ErrorEvidence) {
	if registry == nil || registry.channels == nil {
		return nil, &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: "provider adapter registry is unavailable"}
	}
	channelID := channel.ID(spec.ChannelID)
	target, err := registry.channels.ResolveExecutionTarget(channelID, spec.TargetConfig)
	if err != nil {
		return nil, &execution.ErrorEvidence{Kind: execution.ErrorKindInvalidRequest, Summary: "invalid channel target"}
	}
	mode, ok := target.ModeForModel(spec.ClientProtocol, spec.Operation, spec.UpstreamModel)
	if !ok || mode != spec.RouteMode || !spec.RouteRequirement.Allows(spec.RouteMode) {
		return nil, &execution.ErrorEvidence{Kind: execution.ErrorKindInvalidRequest, Summary: "attempt route is not declared by channel"}
	}
	adapter := registry.bindings[target.ProviderKind]
	if isNilAdapter(adapter) {
		return nil, &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: "provider adapter binding is unavailable"}
	}
	return adapter, nil
}

// DefaultBaseURL returns a safe UI hint from the channel definition or the
// adapter that owns an SDK-derived endpoint.
func (registry *Registry) DefaultBaseURL(channelID channel.ID) (string, bool, error) {
	if registry == nil || registry.channels == nil {
		return "", false, fmt.Errorf("provider adapter registry is unavailable")
	}
	if fixed, ok := registry.channels.FixedBaseURL(channelID); ok {
		return fixed, true, nil
	}
	providerKind, ok := registry.channels.ProviderKind(channelID)
	if !ok {
		return "", false, fmt.Errorf("unknown channel %q", channelID)
	}
	provider, ok := registry.bindings[providerKind].(interface {
		DefaultBaseURL(channel.ID) (string, bool, error)
	})
	if !ok {
		return "", false, nil
	}
	return provider.DefaultBaseURL(channelID)
}

// ReconcileTargets partitions snapshot targets by their bound adapter. Adapters
// without process-owned runtime state do not participate in reconciliation.
func (registry *Registry) ReconcileTargets(targets []channel.ResolvedTarget) error {
	if registry == nil {
		return fmt.Errorf("reconcile provider adapters: registry is unavailable")
	}
	for _, target := range targets {
		if isNilAdapter(registry.bindings[target.ProviderKind]) {
			return fmt.Errorf("reconcile provider adapters: provider %q is not bound", target.ProviderKind)
		}
	}
	for _, adapter := range registry.unique {
		reconciler, ok := adapter.(runtimeTargetReconciler)
		if !ok {
			continue
		}
		owned := make([]channel.ResolvedTarget, 0, len(targets))
		for _, target := range targets {
			if sameAdapter(adapter, registry.bindings[target.ProviderKind]) {
				owned = append(owned, target)
			}
		}
		if err := reconciler.Reconcile(owned); err != nil {
			return fmt.Errorf("reconcile provider adapter: %w", err)
		}
	}
	return nil
}

// RetireCredential releases adapter-owned state exactly once per adapter.
func (registry *Registry) RetireCredential(credentialID uint) {
	if registry == nil {
		return
	}
	for _, adapter := range registry.unique {
		if retire, ok := adapter.(interface{ RetireCredential(uint) }); ok {
			retire.RetireCredential(credentialID)
		}
	}
}

func isNilAdapter(adapter execution.Executor) bool {
	if adapter == nil {
		return true
	}
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func containsAdapter(adapters []execution.Executor, candidate execution.Executor) bool {
	for _, existing := range adapters {
		if sameAdapter(existing, candidate) {
			return true
		}
	}
	return false
}

func sameAdapter(left execution.Executor, right execution.Executor) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	return leftValue.Kind() == reflect.Pointer && leftValue.Pointer() == rightValue.Pointer()
}

func unaryFailure(kind execution.ErrorKind, summary string) execution.AttemptResult {
	return execution.AttemptResult{
		DispatchState: execution.DispatchNotSent,
		Error:         &execution.ErrorEvidence{Kind: kind, Summary: summary},
	}
}

func streamFailure(kind execution.ErrorKind, summary string) execution.StreamResult {
	return execution.StreamResult{
		DispatchState: execution.DispatchNotSent,
		Error:         &execution.ErrorEvidence{Kind: kind, Summary: summary},
	}
}

var _ execution.Executor = (*Registry)(nil)
