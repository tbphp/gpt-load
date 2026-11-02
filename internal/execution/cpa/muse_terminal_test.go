package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/telemetry"
)

func museTerminalCredential() []byte {
	return []byte(`{"type":"muse","access_token":"access","dca_token":"dca:renewal","email":"account"}`)
}

func newMuseTerminalGateway(t *testing.T, baseURL string) (*gin.Engine, *searchRequestLogSink) {
	t.Helper()
	id, model := channel.Muse, "muse-spark-1.3"
	adapter, _, registry, keys, row := newSubscriptionAdapterFixture(t, string(id), museTerminalCredential(), "account")
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	generation := stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, string(id), "subscription", json.RawMessage(`{}`))
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: row.ID, GroupID: row.GroupID, Version: row.SecretVersion, IdentityGeneration: generation, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive, EncryptedValue: row.Data}}); err != nil {
		t.Fatal(err)
	}
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		SystemSettings:  config.Settings{state.SettingRetryCount: 0},
		AccessKeys:      []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keys.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
		Groups:          []state.GroupConfig{{ID: row.GroupID, Name: "audit", ChannelID: id, ConnectionType: "subscription", Params: params, Models: []state.ModelConfig{{ID: model}}, Enabled: true}},
		Credentials:     []state.CredentialConfig{{ID: row.ID, GroupID: row.GroupID, Version: row.SecretVersion, IdentityGeneration: generation, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive}},
	}
	manager := state.NewManager()
	_, err = manager.Publish(input)
	if err != nil {
		t.Fatal(err)
	}
	sink := &searchRequestLogSink{}
	handler := gateway.NewHandler(manager, registry, keys, gateway.NewExecutionForwarder(contextTransportCPAExecutor{adapter}), dialect.NewSet(dialect.NewOpenAIResponses(), dialect.NewOpenAI(), dialect.NewAnthropic(), dialect.NewGemini()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, sink, nil)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	return engine, sink
}

func TestMuseGatewayPreservesStreamTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "muse-spark-1.3"
	for _, terminal := range []string{"completed", "incomplete", "missing"} {
		for _, format := range []string{"responses", "chat", "anthropic", "gemini"} {
			t.Run(terminal+"/"+format, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":%q,\"output\":[]}}\n\n", model)
					io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"response_id\":\"resp_1\",\"output_index\":0,\"item_id\":\"msg_1\",\"content_index\":0,\"delta\":\"partial answer\"}\n\n")
					if terminal == "missing" {
						return
					}
					details := ""
					if terminal == "incomplete" {
						details = ",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}"
					}
					fmt.Fprintf(w, "data: {\"type\":\"response.%s\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":%q,\"model\":%q%s,\"output\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n", terminal, terminal, model, details)
				}))
				defer server.Close()
				engine, sink := newMuseTerminalGateway(t, server.URL)
				path, body := "/v1/responses", fmt.Sprintf("{\"model\":%q,\"stream\":true,\"input\":\"hello\"}", model)
				switch format {
				case "chat":
					path = "/v1/chat/completions"
					body = fmt.Sprintf("{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}", model)
				case "anthropic":
					path = "/v1/messages"
					body = fmt.Sprintf("{\"model\":%q,\"stream\":true,\"max_tokens\":64,\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}", model)
				case "gemini":
					path = "/v1beta/models/" + model + ":streamGenerateContent"
					body = "{\"contents\":[{\"role\":\"user\",\"parts\":[{\"text\":\"hello\"}]}]}"
				}
				req := httptest.NewRequest("POST", path, strings.NewReader(body))
				req = req.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", server.Client().Transport))
				req.Header.Set("Authorization", "Bearer gl-client")
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, req)
				if calls.Load() != 1 || response.Code != 200 || len(sink.events) != 1 {
					t.Fatalf("invalid reproduction: calls=%d status=%d logs=%d body=%s", calls.Load(), response.Code, len(sink.events), response.Body.String())
				}
				if terminal == "missing" {
					if sink.events[0].Status == telemetry.RequestStatusSuccess {
						t.Fatalf("missing terminal incorrectly succeeded for %s", format)
					}
					if strings.Contains(response.Body.String(), "[DONE]") || strings.Contains(response.Body.String(), `"finishReason":"STOP"`) {
						t.Fatal("missing terminal gained a synthetic success marker")
					}
					return
				}
				if sink.events[0].Usage.Result.Tokens.UncachedInput != 2 || sink.events[0].Usage.Result.Tokens.Output != 1 {
					t.Errorf("usage lost: %+v", sink.events[0].Usage.Result)
				}
				want := telemetry.RequestStatusSuccess
				if terminal == "incomplete" {
					want = telemetry.RequestStatusIncomplete
				}
				if sink.events[0].Status != want {
					t.Fatalf("upstream=%s downstream=%s actual_log=%s expected_log=%s error=%s", terminal, format, sink.events[0].Status, want, sink.events[0].ErrorCode)
				}
			})
		}
	}
}
