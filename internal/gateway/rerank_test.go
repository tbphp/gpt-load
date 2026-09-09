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
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

func TestRerankGatewayRoutesObservesAndProtectsAccess(t *testing.T) {
	for _, test := range []struct {
		name, response string
		filters        state.FilterSet
		status, calls  int
		priced         bool
	}{
		{name: "tokens", response: `"usage":{"total_tokens":1000000}`, status: 200, calls: 1, priced: true},
		{name: "units only", response: `"meta":{"billed_units":{"search_units":1}}`, status: 200, calls: 1},
		{name: "protocol denied", response: `"usage":{"total_tokens":1}`, filters: state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAIEmbeddings: {}}}, status: 503},
		{name: "model denied", response: `"usage":{"total_tokens":1}`, filters: state.FilterSet{Models: map[string]struct{}{"other": {}}}, status: 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path != "/team/v1/rerank" || r.Header.Get("Authorization") != "Bearer sk-upstream" || !bytes.Contains(body, []byte(`"model":"provider-model"`)) || !bytes.Contains(body, []byte(`"top_n":1`)) {
					t.Errorf("upstream request %s %s", r.URL, body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"model":"provider-model","results":[{"index":1,"relevance_score":0.1234567890123456789,"document":{"text":"document-sentinel"}}],`+test.response+`}`)
			}))
			defer server.Close()
			runtime, err := bifrostexecutor.NewRuntime(context.Background(), channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Shutdown()
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(t, NewExecutionForwarder(runtime), &recordingAccessKeyRPMLimiter{}, sink, "sk-upstream")
			handler.dialects = dialect.NewSet(dialect.NewRerank())
			params, _ := json.Marshal(map[string]string{"base_url": server.URL + "/team"})
			_, err = manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "rerank", ChannelID: channel.NewAPI, Params: params, Models: []state.ModelConfig{{ID: "provider-model", Alias: "public"}}, Enabled: true,
					Settings: config.Settings{"parameter_overrides": []any{map[string]any{"match": map[string]any{"protocol": "rerank"}, "set": map[string]any{"top_n": 1}}}},
				}},
				Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
				AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive, Filters: test.filters}},
			})
			if err != nil {
				t.Fatal(err)
			}
			table, err := pricing.NewTable([]pricing.Rule{{Identity: pricing.Identity{ChannelID: string(channel.NewAPI), ModelID: "provider-model"}, Prices: pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 2_000_000_000, Set: true}}}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			request := httptest.NewRequest(http.MethodPost, "/v1/rerank", strings.NewReader(`{"model":"public","query":"query-sentinel","documents":["first","document-sentinel"]}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || calls.Load() != int64(test.calls) {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
			if test.calls == 0 {
				return
			}
			if !strings.Contains(response.Body.String(), `"relevance_score":0.1234567890123456789`) || !strings.Contains(response.Body.String(), `"model":"public"`) {
				t.Fatalf("response changed: %s", response.Body)
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Protocol != protocol.Rerank || events[0].Operation != execution.OperationRerank {
				t.Fatalf("events=%#v", events)
			}
			observation := events[0].Usage
			if test.priced {
				if observation.Result.State != usage.StateComplete || observation.Result.Tokens.UncachedInput != 1_000_000 || observation.Pricing.EstimatedCostNanoUSD != 2_000_000_000 || observation.Pricing.CostState != "priced" {
					t.Fatalf("usage=%#v", observation)
				}
			} else if observation.Result.State != usage.StateMissing || observation.Pricing.CostState != "unpriced" {
				t.Fatalf("usage=%#v", observation)
			}
			encoded, err := json.Marshal(events)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"query-sentinel", "document-sentinel", "sk-upstream"} {
				if bytes.Contains(encoded, []byte(secret)) {
					t.Fatalf("input leaked into log: %s", secret)
				}
			}
		})
	}
}
