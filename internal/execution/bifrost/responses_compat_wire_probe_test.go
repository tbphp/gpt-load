package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAICompatibleResponsesConversionDropsReasoningEffort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "omitted output limit",
			body: `{"model":"deepseek-v4.1-flash","input":"hi"}`,
			want: map[string]any{
				"model": "upstream-model",
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
			},
		},
		{
			name: "explicit output limit",
			body: `{"model":"deepseek-v4.1-flash","input":"hi","max_output_tokens":4096}`,
			want: map[string]any{
				"model":                 "upstream-model",
				"max_completion_tokens": float64(4096),
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
			},
		},
		{
			name: "reasoning effort is not forwarded",
			body: `{"model":"deepseek-v4.1-flash","input":"hi","reasoning":{"effort":"medium"}}`,
			want: map[string]any{
				"model": "upstream-model",
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
			},
		},
		{
			name: "reasoning budget is not forwarded",
			body: `{"model":"deepseek-v4.1-flash","input":"hi","reasoning":{"effort":"medium","max_tokens":32768}}`,
			want: map[string]any{
				"model": "upstream-model",
				"messages": []any{
					map[string]any{"role": "user", "content": "hi"},
				},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var got []byte
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/chat/completions" {
					t.Errorf("path = %s", request.URL.Path)
				}
				got, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer server.Close()

			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			target, err := json.Marshal(map[string]string{"base_url": server.URL + "/v1"})
			if err != nil {
				t.Fatal(err)
			}
			spec := convertedSpec(channel.OpenAICompatible, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", []byte(test.body))
			spec.TargetConfig = target
			spec = freezeTestAttempt(spec)
			result := runtime.Execute(context.Background(), spec)
			if result.Error != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("result = %+v body=%s", result, got)
			}
			var payload map[string]any
			if err := json.Unmarshal(got, &payload); err != nil {
				t.Fatalf("decode upstream body: %v; body=%s", err, got)
			}
			if _, ok := payload["reasoning_effort"]; ok {
				t.Fatalf("forwarded reasoning_effort: %s", got)
			}
			if _, ok := payload["reasoning"]; ok {
				t.Fatalf("forwarded reasoning: %s", got)
			}
			wantRaw, _ := json.Marshal(test.want)
			gotComparable, _ := json.Marshal(payload)
			var wantPayload map[string]any
			_ = json.Unmarshal(wantRaw, &wantPayload)
			if !jsonEqual(wantPayload, payload) {
				t.Fatalf("upstream body = %s, want %s", gotComparable, wantRaw)
			}
		})
	}
}

func jsonEqual(left, right map[string]any) bool {
	leftRaw, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightRaw, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return string(leftRaw) == string(rightRaw)
}
