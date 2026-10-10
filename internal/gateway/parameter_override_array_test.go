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

func TestArrayParameterOverridesReachUpstreamAndStayGroupScoped(t *testing.T) {
	for _, channelID := range []channel.ID{channel.OpenAICompatible, channel.DeepSeek} {
		for _, stream := range []bool{false, true} {
			for _, test := range []struct {
				name    string
				match   map[string]any
				set     map[string]any
				applied bool
			}{
				{"remove only", map[string]any{"model": "DeepSeek-V4*"}, nil, true},
				{"remove and set", map[string]any{"model": "DeepSeek-V4*", "protocol": "openai-completions"}, map[string]any{"messages": map[string]any{"*": map[string]any{"custom_tag": "gateway"}}}, true},
				{"set after remove wins", map[string]any{"model": "DeepSeek-V4*"}, map[string]any{"messages": map[string]any{"*": map[string]any{"reasoning_content": "configured"}}}, true},
				{"model mismatch", map[string]any{"model": "other*"}, nil, false},
				{"protocol mismatch", map[string]any{"protocol": "anthropic"}, nil, false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", channelID, stream, test.name), func(t *testing.T) {
					first := fakeupstream.New(fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "401.json"})
					defer first.Close()
					fixture := "success.json"
					if stream {
						fixture = "stream.sse"
					}
					second := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: fixture, Stream: stream})
					defer second.Close()
					group := func(id uint, name, baseURL string) dialectGatewayGroup {
						_, params := testChannelConfig(t, protocol.OpenAICompletions, baseURL+"/v1")
						return dialectGatewayGroup{
							id: id, name: name, channelID: channelID, params: params, apiKeys: []string{"synthetic-" + name},
							models: []state.ModelConfig{{ID: "upstream-model", Alias: "DeepSeek-V4-test"}},
						}
					}
					rule := map[string]any{"match": test.match, "remove": []any{"/messages/*/reasoning_content"}}
					if test.set != nil {
						rule["set"] = test.set
					}
					firstGroup := group(1, "first", first.URL)
					firstGroup.settings = config.Settings{state.SettingParameterOverrides: []any{rule}}
					engine, _ := newDialectGatewayEngine(t, protocol.OpenAICompletions, "DeepSeek-V4-test",
						dialect.NewSet(dialect.NewOpenAI()), firstGroup, group(2, "second", second.URL))
					const history = `[{"role":"user","content":"question"},{"role":"assistant","content":"answer","reasoning":"alias thought","reasoning_content":"original thought","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call","content":"result"},{"role":"assistant","content":"summary","reasoning":"only alias"}]`
					body := fmt.Sprintf(`{"model":"DeepSeek-V4-test","stream":%t,"messages":%s}`, stream, history)
					request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer gl-client")
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("response=%d %s", response.Code, response.Body.String())
					}
					for index, upstream := range []*fakeupstream.Server{first, second} {
						requests := upstream.Requests()
						if len(requests) != 1 {
							t.Fatalf("group %d requests=%d, want 1", index+1, len(requests))
						}
						var got map[string]any
						if err := json.Unmarshal(requests[0].Body, &got); err != nil {
							t.Fatal(err)
						}
						var want []any
						if err := json.Unmarshal([]byte(history), &want); err != nil {
							t.Fatal(err)
						}
						if index == 0 && test.applied {
							for _, item := range want {
								message := item.(map[string]any)
								delete(message, "reasoning_content")
								if test.set != nil {
									for name, value := range test.set["messages"].(map[string]any)["*"].(map[string]any) {
										message[name] = value
									}
								}
							}
						} else if channelID == channel.DeepSeek {
							want[3].(map[string]any)["reasoning_content"] = "only alias"
						}
						if got["model"] != "upstream-model" || !reflect.DeepEqual(got["messages"], want) {
							t.Errorf("group %d upstream request=%s, want messages=%#v", index+1, requests[0].Body, want)
						}
					}
				})
			}
		}
	}
}
