package channel

import (
	"encoding/json"
	"reflect"
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

func TestCompilerAcceptsDeviceOAuthAuthorizationMethod(t *testing.T) {
	t.Parallel()

	codex := findModule(t, builtInModules(), Codex)
	codex.Definition.Connection.AuthorizationMethods = []spec.AuthorizationMethod{
		"device_oauth",
		spec.AuthorizationOAuthFile,
	}

	if _, err := compileBuiltInModules([]spec.Module{codex}); err != nil {
		t.Fatalf("compileBuiltInModules() rejected device OAuth: %v", err)
	}
}

func TestCompilerRejectsResetCreditWithoutObservation(t *testing.T) {
	t.Parallel()

	codex := findModule(t, builtInModules(), Codex)
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

func TestCompilerValidatesExplicitResponsesStoreHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		clientProtocol protocol.Protocol
		operation      execution.Operation
		mode           execution.RouteMode
		possibleModes  []execution.RouteMode
		handling       spec.ResponsesStoreHandling
		wantError      bool
	}{
		{
			name:           "native Responses create upstream managed",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteNative,
			handling:       spec.ResponsesStoreHandlingUpstreamManaged,
		},
		{
			name:           "native Responses create stateless",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteNative,
			handling:       spec.ResponsesStoreHandlingStateless,
		},
		{
			name:           "converted Responses create stateless",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteConverted,
			handling:       spec.ResponsesStoreHandlingStateless,
		},
		{
			name:           "converted Responses create upstream managed",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteConverted,
			handling:       spec.ResponsesStoreHandlingUpstreamManaged,
			wantError:      true,
		},
		{
			name:           "model-dependent Responses create can convert upstream managed",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteNative,
			possibleModes:  []execution.RouteMode{execution.RouteNative, execution.RouteConverted},
			handling:       spec.ResponsesStoreHandlingUpstreamManaged,
			wantError:      true,
		},
		{
			name:           "Responses create without handling",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteNative,
			wantError:      true,
		},
		{
			name:           "non Responses operation",
			clientProtocol: protocol.OpenAICompletions,
			operation:      execution.OperationChatCompletion,
			mode:           execution.RouteNative,
			handling:       spec.ResponsesStoreHandlingStateless,
			wantError:      true,
		},
		{
			name:           "unknown compatibility",
			clientProtocol: protocol.OpenAIResponses,
			operation:      execution.OperationResponsesCreate,
			mode:           execution.RouteNative,
			handling:       spec.ResponsesStoreHandling("unknown"),
			wantError:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			module := findModule(t, builtInModules(), Codex)
			module.Definition.Routes = []spec.Route{{
				ClientProtocol:         test.clientProtocol,
				Operation:              test.operation,
				Mode:                   test.mode,
				PossibleModes:          test.possibleModes,
				ResponsesStoreHandling: test.handling,
			}}
			definitions, err := compileBuiltInModules([]spec.Module{module})
			if test.wantError {
				if err == nil {
					t.Fatal("compileBuiltInModules() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("compileBuiltInModules() error = %v", err)
			}
			key := routeKey{
				clientProtocol: protocol.OpenAIResponses,
				operation:      execution.OperationResponsesCreate,
			}
			if got := definitions[0].responsesStoreHandlings[key]; got != test.handling {
				t.Fatalf("compiled handling = %q, want %q", got, test.handling)
			}
		})
	}
}

func TestValidProtocolOperationOpenAIImagesMatrix(t *testing.T) {
	t.Parallel()

	for _, operation := range []execution.Operation{
		execution.OperationImagesGenerate,
		execution.OperationImagesEdit,
	} {
		if !validProtocolOperation(protocol.OpenAIImages, operation) {
			t.Fatalf("openai-images/%s must be valid", operation)
		}
		if validProtocolOperation(protocol.OpenAIResponses, operation) {
			t.Fatalf("openai-responses/%s must be invalid", operation)
		}
	}
	if validProtocolOperation(protocol.OpenAIImages, execution.OperationChatCompletion) ||
		validProtocolOperation(protocol.OpenAIImages, execution.OperationResponsesCreate) {
		t.Fatal("openai-images accepted a non-images operation")
	}
}

func TestCompilerDefaultsEmptyIconToChannelID(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Definition.Icon = ""

	definitions, err := compileBuiltInModules([]spec.Module{openAI})
	if err != nil {
		t.Fatal(err)
	}
	if got := definitions[0].descriptor.Icon; got != string(OpenAI) {
		t.Fatalf("compiled icon = %q, want %q", got, OpenAI)
	}
}

func TestCompilerRejectsEmptyChannelMark(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Definition.Mark = " "

	if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
		t.Fatal("compileBuiltInModules() accepted an empty channel mark")
	}
}

func TestCompilerProjectsTypedChannelNotice(t *testing.T) {
	t.Parallel()

	openAI := findModule(t, builtInModules(), OpenAI)
	openAI.Definition.Notices = []spec.Notice{{
		ID:   spec.NoticeClaudeOAuthRisk,
		Tone: spec.NoticeToneWarning,
	}}

	definitions, err := compileBuiltInModules([]spec.Module{openAI})
	if err != nil {
		t.Fatal(err)
	}
	want := []NoticeDescriptor{{
		ID:   NoticeClaudeOAuthRisk,
		Tone: NoticeToneWarning,
	}}
	if got := definitions[0].descriptor.Notices; !reflect.DeepEqual(got, want) {
		t.Fatalf("compiled notices = %#v, want %#v", got, want)
	}
}

func TestCompilerRejectsInvalidOrDuplicateChannelNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		notices []spec.Notice
	}{
		{
			name: "unknown ID",
			notices: []spec.Notice{{
				ID: "unknown_notice", Tone: spec.NoticeToneWarning,
			}},
		},
		{
			name: "unknown tone",
			notices: []spec.Notice{{
				ID: spec.NoticeClaudeOAuthRisk, Tone: "critical",
			}},
		},
		{
			name: "duplicate",
			notices: []spec.Notice{
				{ID: spec.NoticeClaudeOAuthRisk, Tone: spec.NoticeToneWarning},
				{ID: spec.NoticeClaudeOAuthRisk, Tone: spec.NoticeToneWarning},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			openAI := findModule(t, builtInModules(), OpenAI)
			openAI.Definition.Notices = test.notices
			if _, err := compileBuiltInModules([]spec.Module{openAI}); err == nil {
				t.Fatal("compileBuiltInModules() accepted invalid channel notice")
			}
		})
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

func TestResolvedTargetPreferredRouteModeIsResolved(t *testing.T) {
	t.Parallel()

	key := routeKey{
		clientProtocol: protocol.OpenAICompletions,
		operation:      execution.OperationListModels,
	}
	target := ResolvedTarget{
		modes: map[protocol.Protocol]map[execution.Operation]RouteMode{
			protocol.OpenAICompletions: {
				execution.OperationListModels: RouteConverted,
			},
		},
		resolvers: map[routeKey]spec.RouteResolver{
			key: func(string, execution.RouteMode) execution.RouteMode { return execution.RouteNative },
		},
	}

	clientProtocol, mode, ok := target.PreferredRoute(execution.OperationListModels, "")
	if !ok || clientProtocol != protocol.OpenAICompletions || mode != RouteNative {
		t.Fatalf("PreferredRoute() = %q, %q, %t; want openai-completions, native, true", clientProtocol, mode, ok)
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
