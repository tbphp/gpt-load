package cpa

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAdapterRejectsOnlyLossyMidConversationInstructions(t *testing.T) {
	t.Parallel()
	for _, channelID := range []channel.ID{channel.Codex, channel.Grok, channel.Claude, channel.Antigravity} {
		for _, clientProtocol := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic} {
			for _, role := range []string{"system", "user"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", channelID, clientProtocol, role, stream), func(t *testing.T) {
						registry := channel.NewRegistry()
						target, err := registry.Resolve(channelID, nil)
						if err != nil {
							t.Fatal(err)
						}
						operation, path, field := execution.OperationChatCompletion, "/v1/chat/completions", "messages"
						if clientProtocol == protocol.OpenAIResponses {
							operation, path, field = execution.OperationResponsesCreate, "/v1/responses", "input"
						} else if clientProtocol == protocol.Anthropic {
							path = "/v1/messages"
						}
						mode, exists := target.Mode(clientProtocol, operation)
						if !exists {
							t.Fatal("expected subscription route is missing")
						}
						body, err := json.Marshal(map[string]any{"model": "client-model", "max_tokens": 32, field: []map[string]any{
							{"role": "user", "content": "start"},
							{"role": "assistant", "content": "reply"},
							{"role": role, "content": "<system-reminder>new instruction</system-reminder>"},
							{"role": "user", "content": "next"},
						}})
						if err != nil {
							t.Fatal(err)
						}
						preparer := &fakeCredentialPreparer{evidence: &execution.ErrorEvidence{
							Kind: execution.ErrorKindInternal, Code: "after_request_validation", Summary: "test boundary reached",
						}}
						adapter := NewAdapter(nil, registry)
						adapter.credentials = preparer
						spec := execution.NewAttemptSpec(execution.AttemptSpec{
							RequestID: "fidelity-request", AttemptID: "fidelity-attempt", Sequence: 1,
							ChannelID: string(channelID), TargetConfig: target.TargetConfig, RouteMode: mode,
							ClientProtocol: clientProtocol, Operation: operation, Method: http.MethodPost, Path: path,
							ClientModel: "client-model", UpstreamModel: "upstream-model", Body: body,
							Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{}`)),
						})
						var evidence *execution.ErrorEvidence
						if stream {
							evidence = adapter.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil }).Error
						} else {
							evidence = adapter.Execute(t.Context(), spec).Error
						}
						wantRejected := role == "system" && mode == execution.RouteConverted &&
							(channelID == channel.Claude || channelID == channel.Antigravity || clientProtocol == protocol.Anthropic)
						if wantRejected {
							if preparer.calls != 0 || evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
								evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
								t.Fatalf("lossy conversion reached credentials: calls=%d error=%+v", preparer.calls, evidence)
							}
						} else if preparer.calls != 1 || evidence == nil || evidence.Code != "after_request_validation" {
							t.Fatalf("preservable input was rejected: calls=%d error=%+v", preparer.calls, evidence)
						}
					})
				}
			}
		}
	}
}
