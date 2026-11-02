package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protowire"

	"gpt-load/internal/telemetry"
)

func devinReviewRequest(t *testing.T, format string, extra map[string]any, streaming bool) (string, string) {
	t.Helper()
	path := "/v1/responses"
	body := map[string]any{"model": "swe-2", "stream": streaming, "input": "hello"}
	switch format {
	case "openai":
		path = "/v1/chat/completions"
		body = map[string]any{"model": "swe-2", "stream": streaming, "messages": []any{map[string]string{"role": "user", "content": "hello"}}}
	case "claude":
		path = "/v1/messages"
		body = map[string]any{"model": "swe-2", "stream": streaming, "max_tokens": 64, "messages": []any{map[string]string{"role": "user", "content": "hello"}}}
	case "gemini":
		path = "/v1beta/models/swe-2:streamGenerateContent"
		if !streaming {
			path = "/v1beta/models/swe-2:generateContent"
		}
		body = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "hello"}}}}}
	}
	for key, value := range extra {
		body[key] = value
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return path, string(raw)
}

func devinReviewGateway(t *testing.T, format, content, trailer string, extra map[string]any, streaming bool) (string, telemetry.RequestStatus, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previous := logrus.StandardLogger().Out
	var logOutput bytes.Buffer
	logrus.SetOutput(&logOutput)
	defer logrus.SetOutput(previous)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/connect+proto")
		frame := protowire.AppendBytes(protowire.AppendTag(nil, 3, protowire.BytesType), []byte(content))
		frame = protowire.AppendVarint(protowire.AppendTag(frame, 5, protowire.VarintType), 2)
		_, _ = w.Write(devinGatewayFrame(0, frame))
		_, _ = w.Write(devinGatewayFrame(2, []byte(trailer)))
	}))
	defer server.Close()
	engine, sink := newDevinTerminalGateway(t, server.URL)
	path, body := devinReviewRequest(t, format, extra, streaming)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", server.Client().Transport))
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if len(sink.events) != 1 {
		t.Fatalf("invalid fixture: status=%d logs=%d", response.Code, len(sink.events))
	}
	if strings.Contains(logOutput.String(), "devin-session-token$access") {
		t.Error("SDK log exposed fixture credential")
	}
	return response.Body.String(), sink.events[0].Status, int(calls.Load())
}

func TestDevinGatewayRedactsCredentialBearingTrailerErrors(t *testing.T) {
	for _, format := range []string{"openai-response", "openai", "claude", "gemini"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", format, streaming), func(t *testing.T) {
				// 仅使用已有测试夹具里的虚构令牌，同时检查 SDK 日志与客户端输出。
				payload, status, calls := devinReviewGateway(t, format, "partial", `{"error":{"code":"internal","message":"echo devin-session-token$access"}}`, nil, streaming)
				if calls != 1 || status != telemetry.RequestStatusError {
					t.Fatalf("upstream failure was not preserved: calls=%d status=%s", calls, status)
				}
				if strings.Contains(payload, "devin-session-token$access") {
					t.Fatal("translated provider error exposed fixture credential")
				}
			})
		}
	}
}

func TestDevinGatewayRejectsMalformedTrailers(t *testing.T) {
	for index, trailer := range []string{`{"error":`, `{"error":17}`} {
		for _, format := range []string{"openai-response", "openai", "claude", "gemini"} {
			for _, streaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("case%d/%s/stream=%v", index, format, streaming), func(t *testing.T) {
					payload, status, calls := devinReviewGateway(t, format, "partial", trailer, nil, streaming)
					completed := strings.Contains(payload, "response.completed") || strings.Contains(payload, "[DONE]") || strings.Contains(payload, `"finishReason":"STOP"`) || strings.Contains(payload, `"type":"message_stop"`)
					if calls != 1 || status != telemetry.RequestStatusError || completed {
						t.Fatalf("malformed trailer result: calls=%d status=%s success_marker=%t", calls, status, completed)
					}
				})
			}
		}
	}
}

func TestDevinGatewayRejectsUnscopedSelectorsAndUnsupportedControls(t *testing.T) {
	cases := []struct {
		name, format string
		extra        map[string]any
	}{
		{"previous_response_id", "openai", map[string]any{"previous_response_id": "11111111-1111-1111-1111-111111111111"}},
		{"previous_interaction_id", "openai", map[string]any{"previous_interaction_id": "11111111-1111-1111-1111-111111111111"}},
		{"custom", "openai", map[string]any{"tools": []any{map[string]any{"type": "custom", "name": "lookup", "format": map[string]string{"type": "text"}}}}},
		{"namespace", "openai", map[string]any{"tools": []any{map[string]any{"type": "namespace", "name": "functions", "tools": []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]string{"type": "object"}}}}}}},
		{"stop_chat", "openai", map[string]any{"stop": []string{"STOP"}}},
		{"stop_claude", "claude", map[string]any{"stop_sequences": []string{"STOP"}}},
		{"stop_gemini", "gemini", map[string]any{"generationConfig": map[string]any{"stopSequences": []string{"STOP"}}}},
		{"stop_gemini_snake", "gemini", map[string]any{"generation_config": map[string]any{"stop_sequences": []string{"STOP"}}}},
		{"mime_snake", "gemini", map[string]any{"generation_config": map[string]string{"response_mime_type": "application/json"}}},
		{"forced_snake", "gemini", map[string]any{"tool_config": map[string]any{"function_calling_config": map[string]string{"mode": "ANY"}}}},
	}
	for _, test := range cases {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", test.name, streaming), func(t *testing.T) {
				_, status, calls := devinReviewGateway(t, test.format, "should not reach upstream", `{}`, test.extra, streaming)
				if calls != 0 || status != telemetry.RequestStatusError {
					t.Fatalf("unsupported input was dispatched: calls=%d status=%s", calls, status)
				}
			})
		}
	}
}
