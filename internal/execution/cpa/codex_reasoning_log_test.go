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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
)

func TestCodexGatewayLogsFinalReasoningEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			adapter, _, credentials, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			ref, ok := credentials.CredentialRef(row.ID)
			if !ok {
				t.Fatal("credential is unavailable")
			}
			manager := state.NewManager()
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{
					ID: row.GroupID, Name: "codex", ChannelID: channel.Codex, ConnectionType: "subscription",
					Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-6-astra"}}, Enabled: true,
				}},
				Credentials: []state.CredentialConfig{{
					ID: ref.ID, GroupID: ref.GroupID, Version: ref.Version, IdentityGeneration: ref.IdentityGeneration,
					Fingerprint: ref.Fingerprint, Status: state.CredentialStatusActive,
				}},
				AccessKeys: []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
			}); err != nil {
				t.Fatal(err)
			}
			sink := &searchRequestLogSink{}
			handler := gateway.NewHandler(manager, credentials, keyService, gateway.NewExecutionForwarder(contextTransportCPAExecutor{adapter}),
				dialect.NewSet(dialect.NewOpenAIResponses()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, sink, nil)
			engine := gin.New()
			routes, err := httproute.NewRegistry(handler.HTTPModule())
			if err != nil {
				t.Fatal(err)
			}
			if err := routes.Bind(engine); err != nil {
				t.Fatal(err)
			}
			for index, test := range []struct {
				input string
				want  string
			}{
				{`[{"role":"user","content":"hello"}]`, "high"},
				{`[{"type":"configuration_update","reasoning":{"effort":"high"}},{"type":"configuration_update","reasoning":{"effort":"low"}},{"role":"user","content":"continue"}]`, "low"},
			} {
				calls := 0
				transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					body, err := io.ReadAll(request.Body)
					if err != nil {
						return nil, err
					}
					if gjson.GetBytes(body, "reasoning.effort").String() != "high" || gjson.GetBytes(body, "input").Raw != test.input {
						t.Error("upstream baseline or configuration updates changed")
					}
					// 上游回显基准档位，日志仍须采用本轮配置更新后的档位。
					response := `data: {"type":"response.completed","response":{"id":"resp_effort","object":"response","status":"completed","model":"gpt-6-astra","reasoning":{"effort":"high"},"output":[],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}` + "\n\n"
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
				})
				ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
				body := fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"reasoning":{"effort":"high"},"input":%s}`, stream, test.input)
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer gl-client")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusOK || calls != 1 || len(sink.events) != index+1 {
					t.Fatalf("response=%d calls=%d logs=%d", response.Code, calls, len(sink.events))
				}
				log := sink.events[index]
				if len(log.Attempts) != 1 {
					t.Fatalf("attempts=%d", len(log.Attempts))
				}
				if log.Reasoning.Effort != test.want || log.Attempts[0].Reasoning.Effort != test.want {
					t.Errorf("request=%q attempt=%q, want final upstream effort %q", log.Reasoning.Effort, log.Attempts[0].Reasoning.Effort, test.want)
				}
			}
		})
	}
}

// 测试用上下文 transport 替代网络，避免显式代理设置让 CPA 忽略假上游。
type contextTransportCPAExecutor struct{ *Adapter }

func (executor contextTransportCPAExecutor) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	spec.Proxy = outboundproxy.Effective{}
	return executor.Adapter.Execute(ctx, spec)
}

func (executor contextTransportCPAExecutor) ExecuteStream(ctx context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
	spec.Proxy = outboundproxy.Effective{}
	return executor.Adapter.ExecuteStream(ctx, spec, sink)
}
