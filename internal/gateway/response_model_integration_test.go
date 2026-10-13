package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
)

func TestReportedModelOnlyAffectsObservation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, reported := range []string{"different", ""} {
			t.Run(fmt.Sprintf("stream=%t/reported=%s", stream, reported), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var request struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "upstream" {
						t.Errorf("upstream request model=%q error=%v", request.Model, err)
					}
					response := fmt.Sprintf(`{"id":"c","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, reported)
					w.Header().Set("Content-Type", "application/json")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						response = "data: " + strings.ReplaceAll(strings.ReplaceAll(response, "chat.completion", "chat.completion.chunk"), `"message":`, `"delta":`) + "\n\ndata: [DONE]\n\n"
					}
					_, _ = io.WriteString(w, response)
				}))
				defer upstream.Close()
				handler, engine, _ := websocketTestHandler(t, upstream.URL, channel.CLIProxyAPI)
				handler.dialects = dialect.NewSet(dialect.NewOpenAI())
				sink := &recordingRequestLogSink{}
				handler.requestLogSink = sink
				table, err := pricing.NewTable([]pricing.Rule{{Identity: pricing.Identity{ChannelID: string(channel.CLIProxyAPI), ModelID: "upstream"}, Prices: pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true}, Output: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true}}}})
				if err != nil {
					t.Fatal(err)
				}
				handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"public","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, stream)))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != 200 || !strings.Contains(response.Body.String(), "OK") {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				events := sink.snapshot()
				if len(events) != 1 {
					t.Fatalf("logs=%d", len(events))
				}
				e := events[0]
				want := telemetry.ModelConsistencyMismatch
				if reported == "" {
					want = telemetry.ModelConsistencyUnknown
				}
				if e.UpstreamReportedModel != reported || e.ModelConsistency != want {
					t.Fatalf("model=%q consistency=%s want=%s", e.UpstreamReportedModel, e.ModelConsistency, want)
				}
				if calls.Load() != 1 || e.Status != telemetry.RequestStatusSuccess || len(e.Attempts) != 1 || e.Attempts[0].WillRetry {
					t.Fatalf("calls=%d status=%s attempts=%d", calls.Load(), e.Status, len(e.Attempts))
				}
				if e.UpstreamModel != "upstream" || e.Usage.Pricing.UpstreamModel != "upstream" || e.Usage.Pricing.EstimatedCostNanoUSD != 3 {
					t.Fatalf("selected model=%s pricing=%+v", e.UpstreamModel, e.Usage.Pricing)
				}
			})
		}
	}
}
