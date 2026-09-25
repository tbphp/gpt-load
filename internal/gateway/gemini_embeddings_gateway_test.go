package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/testutil/encryptiontest"
	"gpt-load/internal/usage"
)

func geminiEmbeddingsForwardInput(externalModel, upstreamModel string) ForwardInput {
	return ForwardInput{
		Dialect:         dialect.NewGeminiEmbeddings(),
		ClientProtocol:  protocol.GeminiEmbeddings,
		Operation:       execution.OperationEmbeddingsCreate,
		ExternalModel:   externalModel,
		UpstreamModelID: upstreamModel,
	}
}

func TestGeminiEmbeddingsSuccessRepresentationKeepsBodyWithoutCopy(t *testing.T) {
	t.Parallel()

	body := []byte(`{"embeddings":[{"values":[0.12345678901234567,-1,1e-7]}],"usageMetadata":{"promptTokenCount":3}}`)
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		geminiEmbeddingsForwardInput("public-embedding", "provider-embedding"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		body,
		[]string{"sk-secret"},
	)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if !bytes.Equal(prepared.downstream, body) || unsafe.SliceData(prepared.downstream) != unsafe.SliceData(body) {
		t.Fatalf("downstream = %s, want the upstream bytes without copy", prepared.downstream)
	}
}

func TestGeminiEmbeddingsSuccessRepresentationFailsClosedOnCredential(t *testing.T) {
	t.Parallel()

	for _, body := range [][]byte{
		[]byte(`{"embeddings":[{"values":[1]}],"vendor":"sk-secret"}`),
		[]byte(`{"embedding":{"values":[1],"sk-secret":true}}`),
	} {
		if _, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
			geminiEmbeddingsForwardInput("model", "model"),
			http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			body,
			[]string{"sk-secret"},
		); err == nil {
			t.Fatalf("prepareSuccessRepresentation(%s) error = nil, want fail closed", body)
		}
	}
}

func TestGeminiEmbeddingsSuccessRepresentationSkipsNumericVectorsInCredentialScan(t *testing.T) {
	t.Parallel()

	body := []byte(`{"embeddings":[{"values":[0.1234,12]}]}`)
	prepared, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
		geminiEmbeddingsForwardInput("model", "model"),
		http.StatusOK,
		http.Header{"Content-Type": {"application/json"}},
		body,
		[]string{"12"},
	)
	if err != nil || !bytes.Equal(prepared.downstream, body) {
		t.Fatalf("prepareSuccessRepresentation() = %s, %v; want numeric vectors ignored", prepared.downstream, err)
	}
	for _, invalid := range [][]byte{[]byte(`{"embeddings":[`), []byte(`[]`)} {
		if _, err := (&responseProcessor{redactor: redact.New()}).prepareSuccessRepresentation(
			geminiEmbeddingsForwardInput("model", "model"),
			http.StatusOK,
			http.Header{"Content-Type": {"application/json"}},
			invalid,
			[]string{"sk-secret"},
		); err == nil {
			t.Fatalf("prepareSuccessRepresentation(%s) error = nil, want fail closed", invalid)
		}
	}
}

func TestGeminiEmbeddingsExecutionBoundaryMovesBodyOwnership(t *testing.T) {
	t.Parallel()

	body := []byte(`{"embedding":{"values":[1]}}`)
	upstream := upstreamFromExecutionResult(
		context.Background(),
		ForwardInput{ClientProtocol: protocol.GeminiEmbeddings},
		execution.AttemptResult{
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			StatusCode:      http.StatusOK,
			Body:            body,
		},
	)
	if unsafe.SliceData(upstream.Body) != unsafe.SliceData(body) {
		t.Fatal("upstreamFromExecutionResult copied the owned Gemini Embeddings body")
	}
}

func TestExecutionForwarderCapturesGeminiEmbeddingsUsageFromUnaryBody(t *testing.T) {
	t.Parallel()

	responseBody := []byte(`{"embeddings":[{"values":[0.1,0.2]}],"usageMetadata":{"promptTokenCount":7,"totalTokenCount":7}}`)
	executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
		return execution.AttemptResult{
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			StatusCode:      http.StatusOK,
			Header:          http.Header{"Content-Type": {"application/json"}},
			Body:            responseBody,
			Model:           "upstream-embedding",
		}
	}}
	input := executionForwardInput()
	input.Dialect = dialect.NewGeminiEmbeddings()
	input.ClientProtocol = protocol.GeminiEmbeddings
	input.ObserveUsage = true
	input.Operation = execution.OperationEmbeddingsCreate
	input.RouteRequirement = execution.RouteRequirementNative
	input.ExternalModel = "upstream-embedding"
	input.UpstreamModelID = "upstream-embedding"
	input.Request.Path = "/v1beta/models/upstream-embedding:batchEmbedContents"
	input.Request.Body = []byte(`{"requests":[{"content":{"parts":[{"text":"hello"}]}}]}`)

	result := NewExecutionForwarder(executor).Forward(context.Background(), input)
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 7}}
	if result.Err != nil || result.Usage != want || result.ClassificationBody != nil ||
		unsafe.SliceData(result.Body) != unsafe.SliceData(responseBody) {
		t.Fatalf("Forward() usage = %#v, want %#v; result=%#v", result.Usage, want, result)
	}
}

