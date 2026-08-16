package cpa

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/claude"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

func setClaudeExecutor(t *testing.T, adapter *Adapter, executor claude.Executor) {
	t.Helper()
	bridge, ok := adapter.providers[channel.ProviderClaude].(*claudeProviderBridge)
	if !ok {
		t.Fatal("Claude provider bridge is unavailable")
	}
	bridge.executor = executor
}

func TestClaudeAdapterExecutesEveryDeclaredProtocol(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		response   string
	}{
		{
			name: "Anthropic Messages", protocol: protocol.Anthropic,
			operation: execution.OperationChatCompletion, wantFormat: "claude",
			response: `{"id":"msg-one","type":"message","model":"claude-sonnet-4-5","content":[],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`,
		},
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions,
			operation: execution.OperationChatCompletion, wantFormat: "openai",
			response: `{"id":"chat-one","object":"chat.completion","model":"claude-sonnet-4-5","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCreate, wantFormat: "openai-response",
			response: `{"id":"resp-one","object":"response","model":"claude-sonnet-4-5","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		},
		{
			name: "Gemini GenerateContent", protocol: protocol.Gemini,
			operation: execution.OperationChatCompletion, wantFormat: "gemini",
			response: `{"modelVersion":"claude-sonnet-4-5","candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, keyService, row := newClaudeAdapterFixture(t)
			fake := &fakeClaudeExecutor{response: claude.ExecuteResponse{
				Payload: []byte(test.response),
				Headers: http.Header{"X-Request-Id": {"claude-request-one"}},
			}}
			setClaudeExecutor(t, adapter, fake)
			spec := validClaudeSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			if test.protocol != protocol.Anthropic {
				spec.RouteMode = execution.RouteConverted
			}

			result := adapter.Execute(t.Context(), spec)
			if result.Error != nil || len(fake.requests) != 1 ||
				fake.requests[0].Format != test.wantFormat || result.UpstreamProtocol != protocol.Anthropic {
				t.Fatalf("result=%#v requests=%#v", result, fake.requests)
			}
			if result.UpstreamRequestID != "claude-request-one" || result.Usage == nil ||
				result.Usage.Normalized.Tokens.UncachedInput != 2 || result.Usage.Normalized.Tokens.Output != 1 {
				t.Fatalf("request ID/usage = %q/%#v", result.UpstreamRequestID, result.Usage)
			}
		})
	}
}

func TestClaudeAdapterStreamsEveryDeclaredProtocol(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		payload    string
		terminal   string
	}{
		{
			name: "Anthropic Messages", protocol: protocol.Anthropic,
			operation: execution.OperationChatCompletion, wantFormat: "claude",
			payload:  "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-sonnet-4-5\"}}",
			terminal: "\n\n",
		},
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions,
			operation: execution.OperationChatCompletion, wantFormat: "openai",
			payload:  `{"id":"chat-one","object":"chat.completion.chunk","model":"claude-sonnet-4-5","choices":[{"finish_reason":"stop"}]}`,
			terminal: "data: [DONE]\n\n",
		},
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCreate, wantFormat: "openai-response",
			payload:  `data: {"type":"response.completed","response":{"id":"resp-one","model":"claude-sonnet-4-5"}}`,
			terminal: "\n\n",
		},
		{
			name: "Gemini GenerateContent", protocol: protocol.Gemini,
			operation: execution.OperationChatCompletion, wantFormat: "gemini",
			payload:  `{"modelVersion":"claude-sonnet-4-5","candidates":[{"finishReason":"STOP"}]}`,
			terminal: "\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, keyService, row := newClaudeAdapterFixture(t)
			chunks := make(chan claude.ExecuteStreamChunk, 1)
			chunks <- claude.ExecuteStreamChunk{Payload: []byte(test.payload)}
			close(chunks)
			fake := &fakeClaudeExecutor{streamResponse: &claude.ExecuteStreamResponse{Chunks: chunks}}
			setClaudeExecutor(t, adapter, fake)
			spec := validClaudeSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			spec.ClientModel = "public-claude"
			if test.protocol != protocol.Anthropic {
				spec.RouteMode = execution.RouteConverted
			}

			var wire strings.Builder
			result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					_, _ = wire.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil || len(fake.requests) != 1 ||
				fake.requests[0].Format != test.wantFormat || result.UpstreamProtocol != protocol.Anthropic {
				t.Fatalf("result=%#v requests=%#v", result, fake.requests)
			}
			if !strings.HasSuffix(wire.String(), test.terminal) {
				t.Fatalf("stream wire = %q, want suffix %q", wire.String(), test.terminal)
			}
			if !strings.Contains(wire.String(), "public-claude") || strings.Contains(wire.String(), "claude-sonnet-4-5") {
				t.Fatalf("stream model alias was not restored: %q", wire.String())
			}
		})
	}
}

func newClaudeAdapterFixture(t *testing.T) (*Adapter, encryption.Service, models.Credential) {
	t.Helper()
	canonical, err := claude.MarshalCredential(claude.Credential{
		Type: claude.Provider, AccessToken: "claude-access", RefreshToken: "claude-refresh",
		AccountUUID: "claude-account-one", DeviceIDs: []string{strings.Repeat("a", 64)},
		Expire: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, _, keyService, row := newSubscriptionAdapterFixture(
		t,
		string(channel.Claude),
		canonical,
		"claude-account-one",
	)
	return adapter, keyService, row
}

func validClaudeSpec(t *testing.T, row models.Credential, keyService encryption.Service) execution.AttemptSpec {
	t.Helper()
	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID: "request-one", AttemptID: "attempt-one", Sequence: 1,
		ChannelID: string(channel.Claude), RouteMode: execution.RouteNative,
		ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion,
		ClientModel: "claude-sonnet-4-5", UpstreamModel: "claude-sonnet-4-5",
		Method: http.MethodPost, Path: "/v1/messages",
		Body:       []byte(`{"model":"claude-sonnet-4-5","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
		Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext)),
	})
}
