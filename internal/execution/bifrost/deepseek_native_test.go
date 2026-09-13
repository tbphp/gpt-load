package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/sjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestDeepSeekNativeCompatibilityPreservesHistory(t *testing.T) {
	for _, test := range []struct {
		protocol         protocol.Protocol
		implicitThinking bool
	}{
		{protocol.OpenAICompletions, false}, {protocol.OpenAIResponses, false}, {protocol.Anthropic, false},
		{protocol.OpenAICompletions, true}, {protocol.OpenAIResponses, true}, {protocol.Anthropic, true},
	} {
		clientProtocol, implicitThinking := test.protocol, test.implicitThinking
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t/implicitThinking=%t", clientProtocol, stream, implicitThinking), func(t *testing.T) {
				body, response, events := nativeFidelityFixture(clientProtocol)
				if implicitThinking {
					for _, field := range []string{"thinking", "reasoning_effort", "reasoning", "output_config"} {
						var err error
						body, err = sjson.Delete(body, field)
						if err != nil {
							t.Fatal(err)
						}
					}
					choice := `"required"`
					if clientProtocol == protocol.Anthropic {
						choice = `{"type":"any"}`
					}
					var err error
					body, err = sjson.SetRaw(body, "tool_choice", choice)
					if err != nil {
						t.Fatal(err)
					}
				}
				field := "messages"
				if clientProtocol == protocol.OpenAIResponses {
					field = "input"
					const reasoningItem = `{"type":"reasoning","id":"reasoning_history","content":[{"type":"reasoning_text","text":"full reasoning history"}],"summary":[]}`
					body = strings.Replace(body, `{"type":"function_call"`, reasoningItem+`,{"type":"function_call"`, 1)
					response = strings.Replace(response, `"output":[`, `"output":[`+reasoningItem+`,`, 1)
					events = "event: response.reasoning_text.delta\ndata: {\"type\":\"response.reasoning_text.delta\",\"item_id\":\"reasoning_history\",\"output_index\":0,\"content_index\":0,\"delta\":\"full reasoning history\"}\n\n" + events
					events = strings.Replace(events, `"output":[]`, `"output":[`+reasoningItem+`]`, 1)
				}
				if clientProtocol != protocol.Anthropic {
					body = strings.ReplaceAll(body, `"role":"system"`, `"role":"developer"`)
				}
				if clientProtocol == protocol.OpenAICompletions {
					body = strings.Replace(body, `"reasoning_content":"reasoning history"`, `"reasoning":"reasoning history"`, 1)
				}
				body = strings.TrimSuffix(body, "}") + `,"prompt_cache_key":"stable-cache-key","metadata":{"user_id":"stable-user"}}`
				original := []byte(body)
				want := nativeFidelityObject(t, original)
				want["model"] = "upstream-model"
				delete(want, "provider")
				if implicitThinking {
					if clientProtocol == protocol.OpenAIResponses {
						want["reasoning"] = map[string]any{"effort": "none"}
					} else {
						want["thinking"] = map[string]any{"type": "disabled"}
					}
				}
				if stream {
					want["stream"] = true
				}
				history := want[field].([]any)
				switch clientProtocol {
				case protocol.OpenAICompletions:
					history[0].(map[string]any)["role"] = "system"
					history[4].(map[string]any)["role"] = "system"
					history[2].(map[string]any)["reasoning_content"] = "reasoning history"
				case protocol.OpenAIResponses:
					history[4].(map[string]any)["role"] = "system"
				}
				wireBodies := make(chan []byte, 2)
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					wire, err := io.ReadAll(request.Body)
					if err != nil {
						t.Error(err)
						return
					}
					wireBodies <- wire
					if request.Header.Get("X-Session-Id") != "stable-session" {
						t.Error("session header changed")
					}
					writer.Header().Set("Content-Type", "application/json")
					output := response
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						output = events
					}
					_, _ = io.WriteString(writer, output)
				}))
				defer server.Close()
				var spec execution.AttemptSpec
				switch clientProtocol {
				case protocol.OpenAICompletions:
					spec = openAIChatAttempt(t, channel.DeepSeek, server.URL, "native-key")
				case protocol.OpenAIResponses:
					spec = openAIResponsesAttempt(t, channel.DeepSeek, server.URL)
				case protocol.Anthropic:
					spec = deepSeekAnthropicAttempt(t, server.URL)
				}
				spec.ClientModel = spec.UpstreamModel
				spec.Header = http.Header{"X-Session-Id": {"stable-session"}}
				spec.ContinuityKey = "stable-continuity"
				spec.Body = bytes.Clone(original)
				runtime := newRuntimeForTest(t, testRuntimeOptions{allowPrivateNetwork: true})
				execute := func() map[string]any {
					t.Helper()
					before := spec.Clone()
					if stream {
						var received []execution.StreamEvent
						result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
							received = append(received, event.Clone())
							return nil
						})
						assertRawStream(t, result, received, events, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 3})
					} else {
						result := runtime.Execute(t.Context(), spec)
						if result.Error != nil || string(result.Body) != response {
							t.Fatalf("native response changed: error=%+v body=%s", result.Error, result.Body)
						}
					}
					if !reflect.DeepEqual(spec, before) {
						t.Fatal("execution changed the original attempt, affinity identifiers or credential")
					}
					select {
					case wire := <-wireBodies:
						return nativeFidelityObject(t, wire)
					default:
						t.Fatal("request was not dispatched")
						return nil
					}
				}
				first := execute()
				if !reflect.DeepEqual(first, want) {
					t.Fatalf("unexpected compatibility changes:\ngot: %#v\nwant: %#v", first, want)
				}
				var err error
				spec.Body, err = sjson.SetRawBytes(original, field+".-1", []byte(`{"role":"user","content":"continue"}`))
				if err != nil {
					t.Fatal(err)
				}
				second := execute()
				secondHistory := second[field].([]any)
				if len(secondHistory) != len(history)+1 || !reflect.DeepEqual(first[field], secondHistory[:len(history)]) {
					t.Fatal("appending a turn changed the existing message prefix or tool order")
				}
				second[field] = secondHistory[:len(history)]
				if !reflect.DeepEqual(first, second) {
					t.Fatal("appending a turn changed instructions, tools, thinking or affinity fields")
				}
			})
		}
	}
}

