package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

func TestModelRejectionFailsOverWithoutDisablingOtherModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "model-a" && r.Header.Get("Authorization") == "Bearer sk-one" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"unsupported_model"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()
	engine, registry := newDialectGatewayEngine(t, protocol.OpenAICompletions, "model-a", dialect.NewSet(dialect.NewOpenAI()),
		dialectGatewayGroup{id: 1, name: "model-rejection", upstreamURL: upstream.URL, apiKeys: []string{"sk-one", "sk-two"},
			models: []state.ModelConfig{{ID: "model-a"}, {ID: "model-b"}}},
	)
	for _, model := range []string{"model-a", "model-b"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("model %s response = %d: %s", model, response.Code, response.Body.String())
		}
		wantAttempts := "1"
		if model == "model-a" {
			wantAttempts = "2"
		}
		if response.Header().Get(debugHeaderAttempts) != wantAttempts {
			t.Fatalf("model %s attempts = %s, want %s", model, response.Header().Get(debugHeaderAttempts), wantAttempts)
		}
		if candidates := registry.CollectCredentialCandidates([]uint{1}, nil, time.Now()); len(candidates) != 2 {
			t.Fatalf("model %s left %d available credentials, want both", model, len(candidates))
		}
	}
}

func TestModelRetryPreservesFailedAttemptObservations(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": {"application/json"}},
			Body:          []byte(`{"error":{"code":"unsupported_model"}}`),
			DispatchState: execution.DispatchMaybeSent, RequestWritten: true, ResponseStarted: true,
			ExecutionError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable,
				StatusCode: http.StatusBadRequest, Code: "unsupported_model",
			},
		},
		successfulAffinityResult(),
	}}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	events := sink.snapshot()
	if response.Code != http.StatusOK || len(events) != 1 || len(events[0].Attempts) != 2 {
		t.Fatalf("response/events = %d/%#v, want success with both attempts", response.Code, events)
	}
	first, second := events[0].Attempts[0], events[0].Attempts[1]
	if first.FailureCategory != telemetry.FailureCategoryModelUnavailable || first.ErrorCode == "" ||
		first.Effect != telemetry.EffectNone || !first.WillRetry ||
		second.FailureCategory != telemetry.FailureCategoryOK || second.WillRetry ||
		first.CredentialID == second.CredentialID {
		t.Fatalf("attempts = %#v, want failed unpenalized candidate then successful replacement", events[0].Attempts)
	}
}
