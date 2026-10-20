package bifrost

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestDeferredToolSearchRetainsUsageAcrossUpstreamCalls(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, failSecond := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/fail_second=%t", stream, failSecond), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					call := calls.Add(1)
					if call == 2 && failSecond {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"error":{"code":500,"message":"upstream failure","status":"INTERNAL"}}`)
						return
					}
					body := `{"responseId":"second","modelVersion":"gemini-test","candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":6,"candidatesTokenCount":3,"totalTokenCount":23}}`
					if call == 1 {
						body = `{"responseId":"first","modelVersion":"gemini-test","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bifrost_tool_search","args":{"query":"select:lookup"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":12}}`
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, body)
					}
				}))
				defer server.Close()

				runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, geminiBaseURL: server.URL + "/v1beta"})
				spec := convertedSpec(channel.Gemini, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", []byte(fmt.Sprintf(
					`{"model":"client-model","input":"hello","stream":%t,"store":false,"tools":[{"type":"function","name":"lookup","defer_loading":true,"parameters":{"type":"object","properties":{}}}]}`, stream,
				)))
				var evidence *execution.UsageEvidence
				var failure *execution.ErrorEvidence
				if stream {
					result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
					if err := result.Validate(); err != nil {
						t.Fatal(err)
					}
					evidence, failure = result.Usage, result.Error
				} else {
					result := runtime.Execute(t.Context(), spec)
					if err := result.Validate(); err != nil {
						t.Fatal(err)
					}
					evidence, failure = result.Usage, result.Error
				}
				if calls.Load() != 2 || (failure != nil) != failSecond {
					t.Fatalf("upstream calls = %d, failure = %+v", calls.Load(), failure)
				}
				want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 20, CacheRead: 10, Output: 5}}
				if failSecond {
					want = usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 2}}
				}
				if evidence == nil || evidence.Normalized != want {
					t.Fatalf("usage = %+v, want %+v", evidence, want)
				}
			})
		}
	}
}

func TestStreamBilledUsageReplacesSnapshotWithoutDoubleCounting(t *testing.T) {
	snapshot := &execution.UsageEvidence{
		Normalized: usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 10, Output: 2}},
		Raw:        []byte(`{"input_tokens":10,"output_tokens":2}`),
	}
	for _, test := range []struct {
		name   string
		billed *schemas.BifrostLLMUsage
		want   usage.Result
	}{
		{name: "preserve snapshot without billed usage", want: snapshot.Normalized},
		{
			name:   "replace snapshot with cumulative billed usage",
			billed: &schemas.BifrostLLMUsage{PromptTokens: 30, CompletionTokens: 7, TotalTokens: 37},
			want:   usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 30, Output: 7}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: "upstream failure"},
				ExtraFields: schemas.BifrostErrorExtraFields{
					BilledUsage: test.billed,
				},
			}
			result := streamErrorResult(failure, nil, nil, true, http.StatusOK, nil, "model", snapshot)
			if result.Usage == nil || result.Usage.Normalized != test.want {
				t.Fatalf("usage = %+v, want %+v", result.Usage, test.want)
			}
			result.Usage.Raw[0] = ' '
			if snapshot.Raw[0] != '{' {
				t.Fatal("error result changed the existing usage snapshot")
			}
		})
	}
}

func TestBilledUsagePreventsPreDispatchClassification(t *testing.T) {
	for _, field := range []*schemas.ErrorField{
		{Error: &net.DNSError{Err: "no such host", Name: "missing.invalid"}},
		{Type: schemas.Ptr("invalid_request_error"), Message: "cannot marshal next turn"},
	} {
		failure := &schemas.BifrostError{
			IsBifrostError: true, Error: field,
			ExtraFields: schemas.BifrostErrorExtraFields{
				BilledUsage: &schemas.BifrostLLMUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			},
		}
		unary := convertedUnaryErrorResult(failure, nil, nil)
		stream := convertedStreamErrorResult(failure, nil, nil, false, 0, nil, "", nil)
		if unary.DispatchState != execution.DispatchMaybeSent || stream.DispatchState != execution.DispatchMaybeSent {
			t.Fatalf("billed upstream work was marked unsent: unary=%s stream=%s", unary.DispatchState, stream.DispatchState)
		}
	}
}
