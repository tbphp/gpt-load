package cpa

import (
	"context"
	"errors"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

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
