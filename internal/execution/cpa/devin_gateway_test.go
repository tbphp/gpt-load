package cpa

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protowire"

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

func devinTerminalCredential() []byte {
	return []byte(`{"type":"devin","session_token":"devin-session-token$access","user_id":"account"}`)
}

func newDevinTerminalGateway(t *testing.T, baseURL string) (*gin.Engine, *searchRequestLogSink) {
	t.Helper()
	id, model := channel.Devin, "swe-2"
	adapter, _, registry, keys, row := newSubscriptionAdapterFixture(t, string(id), devinTerminalCredential(), "account\x00")
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

func TestDevinGatewayPreservesStreamTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = "swe-2"
	for _, terminal := range []string{"completed", "incomplete", "missing"} {
		for _, format := range []string{"responses", "chat", "anthropic", "gemini"} {
			t.Run(terminal+"/"+format, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/connect+proto")
					frame := protowire.AppendBytes(protowire.AppendTag(nil, 3, protowire.BytesType), []byte("partial answer"))
					stop := uint64(2)
					if terminal == "incomplete" {
						stop = 3
					}
					frame = protowire.AppendVarint(protowire.AppendTag(frame, 5, protowire.VarintType), stop)
					usage := protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 2)
					usage = protowire.AppendVarint(protowire.AppendTag(usage, 3, protowire.VarintType), 1)
					usage = protowire.AppendVarint(protowire.AppendTag(usage, 5, protowire.VarintType), 3)
					frame = protowire.AppendBytes(protowire.AppendTag(frame, 7, protowire.BytesType), usage)
					_, _ = w.Write(devinGatewayFrame(0, frame))
					if terminal != "missing" {
						_, _ = w.Write(devinGatewayFrame(2, []byte(`{}`)))
					}

				}))
				defer server.Close()
				engine, sink := newDevinTerminalGateway(t, server.URL)
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
				if sink.events[0].FirstResponseMs == nil {
					t.Error("missing upstream binary-frame first response")
				}
				if sink.events[0].Usage.Result.Tokens.CacheRead != 3 || sink.events[0].Usage.Result.Tokens.UncachedInput != 2 || sink.events[0].Usage.Result.Tokens.Output != 1 {
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

func devinGatewayFrame(flag byte, payload []byte) []byte {
	result := make([]byte, 5, 5+len(payload))
	result[0] = flag
	binary.BigEndian.PutUint32(result[1:5], uint32(len(payload)))
	return append(result, payload...)
}
