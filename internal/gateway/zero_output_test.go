package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

func TestGatewayZeroOutputProtectionUsesGroupEffectiveParameters(t *testing.T) {
	for _, test := range []struct {
		name       string
		original   int
		override   any
		wantReject bool
	}{
		{name: "zero limit is protected", original: 0, wantReject: true},
		{name: "group requests zero output", original: 16, override: 0, wantReject: true},
		{name: "explicit group override permits generation", original: 0, override: 16},
		{name: "ordinary generation", original: 16},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				fixture := "gemini/success.json"
				if stream {
					fixture = "gemini/stream.sse"
				}
				upstream := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: fixture})
				defer upstream.Close()
				params, err := json.Marshal(map[string]string{"base_url": upstream.URL + "/v1beta"})
				if err != nil {
					t.Fatal(err)
				}
				var settings config.Settings
				if test.override != nil {
					settings = config.Settings{state.SettingParameterOverrides: []any{
						map[string]any{"set": map[string]any{"max_tokens": test.override}},
					}}
				}
				engine, registry := newDialectGatewayEngine(t, protocol.Anthropic, "public",
					dialect.NewSet(dialect.NewAnthropic()), dialectGatewayGroup{
						id: 1, name: "gemini", channelID: channel.Gemini, params: params,
						apiKeys: []string{"upstream-key"}, settings: settings,
					})
				before := registry.Snapshot()
				body := fmt.Sprintf(`{"model":"public","max_tokens":%d,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, test.original, stream)
				request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer gl-client")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if test.wantReject {
					if response.Code != http.StatusUnprocessableEntity ||
						!strings.Contains(response.Body.String(), `"code":"protocol_conversion_unsupported"`) ||
						len(upstream.Requests()) != 0 || strings.Contains(response.Body.String(), "event: message_start") {
						t.Fatalf("response = %d %s, upstream calls = %d", response.Code, response.Body.String(), len(upstream.Requests()))
					}
					if !reflect.DeepEqual(before, registry.Snapshot()) {
						t.Fatal("local conversion rejection changed credential health")
					}
					return
				}
				if response.Code != http.StatusOK || len(upstream.Requests()) != 1 {
					t.Fatalf("generation response = %d %s, upstream calls = %d", response.Code, response.Body.String(), len(upstream.Requests()))
				}
				var received struct {
					GenerationConfig struct {
						MaxOutputTokens int `json:"maxOutputTokens"`
					} `json:"generationConfig"`
				}
				if err := json.Unmarshal(upstream.Requests()[0].Body, &received); err != nil || received.GenerationConfig.MaxOutputTokens != 16 {
					t.Fatalf("effective output limit = %d, decode error = %v", received.GenerationConfig.MaxOutputTokens, err)
				}
			})
		}
	}
}
