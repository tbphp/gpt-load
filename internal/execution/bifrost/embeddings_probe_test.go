package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIEmbeddingsProbeUsesMinimalNativeWire(t *testing.T) {
	t.Parallel()

	for _, responseBody := range []string{
		`{"object":"list","data":[{"index":0,"embedding":[0.1]}]}`,
		`{"object":"list","data":[{"index":0,"embedding":"AAAA"}]}`,
	} {
		responseBody := responseBody
		t.Run(responseBody, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Method != http.MethodPost || request.URL.Path != "/tenant/api/v4/embeddings" {
					t.Errorf("probe target = %s %s", request.Method, request.URL.Path)
				}
				if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
					t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read probe body: %v", err)
					return
				}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Errorf("decode probe body: %v", err)
					return
				}
				if len(payload) != 2 || string(payload["model"]) != `"probe-upstream"` ||
					string(payload["input"]) != `"ping"` {
					t.Errorf("probe body = %s", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "embedding-probe")
				_, _ = io.WriteString(writer, responseBody)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), embeddingsProbeSpec(
				t, channel.OpenAICompatible, server.URL+"/tenant/api/v4",
			))
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if calls.Load() != 1 || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != protocol.OpenAIEmbeddings ||
				result.UpstreamRequestID != "embedding-probe" {
				t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
			}
		})
	}
}

func TestOpenAIEmbeddingsProbeRejectsInvalidSuccessShape(t *testing.T) {
	t.Parallel()

	for _, responseBody := range []string{
		`{"object":"list","data":[]}`,
		`{"object":"list","data":[{"index":0}]}`,
		`{"object":"list","data":[{"index":0,"embedding":null}]}`,
	} {
		responseBody := responseBody
		t.Run(responseBody, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, responseBody)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			result := runtime.Execute(context.Background(), embeddingsProbeSpec(
				t, channel.OpenAICompatible, server.URL+"/v1",
			))
			if err := result.Validate(); err != nil {
				t.Fatalf("result validation: %v; result=%+v", err, result)
			}
			if result.Error == nil || result.StatusCode != http.StatusOK ||
				result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted {
				t.Fatalf("invalid probe result = %+v", result)
			}
		})
	}
}

func embeddingsProbeSpec(
	t *testing.T,
	channelID channel.ID,
	baseURL string,
) execution.AttemptSpec {
	t.Helper()
	target, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	spec := utilitySpec(
		channelID,
		protocol.OpenAIEmbeddings,
		execution.OperationProbe,
		"",
		"",
		nil,
	)
	spec.TargetConfig = target
	spec.ClientModel = "probe-client"
	spec.UpstreamModel = "probe-upstream"
	return freezeTestAttempt(spec)
}
