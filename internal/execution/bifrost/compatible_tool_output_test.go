package bifrost

import (
	"context"
	"encoding/json"
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
func toolOutputRequestBody(t *testing.T, output any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "client-model",
		"input": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "inspect", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": output},
		},
		"max_output_tokens": 32,
		"stream":            false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func runCompatibleResponses(t *testing.T, body []byte) []byte {
	t.Helper()
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		captured, _ = io.ReadAll(request.Body)
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
	result := runtime.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil {
		t.Fatalf("result = %+v err=%v body=%s", result, err, result.Body)
	}
	return captured
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

	upstream := runCompatibleResponses(t, toolOutputRequestBody(t, []any{
		map[string]any{"type": "input_text", "text": "looked at the screenshot"},
		map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,AA=="},
	}))

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
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upstream := runCompatibleResponses(t, toolOutputRequestBody(t, test.blocks))
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

// A plain string tool result must keep working unchanged.
func TestOpenAICompatibleStringToolOutputIsUntouched(t *testing.T) {
	t.Parallel()

	upstream := runCompatibleResponses(t, toolOutputRequestBody(t, "plain result"))
	content := toolMessageContent(t, upstream)
	if text, ok := content.(string); !ok || text != "plain result" {
		t.Fatalf("tool content = %#v, want %q; body=%s", content, "plain result", upstream)
	}
}

// Only tool results are folded; a user turn that mixes text and images keeps its
// parts, because multimodal upstreams rely on them.
func TestOpenAICompatibleUserImageContentIsNotFlattened(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(map[string]any{
		"model": "client-model",
		"input": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "what is this"},
				map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64,AA=="},
			}},
		},
		"max_output_tokens": 32,
		"stream":            false,
	})
	if err != nil {
		t.Fatal(err)
	}

	upstream := runCompatibleResponses(t, body)
	if !strings.Contains(string(upstream), "image_url") {
		t.Fatalf("user image part was dropped: %s", upstream)
	}
}