func TestVisibleGeminiModelIDsIncludesGeminiEmbeddingsRoutes(t *testing.T) {
	t.Parallel()

	snapshot := &state.ConfigSnapshot{ExecutionCandidates: state.ExecutionCandidateIndex{
		protocol.Gemini: {
			execution.OperationChatCompletion: {"gemini-chat": {{GroupID: 1}}},
		},
		protocol.GeminiEmbeddings: {
			execution.OperationEmbeddingsCreate: {"gemini-embedding-001": {{GroupID: 2}}},
		},
	}}
	for _, test := range []struct {
		name      string
		protocols map[protocol.Protocol]struct{}
		want      []string
	}{
		{name: "unrestricted union", want: []string{"gemini-chat", "gemini-embedding-001"}},
		{name: "embedding protocol filter", protocols: map[protocol.Protocol]struct{}{protocol.GeminiEmbeddings: {}}, want: []string{"gemini-embedding-001"}},
		{name: "chat protocol filter", protocols: map[protocol.Protocol]struct{}{protocol.Gemini: {}}, want: []string{"gemini-chat"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			accessKey := state.AccessKeyView{Filters: state.FilterSet{Protocols: test.protocols}}
			if got := visibleModelIDs(snapshot, accessKey, protocol.Gemini); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("visibleModelIDs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestGeminiEmbeddingsRequestLogFreezesProviderInputEcho(t *testing.T) {
	t.Parallel()

	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(
		sink,
		"req-gemini-embeddings-error",
		time.Unix(100, 0),
		9,
		protocol.GeminiEmbeddings,
		func() time.Time { return time.Unix(101, 0) },
	)
	recorder.setOperation(execution.OperationEmbeddingsCreate)
	providerSummary := "invalid content=input-sentinel"
	index := recorder.appendAttempt(
		requestLogSelection(12, 22, "gemini-embeddings"),
		UpstreamResult{StatusCode: http.StatusBadRequest},
		telemetry.FailureCategoryClientError,
		telemetry.ActionTerminate,
		"upstream_client_error",
		providerSummary,
		time.Unix(100, 0),
		time.Unix(100, 0),
	)
	recorder.completeResponse(
		UpstreamResult{StatusCode: http.StatusBadRequest, ErrorSummary: providerSummary},
		health.Decision{Category: health.FailureCategoryClientError},
		"provider-embedding",
		index,
	)
	recorder.emit()

	if len(sink.events) != 1 {
		t.Fatalf("events = %#v", sink.events)
	}
	encoded, _ := json.Marshal(sink.events[0])
	if bytes.Contains(encoded, []byte("input-sentinel")) {
		t.Fatalf("Gemini Embeddings request log retained provider input echo: %s", encoded)
	}
}

func TestHandlerRoutesGeminiEmbeddingsWithAliasUsageAndPricing(t *testing.T) {
	t.Parallel()

	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       []byte(`{"embeddings":[{"values":[0.1,0.2]}]}`),
		Usage: usage.Result{
			State:  usage.StateComplete,
			Tokens: usage.Tokens{UncachedInput: 1_000_000},
		},
		RequestWritten: true,
	}}}
	sink := &recordingRequestLogSink{}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
	)
	keyService := encryptiontest.Service(t, "handler-test-master-key")
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "gemini", ChannelID: channel.Gemini,
			Params: json.RawMessage(`{}`), Enabled: true,
			Models: []state.ModelConfig{{ID: "gemini-embedding-001", Alias: "public-embedding"}},
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler.dialects = dialect.NewSet(dialect.NewGeminiEmbeddings())
	table, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.Gemini), ModelID: "gemini-embedding-001"},
		Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 2_000_000_000, Set: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler.priceTables = &mutableGatewayPriceTableProvider{table: table}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/public-embedding:batchEmbedContents",
		strings.NewReader(`{"requests":[{"model":"models/public-embedding","content":{"parts":[{"text":"input-sentinel"}]}}]}`),
	)
	request.Header.Set("X-Goog-Api-Key", "gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("response = %d %s, forwarded = %d", response.Code, response.Body.String(), len(forwarder.inputs))
	}
	input := forwarder.inputs[0]
	if input.ClientProtocol != protocol.GeminiEmbeddings || input.Operation != execution.OperationEmbeddingsCreate ||
		input.ExternalModel != "public-embedding" || input.UpstreamModelID != "gemini-embedding-001" {
		t.Fatalf("forward input = protocol %q operation %q model %q -> %q",
			input.ClientProtocol, input.Operation, input.ExternalModel, input.UpstreamModelID)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	wantPricing := telemetry.PricingObservation{
		UpstreamModel:        "gemini-embedding-001",
		CostState:            string(pricing.CostStatePriced),
		PricingCompleteness:  string(pricing.CompletenessComplete),
		EstimatedCostNanoUSD: 2_000_000_000,
	}
	if event.Protocol != protocol.GeminiEmbeddings || event.Operation != execution.OperationEmbeddingsCreate ||
		withoutPricingReceipt(event.Usage.Pricing) != wantPricing {
		t.Fatalf("Gemini Embeddings event = %#v", event)
	}
	if encoded, _ := json.Marshal(event); bytes.Contains(encoded, []byte("input-sentinel")) {
		t.Fatalf("request log retained input text: %s", encoded)
	}
}
