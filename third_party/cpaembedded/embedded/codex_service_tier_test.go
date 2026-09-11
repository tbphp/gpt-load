package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func TestCodexFastServiceTierHTTP(t *testing.T) {
	for _, tier := range []string{"priority", "fast"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tier, streaming), func(t *testing.T) {
				requests := 0
				transport := claudeRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					body, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					assertCodexFastRequest(t, body)
					response := append([]byte("data: "), wsCompleted("resp_fast")...)
					response = append(response, '\n', '\n')
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(bytes.NewReader(response)),
						Request:    request,
					}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				credential := CodexCredential{
					Type: ProviderCodex, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "test-account",
				}
				request := ExecuteRequest{
					Model: "gpt-5", Payload: codexFastRequestBody(t, tier), Format: "openai-response",
				}
				executor := NewCodexHTTPExecutor()
				if streaming {
					response, err := executor.ExecuteStreamCanonical(ctx, "fast-test", credential, request)
					if err != nil {
						t.Fatal(err)
					}
					completed := false
					for chunk := range response.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						completed = completed || bytes.Contains(chunk.Payload, []byte("response.completed"))
					}
					if !completed {
						t.Fatal("stream response lost completion event")
					}
				} else {
					response, err := executor.ExecuteCanonical(ctx, "fast-test", credential, request)
					if err != nil {
						t.Fatal(err)
					}
					if gjson.GetBytes(response.Payload, "id").String() != "resp_fast" {
						t.Fatal("unary response was not converted to Responses format")
					}
				}
				if requests != 1 {
					t.Fatalf("upstream requests = %d, want 1", requests)
				}
			})
		}
	}
}

func TestCodexFastServiceTierWebsocket(t *testing.T) {
	for _, tier := range []string{"priority", "fast"} {
		t.Run(tier, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer connection.Close()
				_, body, err := connection.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				assertCodexFastRequest(t, body)
				if err := connection.WriteMessage(websocket.TextMessage, wsCompleted("resp_fast_ws")); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			session := wsTestSession(t, server.URL)
			result, err := session.ExecuteTurn(t.Context(), codexFastRequestBody(t, tier), nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.ResponseID != "resp_fast_ws" {
				t.Fatalf("response ID = %q", result.ResponseID)
			}
		})
	}
}

func codexFastRequestBody(t *testing.T, tier string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "gpt-5", "input": "hello", "service_tier": tier,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertCodexFastRequest(t *testing.T, body []byte) {
	t.Helper()
	if tier := gjson.GetBytes(body, "service_tier").String(); tier != "fast" {
		t.Errorf("outbound service_tier = %q, want fast", tier)
	}
	if !gjson.GetBytes(body, "input").IsArray() || gjson.GetBytes(body, "store").Type != gjson.False {
		t.Error("request lost the original Codex input and store conversions")
	}
}
