package embedded

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	internalcache "github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
	"github.com/tidwall/gjson"
)

func TestAntigravityReplayStaysWithinPrivateContinuityScope(t *testing.T) {
	// 只满足 SDK 的签名格式校验，不包含真实上游数据。
	const signature = "EjQKMgEMOdbHO0Gd+c9Mxk4ELwPGbpCEcp2mFfYYLix2UVtBH3fL8GECc4+JITVnHF4qZDsA"
	const first = `{"model":"gemini-3-flash","messages":[{"role":"user","content":"What time is it?"}],"tools":[{"type":"function","function":{"name":"get_time","parameters":{"type":"object","properties":{"tz":{"type":"string"}}}}}]}`
	for _, stream := range []bool{false, true} {
		for _, differentScope := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/different_scope=%t", stream, differentScope), func(t *testing.T) {
				internalcache.ClearAntigravityReasoningReplayCache()
				t.Cleanup(internalcache.ClearAntigravityReasoningReplayCache)
				previous := internalcache.SignatureCacheEnabled()
				t.Cleanup(func() { internalcache.SetSignatureCacheEnabled(previous) })
				requests := make(chan []byte, 2)
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Error(err)
						writer.WriteHeader(http.StatusInternalServerError)
						return
					}
					requests <- body
					response := `{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_time","args":{"tz":"UTC"}},"thoughtSignature":"` + signature + `"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}}`
					if strings.Contains(string(body), "functionResponse") {
						response = `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"Done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}}}`
					}
					if stream {
						writer.Header().Set("Content-Type", "text/event-stream")
						response = "data: " + response + "\n\n"
					} else {
						writer.Header().Set("Content-Type", "application/json")
					}
					if _, err := io.WriteString(writer, response); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				executor := newAntigravityHTTPExecutor(server.URL)
				credential := AntigravityCredential{
					Type: ProviderAntigravity, AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh",
					AccountID: "account", Email: "fixture@example.invalid", ProjectID: "project",
					Expire: time.Now().Add(time.Hour).Format(time.RFC3339),
				}
				execute := func(payload []byte, scope string) string {
					request := ExecuteRequest{
						Model: "gemini-3-flash", Payload: payload, OriginalRequest: payload, Format: "openai",
						Headers: http.Header{"Session_id": {"shared-client-session"}}, ContinuityKey: scope,
					}
					if !stream {
						response, err := executor.ExecuteCanonical(t.Context(), "credential", credential, request)
						if err != nil {
							t.Fatal(err)
						}
						return gjson.GetBytes(response.Payload, "choices.0.message.tool_calls.0.id").String()
					}
					response, err := executor.ExecuteStreamCanonical(t.Context(), "credential", credential, request)
					if err != nil {
						t.Fatal(err)
					}
					var callID string
					for chunk := range response.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
						for _, line := range bytes.Split(chunk.Payload, []byte("\n")) {
							line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
							if id := gjson.GetBytes(line, "choices.0.delta.tool_calls.0.id").String(); id != "" {
								callID = id
							}
						}
					}
					return callID
				}
				callID := execute([]byte(first), "trusted-tenant-A/credential/model")
				<-requests
				if callID == "" {
					t.Fatal("first response did not return a tool call")
				}
				encodedID, err := json.Marshal(callID)
				if err != nil {
					t.Fatal(err)
				}
				// 客户端只回传工具调用和结果，签名必须由正确的私有缓存补回。
				followup := fmt.Appendf(nil, `{"model":"gemini-3-flash","messages":[{"role":"user","content":"What time is it?"},{"role":"assistant","tool_calls":[{"id":%s,"type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"UTC\"}"}}]},{"role":"tool","tool_call_id":%s,"content":"12:00 UTC"}],"tools":[{"type":"function","function":{"name":"get_time","parameters":{"type":"object","properties":{"tz":{"type":"string"}}}}}]}`, encodedID, encodedID)
				scope := "trusted-tenant-A/credential/model"
				if differentScope {
					scope = "trusted-tenant-B/credential/model"
				}
				execute(followup, scope)
				var got string
				var found bool
				for _, content := range gjson.GetBytes(<-requests, "request.contents").Array() {
					for _, part := range content.Get("parts").Array() {
						if part.Get("functionCall.name").String() == "get_time" {
							found = true
							got = part.Get("thoughtSignature").String()
						}
					}
				}
				if !found {
					t.Fatal("follow-up request lost the historical tool call")
				}
				if !differentScope && got != signature {
					t.Fatalf("same scope lost the upstream signature: %q", got)
				}
				if differentScope && got == signature {
					t.Fatal("different scope reused the first scope's upstream signature")
				}
			})
		}
	}
}
