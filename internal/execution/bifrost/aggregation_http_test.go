package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/testutil/encryptiontest"
	"gpt-load/internal/usage"
)

var aggregationTestChannels = []channel.ID{channel.CLIProxyAPI, channel.NewAPI, channel.GPTLoad}

func newAggregationManagerForTest(t *testing.T, id channel.ID, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()
	switch id {
	case channel.CLIProxyAPI:
		return newCLIProxyAPIManagerForTest(t, baseURL)
	case channel.NewAPI:
		return newNewAPIManagerForTest(t, baseURL)
	case channel.GPTLoad:
		return newGPTLoadManagerForTest(t, baseURL)
	default:
		t.Fatalf("unexpected aggregation channel %q", id)
		return nil, channel.ResolvedTarget{}
	}
}

func TestAggregationSearchCapabilityAndRequestShape(t *testing.T) {
	manager := &RuntimeManager{}
	route := channel.RouteDescriptor{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationWebSearch, RouteMode: execution.RouteNative}
	if err := manager.ValidateRouteCapability(channel.ProviderMultiProtocolGateway, route); err != nil {
		t.Fatalf("search capability: %v", err)
	}
	for _, id := range aggregationTestChannels {
		descriptor, _ := channel.NewRegistry().Get(id)
		for _, declared := range descriptor.Routes {
			if err := manager.ValidateRouteCapability(channel.ProviderMultiProtocolGateway, declared); err != nil {
				t.Errorf("%s %s/%s: %v", id, declared.ClientProtocol, declared.Operation, err)
			}
		}
	}
	for _, provider := range []channel.ProviderKind{channel.ProviderOpenAI, channel.ProviderOpenAICompatible} {
		if err := manager.ValidateRouteCapability(provider, route); err == nil {
			t.Errorf("search unexpectedly supported by %s", provider)
		}
	}
	for _, test := range []struct {
		method, path string
		stream, want bool
	}{
		{http.MethodPost, "/v1/alpha/search", false, true},
		{http.MethodPost, "/v1/alpha/search", true, false},
		{http.MethodGet, "/v1/alpha/search", false, false},
		{http.MethodPost, "/v1/responses", false, false},
	} {
		spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationWebSearch, RouteMode: execution.RouteNative, Method: test.method, Path: test.path}
		if got := supportedRequestShape(spec, test.stream); got != test.want {
			t.Errorf("%s %s stream=%v: supported=%v", test.method, test.path, test.stream, got)
		}
	}
}

type aggregationSearchLogSink struct {
	events []telemetry.RequestEvent
}

func (sink *aggregationSearchLogSink) Emit(event telemetry.RequestEvent) {
	sink.events = append(sink.events, event)
}

func TestAggregationSearchGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestBody = `{"id":"search-session","model":"public","commands":{"search_query":[{"q":"example"}]},"settings":{"external_web_access":true},"extension":{"number":1.2300}}`
	const successBody = `{"model":"upstream-metadata","output":"search result","encrypted_output":"opaque","extension":{"number":1.2300},"usage":{"input_tokens":123}}`
	const errorBody = `{"error":{"message":"search unavailable","search_detail":"preserved"}}`
	for _, id := range aggregationTestChannels {
		t.Run(string(id), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/team-a/v1/alpha/search" || r.Header.Get("Authorization") != "Bearer "+testAPIKey {
					t.Errorf("unexpected upstream target or credential: %s %s", r.Method, r.URL.Path)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
					return
				}
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				if string(payload["model"]) != `"account1/gpt-6.1-sol"` || string(payload["id"]) != `"search-session"` ||
					string(payload["commands"]) != `{"search_query":[{"q":"example"}]}` ||
					string(payload["settings"]) != `{"external_web_access":true}` ||
					string(payload["extension"]) != `{"number":1.2300}` || payload["temperature"] != nil || payload["stream"] != nil {
					t.Errorf("search request changed: %s", body)
				}
				status, err := strconv.Atoi(r.URL.Query().Get("status"))
				if err != nil {
					t.Errorf("decode status: %v", err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(status)
				bodyOut := successBody
				if status >= 400 {
					bodyOut = errorBody
				}
				_, _ = io.WriteString(w, bodyOut)
			}))
			defer server.Close()
			engine, manager, input, sink, stats := newAggregationSearchGateway(t, id, server.URL+"/team-a")
			for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					before := calls.Load()
					request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search?status="+strconv.Itoa(status), strings.NewReader(requestBody))
					request.Header.Set("Authorization", "Bearer gl-search-client")
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					wantBody := successBody
					if status >= 400 {
						wantBody = errorBody
					}
					if response.Code != status || response.Body.String() != wantBody || calls.Load() != before+1 {
						t.Fatalf("response=%d %s, upstream calls=%d", response.Code, response.Body.String(), calls.Load()-before)
					}
					if status == http.StatusTooManyRequests && response.Header().Get("Retry-After") != "17" {
						t.Fatalf("Retry-After=%q", response.Header().Get("Retry-After"))
					}
					event := sink.events[len(sink.events)-1]
					if event.Operation != execution.OperationWebSearch || len(event.Attempts) != 1 || event.Usage.Result.State != usage.StateNotApplicable ||
						event.Usage.Pricing.CostState != string(pricing.CostStateNotApplicable) {
						t.Fatalf("search log=%+v", event)
					}
					if got := stats.Snapshot(1, time.Now()); got != (health.CredentialStats{}) {
						t.Fatalf("search affected model health statistics: %+v", got)
					}
				})
			}
			for _, test := range []struct {
				name, body, token string
				status            int
			}{
				{"authentication", requestBody, "", http.StatusUnauthorized},
				{"stream", `{"id":"search-session","model":"public","stream":true}`, "gl-search-client", http.StatusBadRequest},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := calls.Load()
					request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(test.body))
					request.Header.Set("Authorization", "Bearer "+test.token)
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					if response.Code != test.status || calls.Load() != before {
						t.Fatalf("response=%d %s, upstream calls=%d", response.Code, response.Body.String(), calls.Load()-before)
					}
				})
			}
			input.AccessKeys[0].Filters = state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}}}
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			before := calls.Load()
			request := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(requestBody))
			request.Header.Set("Authorization", "Bearer gl-search-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || calls.Load() != before {
				t.Fatalf("protocol filter response=%d %s, upstream calls=%d", response.Code, response.Body.String(), calls.Load()-before)
			}
		})
	}
}

