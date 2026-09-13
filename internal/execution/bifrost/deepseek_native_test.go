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
	for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", clientProtocol, stream), func(t *testing.T) {
				body, response, events := nativeFidelityFixture(clientProtocol)
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
