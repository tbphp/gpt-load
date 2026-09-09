package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestGatewayImagesConversionFailureUsesExistingFallback(t *testing.T) {
	for _, stream := range []bool{false, true} {
		count := 2
		if stream {
			count = 1
		}
		for _, native := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/native=%t", stream, native), func(t *testing.T) {
				var convertedCalls, nativeCalls atomic.Int32
				convertedUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					convertedCalls.Add(1)
					writer.WriteHeader(http.StatusInternalServerError)
				}))
				defer convertedUpstream.Close()
				nativeUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					nativeCalls.Add(1)
					var received struct {
						Count  int  `json:"n"`
						Stream bool `json:"stream"`
					}
					if err := json.NewDecoder(request.Body).Decode(&received); err != nil || received.Count != count || received.Stream != stream {
						t.Errorf("native parameters = %+v, decode error = %v", received, err)
					}
					body := `{"data":[{"b64_json":"aW1hZ2U="},{"b64_json":"aW1hZ2U="}]}`
					writer.Header().Set("Content-Type", "application/json")
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						body = "data: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\"}\n\n"
					}
					if _, err := writer.Write([]byte(body)); err != nil {
						t.Error(err)
					}
				}))
				defer nativeUpstream.Close()
				params, err := json.Marshal(map[string]string{"base_url": convertedUpstream.URL + "/v1beta"})
				if err != nil {
					t.Fatal(err)
				}
				groups := []dialectGatewayGroup{{
					id: 1, name: "gemini", channelID: channel.Gemini, params: params, apiKeys: []string{"gemini-key-one", "gemini-key-two"},
				}}
				if native {
					nativeParams, err := json.Marshal(map[string]string{"base_url": nativeUpstream.URL + "/v1"})
					if err != nil {
						t.Fatal(err)
					}
					groups = append(groups, dialectGatewayGroup{id: 2, name: "native", channelID: channel.OpenAI, params: nativeParams, apiKeys: []string{"native-key"}})
				}
				engine, registry := newDialectGatewayEngineWithSystemSettings(t, protocol.OpenAIImages, "public-image",
					dialect.NewSet(dialect.NewOpenAIImages()), newTestExecutionForwarder(t),
					config.Settings{state.SettingRouteStrategy: string(state.RouteStrategyWeightedMix)}, groups...)
				before := registry.Snapshot()
				body := fmt.Sprintf(`{"model":"public-image","prompt":"draw","n":%d,"stream":%t}`, count, stream)
				request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				wantStatus, wantAttempts := http.StatusUnprocessableEntity, "1"
				if native {
					wantStatus, wantAttempts = http.StatusOK, "2"
				}
				if response.Code != wantStatus || response.Header().Get(debugHeaderAttempts) != wantAttempts || convertedCalls.Load() != 0 {
					t.Fatalf("response = %d %s, attempts = %s, conversion dispatches = %d", response.Code, response.Body.String(), response.Header().Get(debugHeaderAttempts), convertedCalls.Load())
				}
				if native {
					if nativeCalls.Load() != 1 {
						t.Fatalf("native dispatches = %d", nativeCalls.Load())
					}
					if !strings.Contains(response.Body.String(), `"b64_json":"aW1hZ2U="`) {
						t.Fatalf("native result was lost: %s", response.Body.String())
					}
				} else if !strings.Contains(response.Body.String(), `"code":"protocol_conversion_unsupported"`) || !reflect.DeepEqual(before, registry.Snapshot()) {
					t.Fatalf("conversion failure changed response or credential health: %s", response.Body.String())
				}
			})
		}
	}
}
