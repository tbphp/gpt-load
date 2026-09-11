package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
)

func TestUltrafastRequestPricingAcrossTransports(t *testing.T) {
	for _, transport := range []string{"http", "sse", "websocket"} {
		for _, scenario := range []string{"mode", "override", "missing schedule", "partial schedule", "context tier"} {
			t.Run(transport+"/"+scenario, func(t *testing.T) {
				const final = `{"id":"resp_ultrafast","object":"response","status":"completed","model":"upstream","service_tier":"default","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`
				event := []byte(`{"type":"response.completed","response":` + final + `}`)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if websocket.IsWebSocketUpgrade(r) {
						conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
						if err != nil {
							t.Error(err)
							return
						}
						defer conn.Close()
						if err := conn.ReadJSON(&body); err != nil {
							t.Error(err)
							return
						}
						if body["service_tier"] != "ultrafast" {
							t.Errorf("upstream tier=%v", body["service_tier"])
						}
						if err := conn.WriteMessage(websocket.TextMessage, event); err != nil {
							t.Error(err)
							return
						}
						_, _, _ = conn.ReadMessage()
						return
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if body["service_tier"] != "ultrafast" {
						t.Errorf("upstream tier=%v", body["service_tier"])
					}
					w.Header().Set("Content-Type", "application/json")
					payload := []byte(final)
					if body["stream"] == true {
						w.Header().Set("Content-Type", "text/event-stream")
						payload = append([]byte("data: "), event...)
						payload = append(payload, '\n', '\n')
					}
					if _, err := w.Write(payload); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(upstream.Close)
				handler, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.OpenAI)
				requestTier := "ultrafast"
				if scenario == "override" {
					requestTier = "default"
					input.Groups[0].Settings = config.Settings{"parameter_overrides": []any{map[string]any{"set": map[string]any{"service_tier": "ultrafast"}}}}
					if _, err := handler.manager.Publish(input); err != nil {
						t.Fatal(err)
					}
				}
				prices := func(value int64) pricing.Prices {
					return pricing.Prices{Input: pricing.Price{Set: true, NanoUSDPerMillion: pricing.NanoUSD(value)}, Output: pricing.Price{Set: true, NanoUSDPerMillion: pricing.NanoUSD(value)}}
				}
				rule := pricing.Rule{
					Identity: pricing.Identity{ChannelID: "openai", ModelID: "upstream"},
					Prices:   prices(2_000_000_000),
					ModeSchedules: map[pricing.Mode]pricing.Schedule{
						pricing.ModeUltrafast: {Prices: prices(9_000_000_000)},
					},
				}
				wantCost := int64(27_000)
				wantMode := pricing.ModeUltrafast
				wantCompleteness := string(pricing.CompletenessComplete)
				if scenario == "missing schedule" {
					rule.ModeSchedules = nil
					wantCost = 6_000
					wantMode = pricing.ModeStandard
				}
				if scenario == "partial schedule" {
					schedule := rule.ModeSchedules[pricing.ModeUltrafast]
					schedule.Prices.Output = pricing.Price{}
					rule.ModeSchedules[pricing.ModeUltrafast] = schedule
					wantCost = 18_000
					wantCompleteness = string(pricing.CompletenessPartial)
				}
				if scenario == "context tier" {
					rule.ContextTiers = []pricing.ContextTier{{InputThresholdTokens: 2, Prices: prices(4_000_000_000)}}
					wantCost = 12_000
					wantMode = pricing.ModeStandard
				}
				table, err := pricing.NewTable([]pricing.Rule{rule})
				if err != nil {
					t.Fatal(err)
				}
				handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
				sink := &recordingRequestLogSink{}
				handler.requestLogSink = sink
				payload := []byte(fmt.Sprintf(`{"model":"public","input":"hello","store":false,"stream":%t,"service_tier":%q}`, transport != "http", requestTier))
				if transport == "websocket" {
					server := httptest.NewServer(engine)
					t.Cleanup(server.Close)
					conn := dialGatewayWebsocket(t, server.URL)
					var body map[string]any
					if err := json.Unmarshal(payload, &body); err != nil {
						t.Fatal(err)
					}
					body["type"] = "response.create"
					if err := conn.WriteJSON(body); err != nil {
						t.Fatal(err)
					}
					_, reply, err := conn.ReadMessage()
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(reply, []byte(`"service_tier":"default"`)) {
						t.Fatalf("upstream response changed: %s", reply)
					}
				} else {
					request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
					request.Header.Set("Authorization", "Bearer gl-client")
					request.Header.Set("Content-Type", "application/json")
					recorder := httptest.NewRecorder()
					engine.ServeHTTP(recorder, request)
					reply, err := io.ReadAll(recorder.Result().Body)
					if err != nil {
						t.Fatal(err)
					}
					if recorder.Code != 200 || !bytes.Contains(reply, []byte(`"service_tier":"default"`)) {
						t.Fatalf("response=%d %s", recorder.Code, reply)
					}
				}
				logs := waitWebsocketLogs(t, sink, 1)
				var receipt pricing.Receipt
				if err := json.Unmarshal([]byte(logs[0].Usage.Pricing.ReceiptJSON), &receipt); err != nil {
					t.Fatal(err)
				}
				if logs[0].Usage.Pricing.EstimatedCostNanoUSD != wantCost || receipt.PricingMode != wantMode {
					t.Fatalf("pricing=%+v receipt=%+v", logs[0].Usage.Pricing, receipt)
				}
				if logs[0].Usage.Pricing.PricingCompleteness != wantCompleteness {
					t.Fatalf("completeness=%s want %s", logs[0].Usage.Pricing.PricingCompleteness, wantCompleteness)
				}
				if (receipt.ContextThresholdTokens != nil) != (scenario == "context tier") {
					t.Fatalf("unexpected context tier: %+v", receipt)
				}
			})
		}
	}
}
