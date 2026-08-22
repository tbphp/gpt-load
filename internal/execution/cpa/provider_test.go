package cpa

import (
	"context"
	"errors"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
)

func TestProxyURLForAttemptMapsFinalModesForCPA(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		effective outboundproxy.Effective
		want      string
	}{
		{name: "unspecified inherits existing environment", want: ""},
		{name: "explicit direct", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect}, Source: outboundproxy.SourceCredential}, want: "direct"},
		{name: "environment", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeEnvironment}, Source: outboundproxy.SourceEnvironment}, want: ""},
		{name: "http", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://user:password@proxy.example.com:8080"}, Source: outboundproxy.SourceGroup}, want: "http://user:password@proxy.example.com:8080"},
		{name: "socks5", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "socks5://proxy.example.com:1080"}, Source: outboundproxy.SourceGlobal}, want: "socks5://proxy.example.com:1080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := proxyURLForAttempt(test.effective)
			if err != nil || got != test.want {
				t.Fatalf("proxyURLForAttempt() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

type requestScopedTestError struct{}

func (requestScopedTestError) Error() string         { return "request rejected" }
func (requestScopedTestError) IsRequestScoped() bool { return true }

type credentialScopedTestError bool

func (credentialScopedTestError) Error() string                { return "credential-scoped test error" }
func (err credentialScopedTestError) IsCredentialScoped() bool { return bool(err) }

type recordingProviderBridge struct {
	kind           channel.ProviderKind
	validatedRoute channel.RouteDescriptor
}

func (bridge *recordingProviderBridge) ProviderKind() channel.ProviderKind { return bridge.kind }
func (*recordingProviderBridge) UpstreamProtocol() protocol.Protocol       { return protocol.Anthropic }
func (bridge *recordingProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	bridge.validatedRoute = route
	return nil
}
func (*recordingProviderBridge) ParseCredential([]byte) (providerCredential, error) {
	return nil, nil
}
func (*recordingProviderBridge) Execute(context.Context, string, providerCredential, providerRequest) (providerResponse, error) {
	return providerResponse{}, nil
}
func (*recordingProviderBridge) ExecuteStream(context.Context, string, providerCredential, providerRequest) (*providerStreamResponse, error) {
	return nil, nil
}
func (*recordingProviderBridge) ClassifyError(context.Context, error, providerCredential) (int, *execution.ErrorEvidence) {
	return 0, nil
}

func TestAdapterDelegatesRouteValidationByProviderKind(t *testing.T) {
	codexBridge := &recordingProviderBridge{kind: channel.ProviderCodex}
	claudeBridge := &recordingProviderBridge{kind: channel.ProviderClaude}
	adapter := &Adapter{providers: indexProviderBridges(codexBridge, claudeBridge)}
	route := channel.RouteDescriptor{
		ClientProtocol: protocol.Anthropic,
		Operation:      execution.OperationChatCompletion,
		RouteMode:      execution.RouteNative,
	}
	if err := adapter.ValidateRouteCapability(channel.ProviderClaude, route); err != nil {
		t.Fatal(err)
	}
	if claudeBridge.validatedRoute.ClientProtocol != protocol.Anthropic {
		t.Fatalf("Claude route = %#v", claudeBridge.validatedRoute)
	}
	if codexBridge.validatedRoute.ClientProtocol != "" {
		t.Fatalf("Codex bridge received Claude route = %#v", codexBridge.validatedRoute)
	}
}

func TestAdapterValidatesAllDeclaredGrokRoutes(t *testing.T) {
	registry := channel.NewRegistry()
	adapter := NewAdapter(nil, registry)
	descriptor, ok := registry.Get(channel.Grok)
	if !ok {
		t.Fatal("Grok channel is missing")
	}
	for _, route := range descriptor.Routes {
		if err := adapter.ValidateRouteCapability(channel.ProviderGrok, route); err != nil {
			t.Fatalf("ValidateRouteCapability(%#v) error = %v", route, err)
		}
	}
}

func TestRequestScopedFailureSurvivesErrorWrapping(t *testing.T) {
	if !requestScopedFailure(errors.New("unrelated: " + requestScopedTestError{}.Error())) {
		// A string with the same text is deliberately not sufficient.
	} else {
		t.Fatal("plain error was classified as request-scoped")
	}
	if !requestScopedFailure(errors.Join(errors.New("outer"), requestScopedTestError{})) {
		t.Fatal("wrapped request-scoped error was not detected")
	}
}

func TestCredentialScopedFailureSurvivesErrorWrapping(t *testing.T) {
	for _, test := range []struct {
		value credentialScopedTestError
		want  bool
	}{
		{value: true, want: true},
		{value: false, want: false},
	} {
		got, known := credentialScopedFailure(errors.Join(errors.New("outer"), test.value))
		if !known || got != test.want {
			t.Fatalf("credentialScopedFailure() = %t, %t; want %t, true", got, known, test.want)
		}
	}
	if got, known := credentialScopedFailure(errors.New("plain")); got || known {
		t.Fatalf("unclassified failure = %t, %t", got, known)
	}
}

var _ providerBridge = (*recordingProviderBridge)(nil)
