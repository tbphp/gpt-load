package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/subscription/providers/antigravity"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

type antigravityImagesObservations struct {
	events []telemetry.RequestEvent
	table  *pricing.Table
}

func (o *antigravityImagesObservations) Emit(event telemetry.RequestEvent) {
	o.events = append(o.events, event)
}
func (o *antigravityImagesObservations) Load() *pricing.Table { return o.table }

type localAntigravityImagesExecutor struct {
	antigravity.Executor
	baseURL string
}

func (executor localAntigravityImagesExecutor) Execute(ctx context.Context, id string, credential antigravity.Credential, request antigravity.ExecuteRequest) (antigravity.ExecuteResponse, error) {
	request.BaseURL = executor.baseURL
	return executor.Executor.Execute(ctx, id, credential, request)
}

func newAntigravityImagesRuntime(t *testing.T) (*gin.Engine, *Adapter, *antigravityImagesObservations, execution.AttemptSpec) {
	t.Helper()
	canonical, err := antigravity.MarshalCredential(antigravityProviderTestCredential().value)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _, credentials, keyService, row := newSubscriptionAdapterFixture(t, string(channel.Antigravity), canonical, "account")
	ref, ok := credentials.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential is unavailable")
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: row.GroupID, Name: "antigravity", ChannelID: channel.Antigravity, ConnectionType: "subscription",
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gemini-3.1-flash-image", Alias: "public-image"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: row.ID, GroupID: row.GroupID, Status: state.CredentialStatusActive,
			Version: ref.Version, IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint,
		}},
		AccessKeys: []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: keyService.Hash("gl-images-client"), Status: state.AccessKeyStatusActive}},
	}); err != nil {
		t.Fatal(err)
	}
	table, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.Antigravity), ModelID: "gemini-3.1-flash-image"},
		Prices: pricing.Prices{
			Input:     pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true},
			CacheRead: pricing.Price{NanoUSDPerMillion: 500_000, Set: true},
			Output:    pricing.Price{NanoUSDPerMillion: 2_000_000, Set: true},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	observations := &antigravityImagesObservations{table: table}
	handler := gateway.NewHandler(manager, credentials, keyService, gateway.NewExecutionForwarder(adapter),
		dialect.NewSet(dialect.NewOpenAIImages(), dialect.NewGemini()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, observations, observations)
	engine := gin.New()
	routes, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatal(err)
	}
	if err := routes.Bind(engine); err != nil {
		t.Fatal(err)
	}
	spec := validSpec(t, row, keyService)
	spec.ChannelID, spec.Credential.IdentityGeneration = string(channel.Antigravity), ref.IdentityGeneration
	spec.ClientProtocol, spec.Operation = protocol.OpenAIImages, execution.OperationImagesGenerate
	spec.RouteMode, spec.RouteRequirement = execution.RouteConverted, execution.RouteRequirementAny
	spec.ClientModel, spec.UpstreamModel = "public-image", "gemini-3.1-flash-image"
	spec.Path, spec.Header = "/v1/images/generations", http.Header{"Content-Type": {"application/json"}}
	spec.Body = []byte(`{"model":"public-image","prompt":"draw"}`)
	return engine, adapter, observations, spec
}

func TestAntigravityImagesHTTPGenerationReachesGeminiAndUsesExistingPricing(t *testing.T) {
	engine, adapter, observations, _ := newAntigravityImagesRuntime(t)
	var upstreamPayload []byte
	var upstreamPath string
	var upstreamMu sync.Mutex
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamMu.Lock()
		defer upstreamMu.Unlock()
		calls++
		upstreamPath = request.URL.Path
		var err error
		upstreamPayload, err = io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		payload := antigravityImagesResponse(t, `{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":20,"thoughtsTokenCount":5,"totalTokenCount":125}`)
		writer.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(writer, "data: {\"response\":"+string(payload)+"}\n\n"); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	// CPA 会建立自己的隔离 transport；只允许该测试的本地 TLS 服务，禁止访问外部端点。
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != server.Listener.Addr().String() {
			return nil, errors.New("unexpected external test destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport; transport.CloseIdleConnections() })
	adapter.providers[channel.ProviderAntigravity].(*antigravityProviderBridge).executor = localAntigravityImagesExecutor{Executor: antigravity.NewExecutor(), baseURL: server.URL}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"public-image","prompt":"draw","n":1,"response_format":"b64_json"}`))
	request.Header.Set("Authorization", "Bearer gl-images-client")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	upstreamMu.Lock()
	defer upstreamMu.Unlock()
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"b64_json":"`+antigravityTestImage+`"`) {
		t.Fatalf("HTTP response = %d %s", recorder.Code, recorder.Body.String())
	}
	var upstream struct {
		Model       string `json:"model"`
		RequestType string `json:"requestType"`
		Request     struct {
			GenerationConfig struct {
				Modalities []string `json:"responseModalities"`
			} `json:"generationConfig"`
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		} `json:"request"`
	}
	if err := json.Unmarshal(upstreamPayload, &upstream); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || upstreamPath != "/v1internal:streamGenerateContent" || upstream.Model != "gemini-3.1-flash-image" ||
		upstream.RequestType != "image_gen" || len(upstream.Request.Contents) != 1 || len(upstream.Request.Contents[0].Parts) != 1 ||
		upstream.Request.Contents[0].Parts[0].Text != "draw" || !reflect.DeepEqual(upstream.Request.GenerationConfig.Modalities, []string{"TEXT", "IMAGE"}) {
		t.Fatalf("upstream calls/path/payload = %d %s %s", calls, upstreamPath, upstreamPayload)
	}
	if len(observations.events) != 1 {
		t.Fatalf("request events = %d", len(observations.events))
	}
	event := observations.events[0]
	if event.Usage.Result.Tokens != (usage.Tokens{UncachedInput: 60, CacheRead: 40, Output: 25}) ||
		event.Usage.Pricing.CostState != "priced" || event.Usage.Pricing.EstimatedCostNanoUSD != 130 ||
		event.Protocol != protocol.OpenAIImages || event.Operation != execution.OperationImagesGenerate ||
		event.ClientModel != "public-image" || event.UpstreamModel != "gemini-3.1-flash-image" {
		t.Fatalf("request observation = %+v", event)
	}
}

