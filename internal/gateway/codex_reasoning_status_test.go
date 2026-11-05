package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestCodexReasoningStatusProtocolGuard(t *testing.T) {
	request := &dialect.ParsedRequest{Body: []byte(codexReasoningStatusRequest), Header: make(http.Header)}
	for _, selected := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini} {
		if got := codexResponsesRequest(request, selected, channel.ProviderCodex); got != request {
			t.Fatalf("non-Responses request changed for %s", selected)
		}
	}
	got := codexResponsesRequest(request, protocol.OpenAIResponses, channel.ProviderCodex)
	assertReasoningStatusInput(t, got.Body, true)
	assertReasoningStatusInput(t, request.Body, false)
	got.Header.Set("X-Attempt", "codex")
	if request.Header.Get("X-Attempt") != "" {
		t.Fatal("candidate request aliases original headers")
	}
	if second := codexResponsesRequest(got, protocol.OpenAIResponses, channel.ProviderCodex); second != got {
		t.Fatal("already-clean request changed again")
	}
}

// 模拟 pi-ai 从 Devin thinkingSignature 原样带回的历史，以及原生 Codex 密文和工具调用。
const codexReasoningStatusRequest = `{"model":"gpt-sol","store":false,"input":[{"type":"reasoning","id":"item_9d700c62-046b-426a-8431-27497dcd0a0a_0","status":"completed","encrypted_content":"","summary":[{"type":"summary_text","text":"Devin summary"}]},{"type":"reasoning","id":"rs_native","status":"completed","encrypted_content":"gAAAAAB_native_opaque_content","summary":[]},{"type":"message","id":"msg_keep","status":"completed","role":"assistant","content":[{"type":"output_text","text":"history"}]},{"type":"function_call","id":"fc_keep","call_id":"call_keep","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_keep","output":"result"},{"type":"message","role":"user","content":"continue"}],"metadata":{"integer":9007199254740993}}`

func reasoningStatusSuccess() UpstreamResult {
	return UpstreamResult{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(contextRecoverySuccess), RequestWritten: true}
}

// 使用真实 channel/provider 和订阅凭据解析；Forwarder 仅替代 CPA 发送边界。
func reasoningStatusGateway(t *testing.T, forwarder AttemptForwarder, ids ...channel.ID) (*Handler, *gin.Engine) {
	t.Helper()
	h, manager, registry := newHandlerForTest(t, forwarder)
	input := state.CompileInput{ChannelRegistry: channel.NewRegistry(),
		SystemSettings: config.Settings{state.SettingRetryCount: 4},
		AccessKeys:     []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: h.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	}
	var entries []state.CredentialEntry
	for i, id := range ids {
		groupID := uint(i + 1)
		connectionType, _ := input.ChannelRegistry.ConnectionType(id)
		params := json.RawMessage(`{}`)
		if id == channel.CLIProxyAPI {
			params = json.RawMessage(`{"base_url":"https://relay.example"}`)
		}
		input.Groups = append(input.Groups, state.GroupConfig{ID: groupID, Name: string(id), ChannelID: id,
			ConnectionType: connectionType, Params: params, Enabled: true, Priority: int32(len(ids) - i),
			Models:   []state.ModelConfig{{ID: "gpt-sol"}},
			Settings: config.Settings{state.SettingParameterOverrides: []any{map[string]any{"set": map[string]any{"candidate_marker": string(id)}}}},
		})
		input.Credentials = append(input.Credentials, testCredentialConfig(groupID, groupID))
		credential := `{"api_key":"sk-test"}`
		switch id {
		case channel.Codex:
			credential = `{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account-1"}`
		case channel.Devin:
			credential = `{"type":"devin","session_token":"devin-session-token$test","user_id":"account"}`
		}
		encrypted, err := h.encryption.Encrypt(credential)
		if err != nil {
			t.Fatal(err)
		}
		entry := testCredentialEntry(t, h.encryption, groupID, groupID, "unused")
		entry.EncryptedValue = encrypted
		entries = append(entries, entry)
	}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	h.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	return h, engine
}