func newAggregationSearchGateway(t *testing.T, id channel.ID, endpoint string) (*gin.Engine, *state.Manager, state.CompileInput, *aggregationSearchLogSink, *health.StatsStore) {
	t.Helper()
	runtime, target := newAggregationManagerForTest(t, id, endpoint)
	crypto := encryptiontest.Service(t, "aggregation-search-fixture")
	credential := `{"api_key":"` + testAPIKey + `"}`
	encrypted, err := crypto.Encrypt(credential)
	if err != nil {
		t.Fatal(err)
	}
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{ID: 1, Name: "aggregation", ChannelID: id, ConnectionType: "api_key", Params: target.TargetConfig, Enabled: true,
			Models:   []state.ModelConfig{{ID: "account1/gpt-6.1-sol", Alias: "public"}},
			Settings: config.Settings{state.SettingParameterOverrides: []any{map[string]any{"set": map[string]any{"temperature": 0.25}}}},
		}},
		Credentials: []state.CredentialConfig{{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: crypto.Hash(credential), Status: state.CredentialStatusActive}},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: crypto.Hash("gl-search-client"), Status: state.AccessKeyStatusActive}},
	}
	manager := state.NewManager()
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Fingerprint: crypto.Hash(credential), Status: state.CredentialStatusActive, AuthState: state.CredentialAuthStateReady, EncryptedValue: encrypted,
	}}); err != nil {
		t.Fatal(err)
	}
	sink, stats := &aggregationSearchLogSink{}, health.NewStatsStore()
	handler := gateway.NewHandler(manager, registry, crypto, gateway.NewExecutionForwarder(runtime), dialect.NewSet(dialect.NewOpenAIResponses()),
		stats, health.NewMutationCoordinator(), nil, sink, nil)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	return engine, manager, input, sink, stats
}

func TestAggregationCountTokensUsesNativeGatewayWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		clientProtocol   protocol.Protocol
		path             string
		body             string
		wantPath         string
		credentialHeader string
		response         string
	}{
		{
			name: "anthropic", clientProtocol: protocol.Anthropic,
			path: "/v1/messages/count_tokens",
			body: `{"model":"client-model","messages":[{"role":"user","content":"hello"}],` +
				`"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1/messages/count_tokens", credentialHeader: "X-Api-Key",
			response: `{"input_tokens":7}`,
		},
		{
			name: "gemini", clientProtocol: protocol.Gemini,
			path: "/v1beta/models/client-model:countTokens",
			body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],` +
				`"provider":"client","api_key":"client-body"}`,
			wantPath: "/team-a/v1beta/models/upstream-model:countTokens", credentialHeader: "X-Goog-Api-Key",
			response: `{"totalTokens":7}`,
		},
	}

	for _, channelID := range aggregationTestChannels {
		for _, test := range tests {
			t.Run(string(channelID)+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodPost || request.URL.Path != test.wantPath ||
						request.URL.Query().Get("trace") != "kept" || request.URL.Query().Get("key") != "" {
						t.Errorf("upstream target = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
					}
					if request.Header.Get(test.credentialHeader) != testAPIKey {
						t.Errorf("credential %s = %q", test.credentialHeader, request.Header.Get(test.credentialHeader))
					}
					for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
						if name != test.credentialHeader && request.Header.Get(name) != "" {
							t.Errorf("unexpected credential header %s = %q", name, request.Header.Get(name))
						}
					}
					body, _ := io.ReadAll(request.Body)
					var payload map[string]any
					if err := json.Unmarshal(body, &payload); err != nil {
						t.Errorf("decode upstream request: %v", err)
						return
					}
					if payload["provider"] != nil || payload["api_key"] != nil {
						t.Errorf("control fields reached upstream: %#v", payload)
					}
					if test.clientProtocol == protocol.Anthropic && payload["model"] != "upstream-model" {
						t.Errorf("upstream model = %#v", payload["model"])
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, test.response)
				}))
				defer server.Close()

				manager, resolved := newAggregationManagerForTest(t, channelID, server.URL+"/team-a")
				spec := freezeTestAttempt(execution.AttemptSpec{
					RequestID: "cpa-count-tokens", AttemptID: "cpa-count-tokens-attempt", Sequence: 1,
					ChannelID: string(channelID), ClientProtocol: test.clientProtocol,
					Operation:   execution.OperationCountTokens,
					ClientModel: "client-model", UpstreamModel: "upstream-model",
					Method: http.MethodPost, Path: test.path,
					RawQuery: "trace=kept&key=client-query",
					Header: http.Header{
						"Authorization":  {"Bearer client-auth"},
						"X-Api-Key":      {"client-auth"},
						"X-Goog-Api-Key": {"client-auth"},
						"Content-Type":   {"application/json"},
					},
					Body: []byte(test.body), TargetConfig: resolved.TargetConfig,
					Credential: execution.NewCredentialSnapshot(
						17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
					),
				})
				result := manager.Execute(context.Background(), spec)
				if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
					result.UpstreamProtocol != test.clientProtocol || !bytes.Equal(result.Body, []byte(test.response)) {
					t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
				}
			})
		}
	}
}

func TestAggregationResponsesResourcesUseGatewayRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation execution.Operation
		method    string
		path      string
		body      string
		wantModel string
	}{
		{
			name: "retrieve", operation: execution.OperationResponsesRetrieve,
			method: http.MethodGet, path: "/v1/responses/resp_123",
		},
		{
			name: "delete", operation: execution.OperationResponsesDelete,
			method: http.MethodDelete, path: "/v1/responses/resp_123",
		},
		{
			name: "cancel", operation: execution.OperationResponsesCancel,
			method: http.MethodPost, path: "/v1/responses/resp_123/cancel",
		},
		{
			name: "input items", operation: execution.OperationResponsesInputItems,
			method: http.MethodGet, path: "/v1/responses/resp_123/input_items",
		},
		{
			name: "input tokens", operation: execution.OperationResponsesInputTokens,
			method: http.MethodPost, path: "/v1/responses/input_tokens",
			body:      `{"model":"client-model","input":[],"provider":"client","api_key":"client-body"}`,
			wantModel: "upstream-model",
		},
		{
			name: "vendor passthrough", operation: execution.OperationResponsesPassthrough,
			method: http.MethodPatch, path: "/v1/responses/vendor-extension/nested",
			body: `{"vendor":true,"provider":"client","api_key":"client-body"}`,
		},
	}

	for _, channelID := range aggregationTestChannels {
		for _, test := range tests {
			t.Run(string(channelID)+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				const responseBody = `{"object":"gpt_load.response"}`
				clientModel, upstreamModel := "client-model", "upstream-model"
				if test.wantModel == "" {
					clientModel, upstreamModel = "", ""
				}
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if request.Method != test.method || request.URL.Path != "/gateway"+test.path {
						t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
					}
					if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
						t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
					}
					if test.body != "" {
						body, _ := io.ReadAll(request.Body)
						var payload map[string]any
						if err := json.Unmarshal(body, &payload); err != nil {
							t.Errorf("decode upstream request: %v", err)
							return
						}
						if payload["provider"] != nil || payload["api_key"] != nil {
							t.Errorf("control fields reached upstream: %#v", payload)
						}
						if test.wantModel != "" && payload["model"] != test.wantModel {
							t.Errorf("upstream model = %#v", payload["model"])
						}
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, responseBody)
				}))
				defer server.Close()

				manager, resolved := newAggregationManagerForTest(t, channelID, server.URL+"/gateway")
				spec := freezeTestAttempt(execution.AttemptSpec{
					RequestID: "gpt-load-responses", AttemptID: "gpt-load-responses-attempt", Sequence: 1,
					ChannelID: string(channelID), ClientProtocol: protocol.OpenAIResponses,
					Operation: test.operation, RouteRequirement: execution.RouteRequirementNative,
					ClientModel: clientModel, UpstreamModel: upstreamModel,
					Method: test.method, Path: test.path,
					Header:       http.Header{"Authorization": {"Bearer client-auth"}, "Content-Type": {"application/json"}},
					Body:         []byte(test.body),
					TargetConfig: resolved.TargetConfig,
					Credential: execution.NewCredentialSnapshot(
						17, 1, 1, []byte(`{"api_key":"`+testAPIKey+`"}`),
					),
				})
				result := manager.Execute(context.Background(), spec)
				if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
					result.UpstreamProtocol != protocol.OpenAIResponses || !bytes.Equal(result.Body, []byte(responseBody)) {
					t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
				}
			})
		}
	}
}
