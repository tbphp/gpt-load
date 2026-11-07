package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCodexGatewayReasoningStatusOutbound(t *testing.T) {
	// 与已有 bootstrap 用例相同的 Fernet 外层格式合成密文；不证明真实 Codex 可解密。
	cipher := "gAAAA" + strings.Repeat("A", 93)
	var mu sync.Mutex
	var bodies [][]byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		// 这里只记录出站正文，响应成功由测试桩提供，不能当作真实 Codex 接受的证据。
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"id":"resp_status","object":"response","status":"completed","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`+"\n\n")
	}))
	defer upstream.Close()
	engine, _ := newCodexBootstrapGateway(t, upstream.URL, nil)
	body := fmt.Sprintf(`{"model":"gpt-5","store":false,"stream":true,"input":[{"type":"reasoning","id":%q,"status":"completed","encrypted_content":%q,"summary":[]},{"type":"reasoning","id":"item_9d700c62-046b-426a-8431-27497dcd0a0a_0","status":"completed","encrypted_content":"","summary":[{"type":"summary_text","text":"Devin summary"}]},{"type":"reasoning","id":"rs_native","status":"completed","encrypted_content":%q,"summary":[]},{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"history"}]},{"type":"function_call","call_id":"call_keep","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_keep","output":"result"}]}`, "rs_"+strings.Repeat("x", 65), cipher, cipher)
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", upstream.Client().Transport))
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("test upstream exchange failed: %d %s", response.Code, response.Body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want two turns without status-error retries", len(bodies))
	}
	for _, sent := range bodies {
		var request struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(sent, &request); err != nil {
			t.Fatal(err)
		}
		items := request.Input
		if len(items) != 5 {
			t.Fatalf("CPA should drop only the overlong encrypted reasoning: %s", sent)
		}
		// CPA v8.0.51 在 store=false 时另行移除空密文和因此失去密文的 reasoning id。
		// 嵌入层在 CPA 之前只删 reasoning.status；其余字段由本断言核对出站结果。
		wantSummary := map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "Devin summary"}}}
		if !reflect.DeepEqual(items[0], wantSummary) {
			t.Fatalf("short-id reasoning was dropped or still has status: %#v", items[0])
		}
		if items[1]["id"] != "rs_native" || items[1]["encrypted_content"] != cipher {
			t.Fatalf("native opaque encrypted content changed: %#v", items[1])
		}
		if _, exists := items[1]["status"]; exists {
			t.Fatalf("native reasoning status still present: %#v", items[1])
		}
		if items[2]["type"] != "message" || items[2]["status"] != "completed" {
			t.Fatalf("message.status changed: %#v", items[2])
		}
		if items[3]["type"] != "function_call" || items[3]["call_id"] != "call_keep" || items[3]["name"] != "lookup" || items[3]["arguments"] != "{}" ||
			items[4]["type"] != "function_call_output" || items[4]["call_id"] != "call_keep" || items[4]["output"] != "result" {
			t.Fatalf("tool pair changed: %#v", items[3:])
		}
	}
}