func TestDeepSeekForcedToolsRespectExplicitThinking(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		choice   string
		control  string
		added    string
	}{
		{"chat required", protocol.OpenAICompletions, `"required"`, ``, `,"thinking":{"type":"disabled"}`},
		{"chat named", protocol.OpenAICompletions, `{"type":"function","function":{"name":"lookup"}}`, ``, `,"thinking":{"type":"disabled"}`},
		{"chat auto", protocol.OpenAICompletions, `"auto"`, ``, ``},
		{"chat none", protocol.OpenAICompletions, `"none"`, ``, ``},
		{"chat enabled", protocol.OpenAICompletions, `"required"`, `,"thinking":{"type":"enabled"}`, ``},
		{"chat disabled", protocol.OpenAICompletions, `"required"`, `,"thinking":{"type":"disabled"}`, ``},
		{"chat effort", protocol.OpenAICompletions, `"required"`, `,"reasoning_effort":"high"`, ``},
		{"chat invalid thinking", protocol.OpenAICompletions, `"required"`, `,"thinking":null`, ``},
		{"responses required", protocol.OpenAIResponses, `"required"`, ``, `,"reasoning":{"effort":"none"}`},
		{"responses named", protocol.OpenAIResponses, `{"type":"function","name":"lookup"}`, ``, `,"reasoning":{"effort":"none"}`},
		{"responses effort", protocol.OpenAIResponses, `"required"`, `,"reasoning":{"effort":"high"}`, ``},
		{"responses disabled", protocol.OpenAIResponses, `"required"`, `,"reasoning":{"effort":"none"}`, ``},
		{"responses auto", protocol.OpenAIResponses, `"auto"`, ``, ``},
		{"responses invalid reasoning", protocol.OpenAIResponses, `"required"`, `,"reasoning":"high"`, ``},
		{"anthropic any", protocol.Anthropic, `{"type":"any"}`, ``, `,"thinking":{"type":"disabled"}`},
		{"anthropic named", protocol.Anthropic, `{"type":"tool","name":"lookup","disable_parallel_tool_use":true}`, ``, `,"thinking":{"type":"disabled"}`},
		{"anthropic auto", protocol.Anthropic, `{"type":"auto"}`, ``, ``},
		{"anthropic adaptive", protocol.Anthropic, `{"type":"any"}`, `,"thinking":{"type":"adaptive"}`, ``},
		{"anthropic effort", protocol.Anthropic, `{"type":"any"}`, `,"output_config":{"effort":"high"}`, ``},
		{"unsupported protocol", protocol.Gemini, `"required"`, ``, ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"tool_choice":` + test.choice + test.control + `}`
			got, err := normalizeDeepSeekNativeRequest([]byte(body), test.protocol)
			want := strings.TrimSuffix(body, "}") + test.added + "}"
			if err != nil || string(got) != want {
				t.Fatalf("tool/thinking compatibility: error=%v got=%s want=%s", err, got, want)
			}
			again, err := normalizeDeepSeekNativeRequest(got, test.protocol)
			if err != nil || !bytes.Equal(got, again) {
				t.Fatal("normalization changed an already prepared request")
			}
		})
	}
}

func TestDeepSeekDefaultThinkingPreservesOtherOptions(t *testing.T) {
	for _, test := range []struct {
		protocol protocol.Protocol
		body     string
		want     string
	}{
		{protocol.OpenAIResponses,
			`{"tool_choice":"required","reasoning":{"summary":"auto"}}`,
			`{"tool_choice":"required","reasoning":{"summary":"auto","effort":"none"}}`},
		{protocol.Anthropic,
			`{"tool_choice":{"type":"any"},"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`,
			`{"tool_choice":{"type":"any"},"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}},"thinking":{"type":"disabled"}}`},
	} {
		got, err := normalizeDeepSeekNativeRequest([]byte(test.body), test.protocol)
		if err != nil || string(got) != test.want {
			t.Fatalf("changed unrelated options: error=%v body=%s", err, got)
		}
	}
}

