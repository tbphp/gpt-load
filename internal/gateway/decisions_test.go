package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

func TestDecisionsGatewayRoutesObservesAndProtectsAccess(t *testing.T) {
	for _, test := range []struct {
		name    string
		filters state.FilterSet
		status  int
		calls   int64
		redact  bool
	}{
		{name: "allowed", status: http.StatusOK, calls: 1},
		{name: "redacted state", status: http.StatusOK, calls: 1, redact: true},
		{name: "protocol denied", filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}}}, status: http.StatusServiceUnavailable},
		{name: "model denied", filters: state.FilterSet{Models: map[string]struct{}{"other": {}}}, status: http.StatusServiceUnavailable},
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
				stateText := `"task":"decision-input-sentinel"`
				if test.redact {
					stateText = `"task":"[VALUE]"`
				}
				if request.URL.Path != "/v1/systemone" || request.Header.Get("Authorization") != "Bearer sk-upstream" ||
					!bytes.Contains(body, []byte(`"model":"jev-latest"`)) ||
					!bytes.Contains(body, []byte(stateText)) ||
					!bytes.Contains(body, []byte(`"future":1.2300`)) {
					t.Errorf("upstream request %s %s", request.URL, body)
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-Id", "decision-request")
				_, _ = io.WriteString(writer, `{"id":"decision-1","model":"jev-latest","provider":"Typesafe","answers":{"tier":{"choice":"low","confidence":0.9}},"usage":{"input_tokens":1000000,"output_tokens":0,"cost":0.042},"future":1.2300}`)
			}))
			defer server.Close()

			runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown()
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t,
				NewExecutionForwarder(runtime),
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-upstream",
			)
			handler.dialects = dialect.NewSet(dialect.NewDecisions())
			params, _ := json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
			redactionRules := []requestredact.Rule{}
			if test.redact {
				redactionRules = []requestredact.Rule{{Pattern: "decision-input-sentinel", Replacement: "[VALUE]"}}
			}
			_, err = manager.Publish(state.CompileInput{
				ChannelRegistry:  channel.NewRegistry(),
				RequestRedaction: redactionRules,
				Groups: []state.GroupConfig{{
					ConnectionType: "api_key", ID: 1, Name: "jev", ChannelID: channel.Jev,
					Params: params, Models: []state.ModelConfig{{ID: "jev-latest", Alias: "public"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive, Filters: test.filters,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: string(channel.Jev), ModelID: "jev-latest"},
				Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 42_000_000, Set: true}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			request := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"public","state":{"task":"decision-input-sentinel"},"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}},"future":1.2300}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || calls.Load() != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
			if test.calls == 0 {
				return
			}
			if !strings.Contains(response.Body.String(), `"model":"public"`) ||
				!strings.Contains(response.Body.String(), `"provider":"Typesafe"`) ||
				!strings.Contains(response.Body.String(), `"future":1.2300`) {
				t.Fatalf("response changed: %s", response.Body)
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Protocol != protocol.Decisions ||
				events[0].Operation != execution.OperationDecisionsCreate {
				t.Fatalf("events = %#v", events)
			}
			observation := events[0].Usage
			if observation.Result.State != usage.StateComplete || observation.Result.Tokens.UncachedInput != 1_000_000 ||
				observation.Pricing.EstimatedCostNanoUSD != 42_000_000 || observation.Pricing.CostState != "priced" {
				t.Fatalf("usage = %#v", observation)
			}
			encoded, err := json.Marshal(events)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"decision-input-sentinel", "sk-upstream"} {
				if bytes.Contains(encoded, []byte(secret)) {
					t.Fatalf("input leaked into log: %s", secret)
				}
			}
		})
	}
}
