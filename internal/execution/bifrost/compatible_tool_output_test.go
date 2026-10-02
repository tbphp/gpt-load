package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

// toolOutputRequestBody builds a Responses request whose only interesting part
// is the tool result at input[2].
func toolOutputRequestBody(t *testing.T, output any, stream bool) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "client-model",
		"input": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "inspect", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": output},
		},
		"max_output_tokens": 32,
		"stream":            stream,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func runCompatibleResponses(t *testing.T, body []byte, stream bool) []byte {
	t.Helper()
	captured := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		wire, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		captured <- wire
		var payload struct {
			Stream bool `json:"stream"`
		}
		if err := json.Unmarshal(wire, &payload); err != nil || payload.Stream != stream {
			t.Errorf("unexpected stream flag: body=%s err=%v", wire, err)
		}
		if stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, openAIChatFinalStream)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()

	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	target, _ := json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
	spec := openAIResponsesSpec(execution.OperationResponsesCreate, http.MethodPost, "/v1/responses")
	spec.ChannelID = string(channel.OpenAICompatible)
	spec.TargetConfig = target
	spec = freezeTestAttempt(spec)
	spec.Body = body
	if stream {
		var data strings.Builder
		result := runtime.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
			data.Write(event.Data)
			return nil
		})
		if err := result.Validate(); err != nil || result.Error != nil || !strings.Contains(data.String(), "response.completed") {
			t.Fatalf("result = %+v err=%v data=%s", result, err, data.String())
		}
	} else {
		result := runtime.Execute(context.Background(), spec)
		if err := result.Validate(); err != nil || result.Error != nil {
			t.Fatalf("result = %+v err=%v body=%s", result, err, result.Body)
		}
	}
	return <-captured
}

func toolMessageContent(t *testing.T, upstream []byte) any {
	t.Helper()
	var decoded struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(upstream, &decoded); err != nil {
		t.Fatalf("decode upstream body: %v (%s)", err, upstream)
	}
	for _, message := range decoded.Messages {
		if message.Role == "tool" {
			var content any
			if err := json.Unmarshal(message.Content, &content); err != nil {
				t.Fatalf("decode tool content: %v (%s)", err, message.Content)
			}
			return content
		}
	}
	t.Fatalf("no tool message in upstream body: %s", upstream)
	return nil
}

// A tool result that carries an image used to be forwarded as a content array,
// which strict Chat Completions upstreams reject with
// "Input should be a valid string". See tbphp/gpt-load#743.
func TestOpenAICompatibleToolOutputImageIsFlattenedToString(t *testing.T) {
	t.Parallel()

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			t.Parallel()
			upstream := runCompatibleResponses(t, toolOutputRequestBody(t, []any{
				map[string]any{"type": "input_text", "text": "looked at the screenshot"},
				map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,AA=="},
			}, stream), stream)

			content := toolMessageContent(t, upstream)
			text, ok := content.(string)
			if !ok {
				t.Fatalf("tool content = %T (%v), want string; body=%s", content, content, upstream)
			}
			want := "looked at the screenshot\n\n[image omitted: unsupported by upstream]"
			if text != want {
				t.Fatalf("tool content = %q, want %q", text, want)
			}
			if strings.Contains(string(upstream), "image_url") {
				t.Fatalf("text-only upstream still received an image part: %s", upstream)
			}
		})
	}
}

func TestOpenAICompatibleToolOutputFlatteningCoversEveryPartType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		blocks []any
		want   string
	}{
		{
			name: "text only",
			blocks: []any{
				map[string]any{"type": "input_text", "text": "first"},
				map[string]any{"type": "input_text", "text": "second"},
			},
			want: "first\n\nsecond",
		},
		{
			name:   "image only",
			blocks: []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AA=="}},
			want:   "[image omitted: unsupported by upstream]",
		},
		{
			name:   "file",
			blocks: []any{map[string]any{"type": "input_file", "file_id": "file_1"}},
			want:   "[file omitted: unsupported by upstream]",
		},
		{
			name:   "audio",
			blocks: []any{map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "AA==", "format": "wav"}}},
			want:   "[audio omitted: unsupported by upstream]",
		},
		{
			name:   "unknown part",
			blocks: []any{map[string]any{"type": "some_vendor_part", "text": "ignored"}},
			want:   "[content omitted: unsupported by upstream]",
		},
	}
	for _, test := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				t.Parallel()
				upstream := runCompatibleResponses(t, toolOutputRequestBody(t, test.blocks, stream), stream)
				content := toolMessageContent(t, upstream)
				text, ok := content.(string)
				if !ok {
					t.Fatalf("tool content = %T (%v), want string; body=%s", content, content, upstream)
				}
				if text != test.want {
					t.Fatalf("tool content = %q, want %q", text, test.want)
				}
			})
		}
	}
}

// A plain string tool result must keep working unchanged.
func TestOpenAICompatibleStringToolOutputIsUntouched(t *testing.T) {
	t.Parallel()

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			t.Parallel()
			upstream := runCompatibleResponses(t, toolOutputRequestBody(t, "plain result", stream), stream)
			content := toolMessageContent(t, upstream)
			if text, ok := content.(string); !ok || text != "plain result" {
				t.Fatalf("tool content = %#v, want %q; body=%s", content, "plain result", upstream)
			}
		})
	}
}

// Only tool results are folded; a user turn that mixes text and images keeps its
// parts, because multimodal upstreams rely on them.
func TestOpenAICompatibleUserImageContentIsNotFlattened(t *testing.T) {
	t.Parallel()

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(map[string]any{
				"model": "client-model",
				"input": []any{
					map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "input_text", "text": "what is this"},
						map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,AA=="},
					}},
					map[string]any{"type": "function_call", "call_id": "call_1", "name": "inspect", "arguments": "{}"},
					map[string]any{"type": "function_call_output", "call_id": "call_1", "output": []any{
						map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AQ=="},
					}},
				},
				"max_output_tokens": 32,
				"stream":            stream,
			})
			if err != nil {
				t.Fatal(err)
			}

			upstream := runCompatibleResponses(t, body, stream)
			if !strings.Contains(string(upstream), "image_url") {
				t.Fatalf("user image part was dropped: %s", upstream)
			}
			if content := toolMessageContent(t, upstream); content != "[image omitted: unsupported by upstream]" {
				t.Fatalf("tool image was not flattened: %s", upstream)
			}
		})
	}
}
