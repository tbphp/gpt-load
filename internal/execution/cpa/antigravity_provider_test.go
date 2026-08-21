package cpa

import (
	"context"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/subscription/providers/antigravity"
)

type recordingAntigravityExecutor struct {
	request      antigravity.ExecuteRequest
	response     []byte
	streamChunks [][]byte
}

type antigravityClassifiedTestError struct {
	status int
	typeID string
	code   string
}

type antigravityRetryAfterTestError struct {
	antigravityClassifiedTestError
	retryAfter time.Duration
}

func (err antigravityRetryAfterTestError) RetryAfter() *time.Duration {
	return &err.retryAfter
}

func (err antigravityClassifiedTestError) Error() string   { return "provider body must not escape" }
func (err antigravityClassifiedTestError) StatusCode() int { return err.status }
func (err antigravityClassifiedTestError) ErrorType() string {
	return err.typeID
}
func (err antigravityClassifiedTestError) ErrorCode() string { return err.code }

func (executor *recordingAntigravityExecutor) Execute(
	_ context.Context,
	_ string,
	_ antigravity.Credential,
	request antigravity.ExecuteRequest,
) (antigravity.ExecuteResponse, error) {
	executor.request = request
	payload := executor.response
	if payload == nil {
		payload = []byte(`{"ok":true}`)
	}
	return antigravity.ExecuteResponse{Payload: append([]byte(nil), payload...)}, nil
}

func (executor *recordingAntigravityExecutor) CountTokens(
	ctx context.Context,
	credentialID string,
	credential antigravity.Credential,
	request antigravity.ExecuteRequest,
) (antigravity.ExecuteResponse, error) {
	return executor.Execute(ctx, credentialID, credential, request)
}

