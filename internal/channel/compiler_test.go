package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCompilerRejectsExtensionImplementedByAnotherChannelModule(t *testing.T) {
	t.Parallel()

	all := builtInModules()
	vertex := findModule(t, all, GoogleVertex)
	openAI := findModule(t, all, OpenAI)
	openAI.Extensions = vertex.Extensions
	vertex.Extensions = spec.Extensions{}

	if _, err := compileBuiltInModules([]spec.Module{openAI, vertex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted a cross-channel extension")
	}
}

func TestCompilerRejectsUnreferencedChannelExtension(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Extensions.ParamsNormalizers = map[spec.ParamsNormalizerID]spec.ParamsNormalizer{
		"unused": func(json.RawMessage) (map[string]string, error) {
			return map[string]string{}, nil
		},
	}

	if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an unreferenced channel extension")
	}
}

func TestCompilerRejectsUnknownAuthorizationMethod(t *testing.T) {
	t.Parallel()

	codex := findModule(t, builtInModules(), Codex)
	codex.Definition.Connection.AuthorizationMethods = []spec.AuthorizationMethod{"unknown"}

	if _, err := compileBuiltInModules([]spec.Module{codex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an unknown authorization method")
	}
}

func TestCompilerRejectsQuotaPriorityWithoutObservation(t *testing.T) {
	t.Parallel()

	codex := findModule(t, builtInModules(), Codex)
	codex.Definition.Capabilities.QuotaObservation = ""

	if _, err := compileBuiltInModules([]spec.Module{codex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted quota priority without observation")
	}
}

func TestCompilerRejectsResetCreditWithoutObservation(t *testing.T) {
	t.Parallel()

	codex := findModule(t, builtInModules(), Codex)
	codex.Definition.Scheduling.QuotaPriority = false
	codex.Definition.Capabilities.QuotaObservation = ""

	if _, err := compileBuiltInModules([]spec.Module{codex}); err == nil {
		t.Fatal("compileBuiltInModules() accepted reset credit without observation")
	}
}

func TestCompilerRejectsSubscriptionCapabilityOnAPIKeyChannel(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Definition.Capabilities.ModelDiscovery = "subscription_models"

	if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted a subscription capability on an API key channel")
	}
}

func TestCompilerRejectsOpenAIResponsesModelListRoute(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Definition.Routes = []spec.Route{spec.NewRoute(
		protocol.OpenAIResponses,
		execution.OperationListModels,
		execution.RouteNative,
	)}

	if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an OpenAI Responses model-list route")
	}
}

func TestResolvedTargetRejectsRouteResolverModeOutsideDeclaredSet(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	const resolverID spec.RouteResolverID = "test_out_of_range"
	for index := range openAI.Definition.Routes {
		route := &openAI.Definition.Routes[index]
		if route.ClientProtocol != protocol.OpenAICompletions ||
			route.Operation != execution.OperationChatCompletion {
			continue
		}
		route.Resolver = resolverID
		route.PossibleModes = []execution.RouteMode{execution.RouteNative}
	}
	openAI.Extensions.RouteResolvers = map[spec.RouteResolverID]spec.RouteResolver{
		resolverID: func(string, execution.RouteMode) execution.RouteMode {
			return execution.RouteConverted
		},
	}

	definitions, err := compileBuiltInModules([]spec.Module{openAI})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	target, err := registry.Resolve(OpenAI, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mode, ok := target.ModeForModel(
		protocol.OpenAICompletions,
		execution.OperationChatCompletion,
		"gpt-test",
	); ok {
		t.Fatalf("ModeForModel() = %q, true; want false for undeclared mode", mode)
	}
}

func TestResolvedTargetPreferredProtocolUsesDeclaredRoutes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		modes map[protocol.Protocol]map[execution.Operation]RouteMode
		want  protocol.Protocol
	}{
		{
			name: "only declared route",
			modes: map[protocol.Protocol]map[execution.Operation]RouteMode{
				protocol.OpenAICompletions: {
					execution.OperationListModels: RouteConverted,
				},
			},
			want: protocol.OpenAICompletions,
		},
		{
			name: "native before converted",
			modes: map[protocol.Protocol]map[execution.Operation]RouteMode{
				protocol.OpenAICompletions: {
					execution.OperationListModels: RouteConverted,
				},
				protocol.Anthropic: {
					execution.OperationListModels: RouteNative,
				},
			},
			want: protocol.Anthropic,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := ResolvedTarget{modes: test.modes}

			got, ok := target.PreferredProtocol(execution.OperationListModels, "")
			if !ok || got != test.want {
				t.Fatalf("PreferredProtocol() = %q, %t, want %q, true", got, ok, test.want)
			}
		})
	}
}

func findModule(t *testing.T, all []spec.Module, id spec.ID) spec.Module {
	t.Helper()
	for _, candidate := range all {
		if candidate.Definition.ID == id {
			return candidate
		}
	}
	t.Fatalf("channel module %q not found", id)
	return spec.Module{}
}
