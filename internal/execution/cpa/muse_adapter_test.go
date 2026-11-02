package cpa

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/muse"
)

type fakeMuseStreamExecutor struct {
	stream *muse.ExecuteStreamResponse
}

func (*fakeMuseStreamExecutor) Execute(context.Context, string, muse.Credential, muse.ExecuteRequest) (muse.ExecuteResponse, error) {
	return muse.ExecuteResponse{}, nil
}

func (*fakeMuseStreamExecutor) CountTokens(context.Context, muse.ExecuteRequest) (muse.ExecuteResponse, error) {
	return muse.ExecuteResponse{}, nil
}

func (executor *fakeMuseStreamExecutor) ExecuteStream(context.Context, string, muse.Credential, muse.ExecuteRequest) (*muse.ExecuteStreamResponse, error) {
	return executor.stream, nil
}

func TestMuseAdapterGroupsNativeResponsesSSELinesIntoCompleteEvents(t *testing.T) {
	canonical := []byte(`{"type":"muse","access_token":"minted-key","dca_token":"dca:test","email":"muse@example.com"}`)
	adapter, _, _, keyService, row := newSubscriptionAdapterFixture(t, string(channel.Muse), canonical, "muse@example.com")
	bridge, ok := adapter.providers[channel.ProviderMuse].(*museProviderBridge)
	if !ok {
		t.Fatal("Muse provider bridge is unavailable")
	}
	chunks := make(chan muse.ExecuteStreamChunk, 2)
	chunks <- muse.ExecuteStreamChunk{Payload: []byte("event: response.completed")}
	chunks <- muse.ExecuteStreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp_1"}}`)}
	close(chunks)
	bridge.executor = &fakeMuseStreamExecutor{stream: &muse.ExecuteStreamResponse{Chunks: chunks}}

	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "request-1", AttemptID: "attempt-1", Sequence: 1,
		ChannelID: string(channel.Muse), RouteMode: execution.RouteNative,
		ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate,
		ClientModel: "muse-spark-1.3", UpstreamModel: "muse-spark-1.3", Method: http.MethodPost, Path: "/v1/responses",
		Body:       []byte(`{"model":"muse-spark-1.3","input":"hi"}`),
		Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext)),
	})
	var wire string
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			wire += string(event.Data)
		}
		return nil
	})
	if result.Error != nil {
		t.Fatalf("ExecuteStream() error = %#v", result.Error)
	}
	want := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"
	if wire != want {
		t.Fatalf("wire = %q, want %q", wire, want)
	}
}

var _ muse.Executor = (*fakeMuseStreamExecutor)(nil)

func TestMuseLocalTokenCountIsMarkedAsEstimate(t *testing.T) {
	bridge := newMuseProviderBridge()
	request := providerRequest{Model: "muse-spark-1.3", Format: "openai-response", Payload: []byte(`{"model":"muse-spark-1.3","input":"hello"}`)}
	if err := bridge.ValidateLocalTokenCount(request); err != nil {
		t.Fatal(err)
	}
	response, err := bridge.CountTokensLocal(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Headers.Get(localTokenCountHeader) != "local-estimate" || !strings.Contains(string(response.Payload), `"object":"response.input_tokens"`) {
		t.Fatal("local token estimate has no explicit marker")
	}
	request.Payload = []byte(`{"model":"muse-spark-1.3","input":"hi","previous_response_id":"saved"}`)
	if err := bridge.ValidateLocalTokenCount(request); err == nil {
		t.Fatal("local counter accepted unknown server-side context")
	}
}

type museQuotaTestError struct{ grokProviderTestError }

func (museQuotaTestError) IsCredentialScoped() bool { return true }

func TestMuseProviderKeepsSubscriptionQuotaCredentialScoped(t *testing.T) {
	bridge := newMuseProviderBridge()
	_, evidence := bridge.ClassifyError(t.Context(), museQuotaTestError{grokProviderTestError{status: 429, code: "rate_limit_exceeded"}}, museProviderCredential{})
	if evidence.ScopeHint != execution.ErrorScopeCredential || evidence.Hint != execution.FailureHintRateLimited {
		t.Fatalf("quota scope: %#v", evidence)
	}
	_, evidence = bridge.ClassifyError(t.Context(), grokProviderTestError{status: 401}, museProviderCredential{})
	if evidence.Hint != execution.FailureHintRefreshRequired {
		t.Fatal("unauthorized response did not request a managed refresh")
	}
}