func TestAntigravityImagesAdapterRejectsUnsupportedInputsBeforeDispatch(t *testing.T) {
	_, adapter, _, spec := newAntigravityImagesRuntime(t)
	preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
	adapter.credentials = preparer
	for _, test := range []struct {
		payload string
		stream  bool
		invalid bool
	}{
		{payload: `{"model":"public-image","prompt":"draw","n":2}`},
		{payload: `{"model":"public-image","prompt":"draw","stream":true}`, stream: true},
		{payload: `{"model":"public-image","prompt":"draw"}`, stream: true},
		{payload: `{"model":"public-image","prompt":"draw","size":"1024x1024"}`},
		{payload: `{"model":"public-image","prompt":"draw","n":"2"}`, invalid: true},
		{payload: `{"model":"public-image","n":2}`, invalid: true},
		{payload: `{"model":"public-image","prompt":"draw","stream":"true"}`, stream: true, invalid: true},
	} {
		spec.Body = []byte(test.payload)
		var result execution.AttemptResult
		if test.stream {
			stream := adapter.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
			result.DispatchState, result.Error = stream.DispatchState, stream.Error
		} else {
			result = adapter.Execute(t.Context(), spec)
		}
		if result.DispatchState != execution.DispatchNotSent || result.Error == nil || preparer.calls != 0 {
			t.Fatalf("unsupported input result = %+v, credential preparations = %d", result, preparer.calls)
		}
		wantKind := execution.ErrorKindConversionUnsupported
		if test.invalid {
			wantKind = execution.ErrorKindInvalidRequest
		}
		if result.Error.Kind != wantKind || !test.invalid && result.Error.Code != execution.ErrorCodeTargetConversionNotSupported {
			t.Fatalf("input error = %+v, want %s", result.Error, wantKind)
		}
	}
}

func TestAntigravityImagesHTTPPreservesMissingInvalidUsage(t *testing.T) {
	engine, adapter, observations, _ := newAntigravityImagesRuntime(t)
	upstream := antigravityImagesResponse(t, `{"promptTokenCount":"invalid","candidatesTokenCount":"invalid"}`)
	adapter.providers[channel.ProviderAntigravity].(*antigravityProviderBridge).executor = &recordingAntigravityExecutor{response: upstream}
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"public-image","prompt":"draw"}`))
	request.Header.Set("Authorization", "Bearer gl-images-client")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || len(observations.events) != 1 {
		t.Fatalf("HTTP response = %d %s, events = %d", recorder.Code, recorder.Body.String(), len(observations.events))
	}
	want, err := dialect.NewGemini().ExtractUsage(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if event := observations.events[0]; !reflect.DeepEqual(event.Usage.Result, want) || event.Usage.Pricing.CostState != "unpriced" {
		t.Fatalf("usage observation = %+v, want %+v", event.Usage, want)
	}
}

func TestAntigravityImagesHTTPRejectsUnsupportedRequestsAndInvalidOutput(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    string
		response   string
		wantStatus int
	}{
		{name: "stream", request: `{"model":"public-image","prompt":"draw","stream":true}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "multiple images", request: `{"model":"public-image","prompt":"draw","n":2}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid count", request: `{"model":"public-image","prompt":"draw","n":"2"}`, wantStatus: http.StatusBadRequest},
		{name: "no image output", request: `{"model":"public-image","prompt":"private prompt"}`, response: `{"candidates":[{"content":{"parts":[{"text":"private response"}]}}]}`, wantStatus: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, adapter, observations, _ := newAntigravityImagesRuntime(t)
			fake := &recordingAntigravityExecutor{response: []byte(test.response)}
			adapter.providers[channel.ProviderAntigravity].(*antigravityProviderBridge).executor = fake
			request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(test.request))
			request.Header.Set("Authorization", "Bearer gl-images-client")
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || strings.Contains(recorder.Body.String(), "private") ||
				len(observations.events) != 1 || len(observations.events[0].Attempts) != 1 {
				t.Fatalf("response = %d %s; events = %+v", recorder.Code, recorder.Body.String(), observations.events)
			}
			if test.wantStatus != http.StatusBadGateway && fake.request.Format != "" {
				t.Fatal("unsupported request reached the upstream executor")
			}
			if test.wantStatus == http.StatusUnprocessableEntity && !strings.Contains(recorder.Body.String(), `"code":"protocol_conversion_unsupported"`) {
				t.Fatalf("conversion error = %s", recorder.Body.String())
			}
		})
	}
}
