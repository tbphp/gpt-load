package bifrost

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/execution"
)

func TestCompatiblePresetsReuseNativeChatExecution(t *testing.T) {
	for _, id := range []string{"cerebras", "mistral", "nebius", "parasail", "wafer", "huggingface"} {
		t.Run(id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected target %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer "+testAPIKey || r.Header.Get("X-Api-Key") != "" {
					t.Error("selected credential did not replace client authentication")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Error(err)
					return
				}
				if string(payload["model"]) != `"upstream-model"` ||
					!bytes.Contains(payload["vendor_extension"], []byte(`1.2300`)) || payload["api_key"] != nil {
					t.Errorf("unexpected upstream body %s", body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chat_1","object":"chat.completion","created":1,"model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer server.Close()

			runtime := newTestRuntime(t)
			spec := compatibleSpec(server.URL)
			spec.ChannelID = id
			result := runtime.Execute(t.Context(), execution.NewAttemptSpec(spec))
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.Usage == nil {
				t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
			}
		})
	}
}