func reasoningStatusInput(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var decoded struct {
		Input []map[string]any `json:"input"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Input
}

func assertReasoningStatusInput(t *testing.T, body []byte, cleaned bool) {
	t.Helper()
	want := reasoningStatusInput(t, []byte(codexReasoningStatusRequest))
	if cleaned {
		delete(want[0], "status")
		delete(want[1], "status")
	}
	if got := reasoningStatusInput(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("outbound input changed beyond reasoning.status (cleaned=%t): %s", cleaned, body)
	}
}

func TestResponsesCodexReasoningStatusAfterDevinQuota(t *testing.T) {
	for _, status := range []int{http.StatusPaymentRequired, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: status, Header: make(http.Header), RequestWritten: true,
				ExecutionError: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: status,
					Hint: execution.FailureHintCandidateUnavailable, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing},
			}, reasoningStatusSuccess()}}
			_, engine := reasoningStatusGateway(t, f, channel.Devin, channel.Codex)
			serveContinuation(t, engine, "gl-client", codexReasoningStatusRequest, http.StatusOK)
			if len(f.inputs) != 2 || f.inputs[0].ChannelID != string(channel.Devin) || f.inputs[1].ChannelID != string(channel.Codex) {
				t.Fatalf("want Devin quota failure then Codex, got %d attempts", len(f.inputs))
			}
			assertReasoningStatusInput(t, f.inputs[0].Request.Body, false)
			assertReasoningStatusInput(t, f.inputs[1].Request.Body, true)
			if !strings.Contains(string(f.inputs[1].Request.Body), `"candidate_marker":"codex"`) {
				t.Fatal("Codex inherited Devin's prepared body")
			}
		})
	}
}

func TestResponsesCodexReasoningStatusTwoTurns(t *testing.T) {
	for _, id := range []channel.ID{channel.Codex, channel.Devin, channel.OpenAI} {
		t.Run(string(id), func(t *testing.T) {
			f := &scriptedForwarder{results: []UpstreamResult{reasoningStatusSuccess(), reasoningStatusSuccess()}}
			_, engine := reasoningStatusGateway(t, f, id)
			for range 2 {
				serveContinuation(t, engine, "gl-client", codexReasoningStatusRequest, http.StatusOK)
			}
			if len(f.inputs) != 2 {
				t.Fatalf("attempts = %d, want 2", len(f.inputs))
			}
			for _, input := range f.inputs {
				assertReasoningStatusInput(t, input.Request.Body, id == channel.Codex)
				if !strings.Contains(string(input.Request.Body), "9007199254740993") {
					t.Fatal("unrelated integer lost precision")
				}
			}
		})
	}
}

func TestResponsesCodexReasoningStatusFailoverPreservesDevin(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_failure", true: "encrypted_cleanup_replay"}[recovery], func(t *testing.T) {
			// OpenAI 是不启用本清理的对照组；它沿用相同的既有密文恢复路径。
			var baseline []map[string]any
			for _, first := range []channel.ID{channel.OpenAI, channel.Codex} {
				results := []UpstreamResult{}
				if recovery {
					results = append(results, UpstreamResult{StatusCode: http.StatusBadRequest, Header: make(http.Header), ErrorSummary: responseItemResourceError, RequestWritten: true})
				}
				results = append(results, UpstreamResult{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: []byte(`{"error":{"message":"quota exhausted"}}`), RequestWritten: true}, reasoningStatusSuccess())
				f := &scriptedForwarder{results: results}
				_, engine := reasoningStatusGateway(t, f, first, channel.Devin)
				serveContinuation(t, engine, "gl-client", codexReasoningStatusRequest, http.StatusOK)
				if len(f.inputs) != len(results) {
					t.Fatalf("attempts = %d, want %d", len(f.inputs), len(results))
				}
				for _, input := range f.inputs[:len(f.inputs)-1] {
					items := reasoningStatusInput(t, input.Request.Body)
					_, status := items[0]["status"]
					if status != (first != channel.Codex) {
						t.Fatalf("wrong reasoning.status on %s attempt: %s", first, input.Request.Body)
					}
				}
				last := f.inputs[len(f.inputs)-1]
				if last.ChannelID != string(channel.Devin) || !strings.Contains(string(last.Request.Body), `"candidate_marker":"devin"`) {
					t.Fatalf("failover inherited another candidate's body: %s", last.Request.Body)
				}
				got := reasoningStatusInput(t, last.Request.Body)
				if baseline == nil {
					baseline = got
				} else if !reflect.DeepEqual(got, baseline) {
					t.Fatalf("Codex cleanup leaked to Devin: got %s, baseline %#v", last.Request.Body, baseline)
				}
				if !recovery {
					assertReasoningStatusInput(t, last.Request.Body, false)
				}
			}
		})
	}
}

func TestWebsocketCodexReasoningStatusTwoTurns(t *testing.T) {
	for _, id := range []channel.ID{channel.Codex, channel.OpenAI} {
		t.Run(string(id), func(t *testing.T) {
			observed := &encryptedRecoveryBodies{}
			h, engine := reasoningStatusGateway(t, nil, id)
			h.forwarder = websocketScriptForwarder{open: func(_ context.Context, input ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
				session := &websocketScriptSession{done: make(chan struct{})}
				session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
					observed.add(input.ChannelID, string(body))
					_, bodies := observed.snapshot()
					if err := emit(ctx, websocketCompleted(fmt.Sprintf("resp_status_%d", len(bodies)), "")); err != nil {
						return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: err.Error()}}
					}
					return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
				}
				return session, execution.WebsocketResult{}
			}}
			server := httptest.NewServer(engine)
			defer server.Close()
			conn := dialGatewayWebsocket(t, server.URL)
			body := strings.Replace(codexReasoningStatusRequest, `{"model":`, `{"type":"response.create","model":`, 1)
			for range 2 {
				if reply := websocketContextRecoveryExchange(t, conn, body); !strings.Contains(reply, `"response.completed"`) {
					t.Fatalf("unexpected reply: %s", reply)
				}
			}
			_, bodies := observed.snapshot()
			if len(bodies) != 2 {
				t.Fatalf("turns = %d, want 2", len(bodies))
			}
			for _, sent := range bodies {
				assertReasoningStatusInput(t, []byte(sent), id == channel.Codex)
			}
		})
	}
}