func TestDeepSeekCompatibilityLeavesUnsupportedContentAndCanonicalReasoning(t *testing.T) {
	for _, test := range []struct {
		protocol protocol.Protocol
		body     string
	}{
		{protocol.OpenAIResponses, `{"input":[{"role":"developer","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]}`},
		{protocol.OpenAIResponses, `{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"a summary"}],"encrypted_content":"opaque"}]}`},
		{protocol.OpenAICompletions, `{"messages":[{"role":"assistant","reasoning_content":"canonical","reasoning":"alias"}]}`},
		{protocol.OpenAICompletions, `{"messages":[{"role":"assistant","reasoning_content":"","reasoning":"alias"}]}`},
		{protocol.OpenAICompletions, `{"messages":[{"role":"assistant","reasoning_content":null,"reasoning":"alias"}]}`},
		{protocol.OpenAICompletions, `{"messages":[{"role":"assistant","reasoning":""}]}`},
		{protocol.OpenAICompletions, `{"messages":[{"role":"assistant","reasoning":{"summary":"not original text"}}]}`},
		{protocol.Anthropic, `{"system":"global","messages":[{"role":"user","content":"start"},{"role":"system","content":"instruction"}]}`},
	} {
		body := []byte(test.body)
		got, err := normalizeDeepSeekNativeRequest(body, test.protocol)
		if err != nil || !bytes.Equal(body, got) {
			t.Fatalf("unexpected rewrite for %s: error=%v body=%s", test.protocol, err, got)
		}
		if !json.Valid(got) {
			t.Fatal("invalid JSON returned")
		}
	}
}

func TestDeepSeekCompatibilityTextBlocks(t *testing.T) {
	for _, test := range []struct {
		protocol protocol.Protocol
		body     string
	}{
		{protocol.OpenAICompletions, `{"messages":[{"role":"developer","content":[{"type":"text","text":"instruction","cache_control":{"type":"ephemeral"}}]}]}`},
		{protocol.OpenAIResponses, `{"input":[{"type":"message","id":"msg_1","role":"developer","content":[{"type":"input_text","text":"instruction"}]}]}`},
	} {
		got, err := normalizeDeepSeekNativeRequest([]byte(test.body), test.protocol)
		want := strings.Replace(test.body, `"role":"developer"`, `"role":"system"`, 1)
		if err != nil || string(got) != want {
			t.Fatalf("text instruction or its metadata changed: error=%v body=%s", err, got)
		}
	}
}
