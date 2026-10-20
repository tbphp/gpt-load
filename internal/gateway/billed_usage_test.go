package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestHandlerLogsDeferredToolSearchBilledUsageOnHTTPFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int32
	}{
		{name: "rejected before model work", failAt: 1},
		{name: "failed after billed tool search", failAt: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if calls.Add(1) >= test.failAt {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"error":{"code":500,"message":"upstream failure","status":"INTERNAL"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"responseId":"first","modelVersion":"gemini-test","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bifrost_tool_search","args":{"query":"select:lookup"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":12}}`)
			}))
			defer server.Close()

			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t, newTestExecutionForwarder(t), &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
			)
			handler.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{
					ConnectionType: "api_key", ID: 1, Name: "gemini", ChannelID: channel.Gemini,
					Params: json.RawMessage(`{"base_url":"` + server.URL + `/v1beta"}`),
					Models: []state.ModelConfig{{ID: "gemini-test"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{{
					ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
					Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
				}},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
					Status: state.AccessKeyStatusActive,
				}},
			}); err != nil {
				t.Fatal(err)
			}
			price := pricing.Price{NanoUSDPerMillion: 1_000_000_000, Set: true}
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: string(channel.Gemini), ModelID: "gemini-test"},
				Prices:   pricing.Prices{Input: price, CacheRead: price, Output: price},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
				`{"model":"gemini-test","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","defer_loading":true,"parameters":{"type":"object","properties":{}}}]}`,
			))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			events := sink.snapshot()
			if calls.Load() != test.failAt || response.Code != http.StatusInternalServerError ||
				len(events) != 1 || events[0].Status != telemetry.RequestStatusError || len(events[0].Attempts) != 1 {
				t.Fatalf("calls=%d response=%d events=%+v", calls.Load(), response.Code, events)
			}
			want := usage.Result{State: usage.StateNotApplicable}
			wantPricing := telemetry.PricingObservation{
				UpstreamModel: "gemini-test", CostState: string(pricing.CostStateNotApplicable),
				PricingCompleteness: string(pricing.CompletenessNotApplicable),
			}
			if test.failAt == 2 {
				want = usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 6, CacheRead: 4, Output: 2}}
				wantPricing.CostState = string(pricing.CostStatePriced)
				wantPricing.PricingCompleteness = string(pricing.CompletenessPartial)
				wantPricing.EstimatedCostNanoUSD = 12_000
			}
			observation := events[0].Usage
			if observation.Result != want || withoutPricingReceipt(observation.Pricing) != wantPricing ||
				observation.GroupID != 1 || observation.ChannelID != channel.Gemini ||
				observation.CredentialID != 1 || observation.AttemptSequence != 1 {
				t.Fatalf("usage=%+v, want result=%+v pricing=%+v", observation, want, wantPricing)
			}
		})
	}
}
