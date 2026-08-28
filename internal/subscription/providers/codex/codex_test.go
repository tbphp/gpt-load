package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

type fakeQuotaBridge struct {
	unary  cpaembedded.ExecuteResponse
	stream *cpaembedded.ExecuteStreamResponse
}

func (bridge fakeQuotaBridge) ExecuteCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return bridge.unary, nil
}

func (bridge fakeQuotaBridge) CountTokensCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return bridge.unary, nil
}

func (bridge fakeQuotaBridge) ExecuteStreamCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (*cpaembedded.ExecuteStreamResponse, error) {
	return bridge.stream, nil
}

type streamErrorBridge struct {
	response *cpaembedded.ExecuteStreamResponse
	err      error
}

func (bridge streamErrorBridge) ExecuteCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return cpaembedded.ExecuteResponse{}, errors.New("unexpected unary execution")
}

func (bridge streamErrorBridge) CountTokensCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return cpaembedded.ExecuteResponse{}, errors.New("unexpected token count")
}

func (bridge streamErrorBridge) ExecuteStreamCanonical(
	context.Context,
	string,
	cpaembedded.CodexCredential,
	cpaembedded.ExecuteRequest,
) (*cpaembedded.ExecuteStreamResponse, error) {
	return bridge.response, bridge.err
}

func TestCredentialRoundTripKeepsCPACompatibleSchema(t *testing.T) {
	raw := []byte(`{
		"type":"codex",
		"id_token":"id-token",
		"access_token":"access-token",
		"refresh_token":"refresh-token",
		"account_id":"account-1",
		"email":"owner@example.com",
		"expired":"2026-08-15T00:00:00Z",
		"last_refresh":"2026-08-14T00:00:00Z"
	}`)
	credential, err := ParseCredentialJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Credential
	if err := json.Unmarshal(canonical, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, credential) {
		t.Fatalf("round trip = %#v, want %#v", roundTrip, credential)
	}
	if got, want := credential.SecretValues(), []string{"access-token", "refresh-token", "id-token"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("secret values = %#v, want %#v", got, want)
	}
}

func TestNormalizeUpstreamErrorPreservesObservationStatus(t *testing.T) {
	err := normalizeUpstreamError(&cpaembedded.UpstreamHTTPError{Operation: "usage", StatusCode: 401})
	var upstream *UpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != 401 || upstream.Operation != "usage" {
		t.Fatalf("normalized error = %#v / %v", upstream, err)
	}
}

func TestExecutorCountsTokensWithoutCredential(t *testing.T) {
	response, err := NewExecutor().CountTokens(t.Context(), "local-token-count", Credential{}, ExecuteRequest{
		Model: "gpt-5", Format: "openai-response",
		Payload: []byte(`{"model":"gpt-5","input":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil || result.InputTokens <= 0 {
		t.Fatalf("token count = %s / %v", response.Payload, err)
	}
}

func TestExecutorDoesNotFanOutNilChunksAfterSynchronousStreamError(t *testing.T) {
	wantErr := errors.New("bootstrap rejected")
	executor := &executor{bridge: streamErrorBridge{
		response: &cpaembedded.ExecuteStreamResponse{
			Headers:             http.Header{"X-Request-Id": {"request-1"}},
			UpstreamRequestPath: "/backend-api/codex/responses",
		},
		err: wantErr,
	}}

	response, err := executor.ExecuteStream(t.Context(), "credential-1", Credential{}, ExecuteRequest{})
	if !errors.Is(err, wantErr) || response == nil || response.Chunks != nil ||
		response.UpstreamRequestPath != "/backend-api/codex/responses" {
		t.Fatalf("ExecuteStream() = %#v, %v", response, err)
	}
}

func TestExecutorPropagatesQuotaSignalsButNotForCountTokens(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	bridge := fakeQuotaBridge{unary: cpaembedded.ExecuteResponse{
		QuotaSignals: cpaembedded.QuotaSignalObservation{
			ObservedAt: observedAt,
			Signals:    map[string]string{"X-Codex-Primary-Used-Percent": "55"},
		},
	}}
	executor := &executor{bridge: bridge}

	execResponse, err := executor.Execute(t.Context(), "credential-1", Credential{}, ExecuteRequest{})
	if err != nil || !execResponse.QuotaObservedAt.Equal(observedAt) ||
		execResponse.QuotaSignals["X-Codex-Primary-Used-Percent"] != "55" {
		t.Fatalf("Execute() quota propagation = %#v, %v", execResponse, err)
	}

	countResponse, err := executor.CountTokens(t.Context(), "credential-1", Credential{}, ExecuteRequest{})
	if err != nil || !countResponse.QuotaObservedAt.IsZero() || countResponse.QuotaSignals != nil {
		t.Fatalf("CountTokens() unexpectedly propagated quota = %#v, %v", countResponse, err)
	}
}

func TestExecutorStreamPropagatesQuotaSignals(t *testing.T) {
	observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	chunks := make(chan cpaembedded.ExecuteStreamChunk)
	close(chunks)
	bridge := fakeQuotaBridge{stream: &cpaembedded.ExecuteStreamResponse{
		Chunks: chunks,
		QuotaSignals: cpaembedded.QuotaSignalObservation{
			ObservedAt: observedAt,
			Signals:    map[string]string{"X-Codex-Primary-Used-Percent": "55"},
		},
	}}
	executor := &executor{bridge: bridge}
	response, err := executor.ExecuteStream(t.Context(), "credential-1", Credential{}, ExecuteRequest{})
	if err != nil || response == nil || !response.QuotaObservedAt.Equal(observedAt) ||
		response.QuotaSignals["X-Codex-Primary-Used-Percent"] != "55" {
		t.Fatalf("ExecuteStream() quota propagation = %#v, %v", response, err)
	}
}

func TestExecuteRequestToBridgePreservesEnvironmentProxyPolicy(t *testing.T) {
	t.Parallel()

	request := executeRequestToBridge(ExecuteRequest{ProxyFromEnvironment: true})
	if !request.ProxyFromEnvironment || request.ProxyURL != "" {
		t.Fatalf("bridge proxy request = %#v", request)
	}
}

func TestExecuteRequestToBridgePreservesFixedRequestPath(t *testing.T) {
	t.Parallel()

	request := executeRequestToBridge(ExecuteRequest{RequestPath: "/v1/images/generations"})
	if request.RequestPath != "/v1/images/generations" {
		t.Fatalf("bridge request path = %q", request.RequestPath)
	}
}
