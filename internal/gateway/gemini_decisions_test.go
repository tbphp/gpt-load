package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestGeminiDecisionsGatewayRoutesRedactsAndObservesUsage(t *testing.T) {
	for _, test := range []struct {
		name    string
		filters state.FilterSet
		status  int
		calls   int64
	}{
		{name: "allowed", status: http.StatusOK, calls: 1},
		{name: "Jev decisions key denied", filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Decisions: {}}}, status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
					return
				}
				// input、instructions 与 criteria 都按决策请求的正文脱敏。
				if request.URL.Path != "/v1beta/decisions" || request.Header.Get("X-Goog-Api-Key") != "sk-upstream" ||
					!bytes.Contains(body, []byte(`"model":"gemini-decision-1"`)) ||
					bytes.Contains(body, []byte("order-sentinel")) || bytes.Count(body, []byte("[VALUE]")) != 3 {
					t.Errorf("upstream request %s %s", request.URL, body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"answers":{"tier":{"type":"choice","choice":"broken","probabilities":{"broken":0.9,"fine":0.1},"confidence":0.9}},"usage":{"total_input_tokens":1000000,"total_output_tokens":0,"total_tokens":1000000}}`)
			}))
			defer server.Close()

			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t, newTestExecutionForwarder(t), &recordingAccessKeyRPMLimiter{}, sink, "sk-upstream",
			)
			handler.dialects = dialect.NewSet(dialect.NewGeminiDecisions())
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry:  channel.NewRegistry(),
				RequestRedaction: []requestredact.Rule{{Pattern: "order-sentinel", Replacement: "[VALUE]"}},
				Groups: []state.GroupConfig{{
					ConnectionType: "api_key", ID: 1, Name: "gemini", ChannelID: channel.Gemini,
					Params: json.RawMessage(`{"base_url":"` + server.URL + `/v1beta"}`),
					Models: []state.ModelConfig{{ID: "gemini-decision-1", Alias: "public"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive, Filters: test.filters,
				}},
			}); err != nil {
				t.Fatal(err)
			}
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: string(channel.Gemini), ModelID: "gemini-decision-1"},
				Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}

			request := httptest.NewRequest(http.MethodPost, "/v1beta/decisions", strings.NewReader(
				`{"model":"public","input":"Order order-sentinel arrived cracked.","questions":{"tier":{"type":"choice","instructions":"Is order-sentinel broken?","criteria":{"broken":"order-sentinel is broken","fine":""}}}}`,
			))
			request.Header.Set("X-Goog-Api-Key", "gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || calls.Load() != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
			if test.calls == 0 {
				return
			}
			if !strings.Contains(response.Body.String(), `"choice":"broken"`) {
				t.Fatalf("response changed: %s", response.Body)
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Protocol != protocol.GeminiDecisions ||
				events[0].Operation != execution.OperationDecisionsCreate {
				t.Fatalf("events = %#v", events)
			}
			observation := events[0].Usage
			if observation.Result.State != usage.StateComplete || observation.Result.Tokens.UncachedInput != 1_000_000 ||
				observation.Pricing.EstimatedCostNanoUSD != 100_000_000 || observation.Pricing.CostState != string(pricing.CostStatePriced) {
				t.Fatalf("usage = %#v", observation)
			}
		})
	}
}

func TestVisibleGeminiModelIDsIncludesGeminiDecisionsRoutes(t *testing.T) {
	t.Parallel()

	snapshot := &state.ConfigSnapshot{ExecutionCandidates: state.ExecutionCandidateIndex{
		protocol.Gemini: {
			execution.OperationChatCompletion: {"gemini-chat": {{GroupID: 1}}},
		},
		protocol.GeminiDecisions: {
			execution.OperationDecisionsCreate: {"gemini-decision-1": {{GroupID: 2}}},
		},
	}}
	for _, test := range []struct {
		name      string
		protocols map[protocol.Protocol]struct{}
		want      []string
	}{
		{name: "unrestricted union", want: []string{"gemini-chat", "gemini-decision-1"}},
		{name: "decisions protocol filter", protocols: map[protocol.Protocol]struct{}{protocol.GeminiDecisions: {}}, want: []string{"gemini-decision-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			accessKey := state.AccessKeyView{Filters: state.FilterSet{Protocols: test.protocols}}
			if got := visibleModelIDs(snapshot, accessKey, protocol.Gemini); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("visibleModelIDs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestGeminiDecisionsRequestLogFreezesProviderInputEcho(t *testing.T) {
	t.Parallel()

	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(
		sink,
		"req-gemini-decisions-error",
		time.Unix(100, 0),
		9,
		protocol.GeminiDecisions,
		func() time.Time { return time.Unix(101, 0) },
	)
	recorder.setOperation(execution.OperationDecisionsCreate)
	providerSummary := "invalid input=input-sentinel"
	index := recorder.appendAttempt(
		requestLogSelection(12, 22, "gemini-decisions"),
		UpstreamResult{StatusCode: http.StatusBadRequest},
		telemetry.FailureCategoryClientError,
		telemetry.ActionTerminate,
		"upstream_client_error",
		providerSummary,
		time.Unix(100, 0),
		time.Unix(100, 0),
	)
	recorder.completeResponse(
		UpstreamResult{StatusCode: http.StatusBadRequest, ErrorSummary: providerSummary},
		health.Decision{Category: health.FailureCategoryClientError},
		"gemini-decision-1",
		index,
	)
	recorder.emit()

	if len(sink.events) != 1 {
		t.Fatalf("events = %#v", sink.events)
	}
	encoded, _ := json.Marshal(sink.events[0])
	if bytes.Contains(encoded, []byte("input-sentinel")) {
		t.Fatalf("Gemini Decisions request log retained provider input echo: %s", encoded)
	}
}
