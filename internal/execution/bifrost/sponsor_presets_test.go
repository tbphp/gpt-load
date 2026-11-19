package bifrost

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSponsorPresetsUseDocumentedNativePaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		channelID      channel.ID
		clientProtocol protocol.Protocol
		operation      execution.Operation
		path           string
		upstreamPath   string
		authHeader     string
		body           string
		response       string
	}{
		{
			name: "modelink-responses", channelID: channel.Modelink, clientProtocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCreate,
			path:      "/v1/responses", upstreamPath: "/bypass/openai/v1/responses", authHeader: "Authorization",
			body:     `{"model":"client-model","input":"hello"}`,
			response: `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[]}`,
		},
		{
			name: "modelink-images", channelID: channel.Modelink, clientProtocol: protocol.OpenAIImages,
			operation: execution.OperationImagesGenerate,
			path:      "/v1/images/generations", upstreamPath: "/bypass/openai/v1/images/generations", authHeader: "Authorization",
			body:     `{"model":"client-model","prompt":"hello"}`,
			response: `{"created":1,"data":[{"url":"https://images.example/result.png"}]}`,
		},
		{
			name: "modelink-anthropic", channelID: channel.Modelink, clientProtocol: protocol.Anthropic,
			operation: execution.OperationChatCompletion,
			path:      "/v1/messages", upstreamPath: "/bypass/anthropic/v1/messages", authHeader: "X-Api-Key",
			body:     `{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`,
			response: `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			name: "modelink-gemini", channelID: channel.Modelink, clientProtocol: protocol.Gemini,
			operation:    execution.OperationChatCompletion,
			path:         "/v1beta/models/client-model:generateContent",
			upstreamPath: "/bypass/vertex/v1/models/upstream-model:generateContent", authHeader: "X-Goog-Api-Key",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}]}`,
		},
		{
			name: "modelink-gemini-probe", channelID: channel.Modelink, clientProtocol: protocol.Gemini,
			operation:    execution.OperationProbe,
			upstreamPath: "/bypass/vertex/v1/models/upstream-model:generateContent", authHeader: "X-Goog-Api-Key",
			response: `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}]}`,
		},
		{
			name: "fenno-count-tokens", channelID: channel.Fenno, clientProtocol: protocol.Anthropic,
			operation: execution.OperationCountTokens,
			path:      "/v1/messages/count_tokens", upstreamPath: "/v1/messages/count_tokens", authHeader: "X-Api-Key",
			body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
			response: `{"input_tokens":7}`,
		},
		{
			name: "fenno-embeddings", channelID: channel.Fenno, clientProtocol: protocol.OpenAIEmbeddings,
			operation: execution.OperationEmbeddingsCreate,
			path:      "/v1/embeddings", upstreamPath: "/v1/embeddings", authHeader: "Authorization",
			body:     `{"model":"client-model","input":"hello"}`,
			response: `{"object":"list","model":"upstream-model","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/tenant"+test.upstreamPath {
					t.Errorf("upstream request = %s %s, want POST /tenant%s", request.Method, request.URL.Path, test.upstreamPath)
				}
				wantCredential := testAPIKey
				if test.authHeader == "Authorization" {
					wantCredential = "Bearer " + testAPIKey
				}
				if request.Header.Get(test.authHeader) != wantCredential {
					t.Errorf("upstream request is missing expected %s credential", test.authHeader)
				}
				if test.operation != execution.OperationProbe && request.URL.Query().Get("trace") != "kept" {
					t.Error("upstream request is missing client query")
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			manager, target := newOpenCodeManagerForTest(t, test.channelID, server.URL+"/tenant")

			method, rawQuery := http.MethodPost, "trace=kept"
			if test.operation == execution.OperationProbe {
				method, rawQuery = "", ""
			}
			spec := freezeTestAttempt(execution.AttemptSpec{
				RequestID: "partner-request", AttemptID: "partner-attempt", Sequence: 1,
				ChannelID: string(test.channelID), ClientProtocol: test.clientProtocol, Operation: test.operation,
				ClientModel: "client-model", UpstreamModel: "upstream-model",
				Method: method, Path: test.path, RawQuery: rawQuery,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(test.body),
				TargetConfig: target.TargetConfig,
				Credential:   execution.NewCredentialSnapshot(17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`)),
			})
			result := manager.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
				result.UpstreamProtocol != test.clientProtocol {
				t.Fatalf("result = %+v, validation = %v, body = %s", result, err, result.Body)
			}
		})
	}
}
