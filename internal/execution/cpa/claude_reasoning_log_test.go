package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/state"
	"gpt-load/internal/subscription/providers/claude"
)

func TestClaudeGatewayLogsFinalReasoningFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name, model, thinking, mode string
			budget                      int64
		}{
			{"manual budget", "claude-sonnet-4-5", `"thinking":{"type":"enabled","budget_tokens":4096}`, "enabled", 4096},
			{"adaptive effort", "claude-sonnet-4-6", `"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}`, "adaptive", 0},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				canonical, err := claude.MarshalCredential(claudeProviderCredentialForTest(t).value)
				if err != nil {
					t.Fatal(err)
				}
				adapter, _, credentials, keyService, row := newSubscriptionAdapterFixture(t, string(channel.Claude), canonical, "account-one")
				ref, ok := credentials.CredentialRef(row.ID)
				if !ok {
					t.Fatal("credential missing")
				}
				manager := state.NewManager()
				_, err = manager.Publish(state.CompileInput{
					ChannelRegistry: channel.NewRegistry(),
					Groups:          []state.GroupConfig{{ID: row.GroupID, Name: "claude", ChannelID: channel.Claude, ConnectionType: "subscription", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: test.model}}, Enabled: true}},
					Credentials:     []state.CredentialConfig{{ID: ref.ID, GroupID: ref.GroupID, Version: ref.Version, IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint, Status: state.CredentialStatusActive}},
					AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
				})
				if err != nil {
					t.Fatal(err)
				}
				sink := &searchRequestLogSink{}
				handler := gateway.NewHandler(manager, credentials, keyService, gateway.NewExecutionForwarder(contextTransportCPAExecutor{adapter}), dialect.NewSet(dialect.NewAnthropic()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, sink, nil)
				engine := gin.New()
				routes, err := httproute.NewRegistry(handler.HTTPModule())
				if err != nil {
					t.Fatal(err)
				}
				if err = routes.Bind(engine); err != nil {
					t.Fatal(err)
				}
				wireMode := ""
				var wireBudget int64
				calls := 0
				transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					body, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					wireMode = gjson.GetBytes(body, "thinking.type").String()
					wireBudget = gjson.GetBytes(body, "thinking.budget_tokens").Int()
					response := `{"id":"msg_diag","type":"message","role":"assistant","model":"` + test.model + `","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
						response = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_diag","type":"message","role":"assistant","model":"` + test.model + `","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n" +
							"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
							"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}` + "\n\n" +
							"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
							"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n" +
							"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				payload := fmt.Sprintf(`{"model":%q,"stream":%t,"max_tokens":8192,%s,"messages":[{"role":"user","content":"hello"}]}`, test.model, stream, test.thinking)
				request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(payload)).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer gl-client")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusOK || calls != 1 || len(sink.events) != 1 {
					t.Fatalf("response=%d calls=%d logs=%d", response.Code, calls, len(sink.events))
				}
				log := sink.events[0]
				if len(log.Attempts) != 1 {
					t.Fatalf("attempts=%d", len(log.Attempts))
				}
				t.Logf("actual upstream mode=%q budget=%d; request log=%+v; attempt log=%+v", wireMode, wireBudget, log.Reasoning, log.Attempts[0].Reasoning)
				if wireMode != test.mode || wireBudget != test.budget {
					t.Fatalf("unexpected upstream thinking: mode=%s budget=%d", wireMode, wireBudget)
				}
				for _, observed := range []reasoning.Config{log.Reasoning, log.Attempts[0].Reasoning} {
					if observed.Mode != wireMode {
						t.Errorf("logged mode=%q want %q", observed.Mode, wireMode)
					}
					if wireBudget != 0 {
						if observed.BudgetTokens == nil || *observed.BudgetTokens != wireBudget {
							t.Errorf("logged budget=%v want %d", observed.BudgetTokens, wireBudget)
						}
					} else if observed.BudgetTokens != nil {
						t.Errorf("unexpected logged budget=%d", *observed.BudgetTokens)
					}
				}
			})
		}
	}
}