func (executor *recordingAntigravityExecutor) ExecuteStream(
	ctx context.Context,
	_ string,
	_ antigravity.Credential,
	request antigravity.ExecuteRequest,
) (*antigravity.ExecuteStreamResponse, error) {
	executor.request = request
	chunks := make(chan antigravity.ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for _, payload := range executor.streamChunks {
			select {
			case chunks <- antigravity.ExecuteStreamChunk{Payload: append([]byte(nil), payload...)}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &antigravity.ExecuteStreamResponse{Chunks: chunks}, nil
}

func antigravityProviderTestCredential() antigravityProviderCredential {
	return antigravityProviderCredential{value: antigravity.Credential{
		Type: "antigravity", AccessToken: "access", RefreshToken: "refresh", AccountID: "account",
		Email: "owner@example.com", ProjectID: "project", Expire: "2030-01-01T00:00:00Z",
	}}
}

func TestAntigravityProviderNormalizesPlaceholderResponseModels(t *testing.T) {
	tests := []struct {
		name   string
		format string
		body   string
	}{
		{name: "OpenAI Chat", format: "openai", body: `{"model":"gemini-default","choices":[]}`},
		{name: "OpenAI Responses", format: "openai-response", body: `{"model":"gemini-default","object":"response"}`},
		{name: "Anthropic", format: "claude", body: `{"model":"gemini-default","type":"message"}`},
		{name: "Gemini", format: "gemini", body: `{"modelVersion":"gemini-default","candidates":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingAntigravityExecutor{response: []byte(test.body)}
			response, err := (&antigravityProviderBridge{executor: executor}).Execute(
				t.Context(), "17", antigravityProviderTestCredential(),
				providerRequest{Model: "gemini-3-flash-agent", Payload: []byte(`{}`), Format: test.format},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(response.Payload); !strings.Contains(got, "gemini-3-flash-agent") || strings.Contains(got, "gemini-default") {
				t.Fatalf("response payload = %s", got)
			}
		})
	}
}

func TestAntigravityProviderPreservesActualReportedModel(t *testing.T) {
	executor := &recordingAntigravityExecutor{response: []byte(`{"model":"routed-model","choices":[]}`)}
	response, err := (&antigravityProviderBridge{executor: executor}).Execute(
		t.Context(), "17", antigravityProviderTestCredential(),
		providerRequest{Model: "requested-model", Payload: []byte(`{}`), Format: "openai"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(response.Payload); !strings.Contains(got, "routed-model") || strings.Contains(got, "requested-model") {
		t.Fatalf("response payload = %s", got)
	}
}

func TestAntigravityProviderNormalizesPlaceholderStreamModels(t *testing.T) {
	tests := []struct {
		name   string
		format string
		chunk  string
	}{
		{name: "OpenAI Chat", format: "openai", chunk: "data: {\"model\":\"gemini-default\",\"choices\":[]}\n\n"},
		{name: "OpenAI Responses", format: "openai-response", chunk: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"model\":\"gemini-default\"}}\n\n"},
		{name: "Anthropic", format: "claude", chunk: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"gemini-default\"}}\n\n"},
		{name: "Gemini", format: "gemini", chunk: `{"modelVersion":"gemini-default","candidates":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingAntigravityExecutor{streamChunks: [][]byte{[]byte(test.chunk)}}
			response, err := (&antigravityProviderBridge{executor: executor}).ExecuteStream(
				t.Context(), "17", antigravityProviderTestCredential(),
				providerRequest{Model: "gemini-3-flash-agent", Payload: []byte(`{}`), Format: test.format},
			)
			if err != nil {
				t.Fatal(err)
			}
			var payload strings.Builder
			for chunk := range response.Chunks {
				if chunk.Err != nil {
					t.Fatal(chunk.Err)
				}
				_, _ = payload.Write(chunk.Payload)
			}
			if value := payload.String(); !strings.Contains(value, "gemini-3-flash-agent") || strings.Contains(value, "gemini-default") {
				t.Fatalf("stream payload = %s", value)
			}
		})
	}
}

func TestAntigravityProviderPreservesActualReportedStreamModel(t *testing.T) {
	executor := &recordingAntigravityExecutor{streamChunks: [][]byte{
		[]byte("data: {\"model\":\"routed-model\",\"choices\":[]}\n\n"),
	}}
	response, err := (&antigravityProviderBridge{executor: executor}).ExecuteStream(
		t.Context(), "17", antigravityProviderTestCredential(),
		providerRequest{Model: "requested-model", Payload: []byte(`{}`), Format: "openai"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var payload strings.Builder
	for chunk := range response.Chunks {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		_, _ = payload.Write(chunk.Payload)
	}
	if got := payload.String(); !strings.Contains(got, "routed-model") || strings.Contains(got, "requested-model") {
		t.Fatalf("stream payload = %s", got)
	}
}

func TestAntigravityProviderScopesContinuityPerCredentialAndModel(t *testing.T) {
	executor := &recordingAntigravityExecutor{}
	bridge := &antigravityProviderBridge{executor: executor}
	_, err := bridge.Execute(context.Background(), "17", antigravityProviderCredential{value: antigravity.Credential{
		Type: "antigravity", AccessToken: "access", RefreshToken: "refresh", AccountID: "account",
		Email: "owner@example.com", ProjectID: "project", Expire: "2030-01-01T00:00:00Z",
	}}, providerRequest{Model: "gemini-live", Payload: []byte(`{}`), Format: "gemini", ContinuityKey: "tenant-hmac"})
	if err != nil {
		t.Fatal(err)
	}
	if executor.request.ContinuityKey == "tenant-hmac" || !strings.Contains(executor.request.ContinuityKey, "tenant-hmac") ||
		!strings.Contains(executor.request.ContinuityKey, "17") || !strings.Contains(executor.request.ContinuityKey, "gemini-live") {
		t.Fatalf("continuity key = %q", executor.request.ContinuityKey)
	}
}

func TestAntigravityProviderRejectsKnownUnsupportedInputs(t *testing.T) {
	bridge := &antigravityProviderBridge{}
	validator, ok := any(bridge).(providerRequestValidator)
	if !ok {
		t.Fatal("Antigravity bridge must validate provider-specific unsupported inputs")
	}

	tests := []struct {
		name    string
		format  string
		payload string
		wantErr bool
	}{
		{
			name: "OpenAI chat remote image", format: "openai", wantErr: true,
			payload: `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}]}`,
		},
		{
			name: "Responses remote image", format: "openai-response", wantErr: true,
			payload: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]}`,
		},
		{
			name: "Responses builtin tool", format: "openai-response", wantErr: true,
			payload: `{"tools":[{"type":"web_search_preview"}],"input":"hello"}`,
		},
		{
			name: "Responses image output model", format: "openai-response", wantErr: true,
			payload: `{"input":"draw a logo"}`,
		},
		{
			name: "Anthropic remote image", format: "claude", wantErr: true,
			payload: `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.test/image.png"}}]}]}`,
		},
		{
			name: "Supported data URL and function tool", format: "openai-response",
			payload: `{"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := "gemini-live"
			if test.name == "Responses image output model" {
				model = "gemini-3.1-flash-image"
			}
			err := validator.ValidateRequest(providerRequest{Model: model, Format: test.format, Payload: []byte(test.payload)})
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateRequest() error = %v, want error = %t", err, test.wantErr)
			}
		})
	}
}

func TestAntigravityProviderTreatsForbiddenAsCandidateUnavailable(t *testing.T) {
	bridge := &antigravityProviderBridge{}
	_, evidence := bridge.ClassifyError(t.Context(), statusError{
		status: 403, message: `{"error":{"status":"PERMISSION_DENIED"}}`,
	}, antigravityProviderCredential{value: antigravity.Credential{
		Type: "antigravity", AccessToken: "access", RefreshToken: "refresh", AccountID: "account",
		Email: "owner@example.com", ProjectID: "project", Expire: "2030-01-01T00:00:00Z",
	}})
	if evidence == nil || evidence.Hint != execution.FailureHintCandidateUnavailable ||
		evidence.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing ||
		strings.Contains(evidence.Summary, "PERMISSION_DENIED") {
		t.Fatalf("error evidence = %#v", evidence)
	}
}

func TestAntigravityProviderClassifiesOAuthAndPaidCreditErrors(t *testing.T) {
	bridge := &antigravityProviderBridge{}
	credential := antigravityProviderCredential{value: antigravity.Credential{
		Type: "antigravity", AccessToken: "access", RefreshToken: "refresh", AccountID: "account",
		Email: "owner@example.com", ProjectID: "project", Expire: "2030-01-01T00:00:00Z",
	}}
	tests := []struct {
		name       string
		err        error
		wantHint   execution.FailureHint
		wantReplay execution.ReplaySafety
		wantRetry  time.Duration
	}{
		{
			name: "OAuth 401 refreshes once", err: antigravityClassifiedTestError{status: 401, typeID: "UNAUTHENTICATED"},
			wantHint: execution.FailureHintRefreshRequired, wantReplay: execution.ReplaySafetyRejectedBeforeProcessing,
		},
		{
			name: "paid credit balance advances to another credential", err: antigravityClassifiedTestError{status: 429, typeID: "RESOURCE_EXHAUSTED", code: "INSUFFICIENT_G1_CREDITS_BALANCE"},
			wantHint: execution.FailureHintRateLimited, wantReplay: execution.ReplaySafetyRejectedBeforeProcessing,
		},
		{
			name: "rate limit retains fractional retry delay", err: antigravityRetryAfterTestError{
				antigravityClassifiedTestError: antigravityClassifiedTestError{status: 429, typeID: "RESOURCE_EXHAUSTED", code: "RATE_LIMIT_EXCEEDED"},
				retryAfter:                     450 * time.Millisecond,
			},
			wantHint: execution.FailureHintRateLimited, wantRetry: 450 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, evidence := bridge.ClassifyError(t.Context(), test.err, credential)
			if evidence == nil || evidence.Hint != test.wantHint || evidence.ReplaySafety != test.wantReplay ||
				strings.Contains(evidence.Summary, "provider body") || evidence.RetryAfter != test.wantRetry {
				t.Fatalf("error evidence = %#v", evidence)
			}
		})
	}
}
