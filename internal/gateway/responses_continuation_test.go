package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/app"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/state"
)

func TestResponsesContinuationPinsCredentialWithoutSoftAffinity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{
				storedResponse("first"), storedResponse("second"), storedResponse("third"),
			}}
			handler, engine, sink := newContinuationFixture(t, forwarder)
			group := handler.manager.Current().Groups[1]
			group.AffinityEnabled = enabled
			handler.manager.Current().Groups[1] = group
			handler.manager.Current().Settings.AffinityEnabled = enabled

			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial","store":true}`, http.StatusOK)
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"continue","previous_response_id":"first"}`, http.StatusOK)
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"continue"}`, http.StatusOK)

			assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one", "sk-two"})
			assertAffinityHits(t, sink.snapshot(), []bool{false, true, false})
			if got := handler.registry.SchedulingState().CaptureCheckpoint().Sequence; got != 3 {
				t.Fatalf("scheduling allocations = %d, want 3", got)
			}
		})
	}
}

func TestResponsesContinuationRejectsUnknownAndOtherAccessKeyIDs(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("first")}}
	handler, engine, sink := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	other := handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-client")]
	other.ID = 2
	handler.manager.Current().AccessKeysByHash[handler.encryption.Hash("gl-other")] = other
	handler.manager.Current().AccessKeysByID[other.ID] = other
	for _, request := range []struct{ key, id string }{{"gl-other", "first"}, {"gl-client", "unknown"}} {
		serveContinuation(t, engine, request.key, fmt.Sprintf(`{"model":"gpt-4o","input":"continue","previous_response_id":%q}`, request.id), http.StatusBadRequest)
	}
	if len(forwarder.inputs) != 1 {
		t.Fatalf("upstream attempts = %d, want only the initial request", len(forwarder.inputs))
	}
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, false})
}

func TestResponsesContinuationUsesCurrentCandidateIntersection(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("first")}}
	handler, engine, sink := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	if err := handler.registry.(*state.CredentialRegistry).SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
		t.Fatal(err)
	}
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"continue","previous_response_id":"first"}`, http.StatusServiceUnavailable)
	if len(forwarder.inputs) != 1 {
		t.Fatal("continuation escaped to the other credential")
	}
	assertAffinityHits(t, sink.snapshot(), []bool{false, false})
}

func TestResponsesContinuationRegistersSSEBeforeDelivery(t *testing.T) {
	var engine *gin.Engine
	writer := httptest.NewRecorder()
	var credentials []uint
	created := "event: response.created\r\n" + `data: {"type":"response.created","response":{"id":"first","object":"response","store":true}}` + "\r\n\r\n"
	completed := "event: response.completed\r\n" + `data: {"type":"response.completed","response":{"id":"first","object":"response","store":true}}` + "\r\n\r\n"
	executor := fakeExecutionExecutor{
		unary: func(_ context.Context, spec execution.AttemptSpec) execution.AttemptResult {
			credentials = append(credentials, spec.Credential.ID)
			return execution.AttemptResult{
				StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
				Header: http.Header{"Content-Type": {"application/json"}}, Body: storedResponse("second").Body,
			}
		},
		stream: func(_ context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
			credentials = append(credentials, spec.Credential.ID)
			for _, event := range []execution.StreamEvent{
				{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}},
				{Sequence: 2, Kind: execution.StreamEventData, Data: []byte(created[:len(created)-4])},
			} {
				if err := sink(event); err != nil {
					t.Fatal(err)
				}
			}
			if writer.Body.Len() != 0 {
				t.Fatal("partial response ID was delivered before registration")
			}
			if err := sink(execution.StreamEvent{Sequence: 3, Kind: execution.StreamEventData, Data: []byte(created[len(created)-4:])}); err != nil {
				t.Fatal(err)
			}
			if writer.Body.String() != created {
				t.Fatalf("created event was not delivered intact: %q", writer.Body.String())
			}
			// 客户端收到 created 即可续接，无需等待原请求 completed。
			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusOK)
			if err := sink(execution.StreamEvent{Sequence: 4, Kind: execution.StreamEventData, Data: []byte(completed)}); err != nil {
				t.Fatal(err)
			}
			return execution.StreamResult{StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true}
		},
	}
	_, engine, _ = newContinuationFixture(t, NewExecutionForwarder(executor))
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-4o","input":"initial","stream":true}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(writer, request)
	if writer.Body.String() != created+completed || fmt.Sprint(credentials) != "[1 1]" {
		t.Fatalf("SSE response = %q, credentials = %v", writer.Body.String(), credentials)
	}
}

func TestResponsesContinuationKeepsBindingAcrossModelCooldownAndConfigPublication(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("first"), storedResponse("second")}}
	handler, engine, sink := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	ref, _ := handler.registry.CredentialRef(1)
	registry := handler.registry.(*state.CredentialRegistry)
	if ok, _ := registry.SetModelCooldown(ref, "gpt-4o", time.Now().Add(time.Hour), time.Now()); !ok {
		t.Fatal("set model cooldown failed")
	}
	response := serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusTooManyRequests)
	if response.Header().Get("Retry-After") == "" || len(forwarder.inputs) != 1 {
		t.Fatal("cooldown did not reuse existing candidate rejection")
	}
	registry.ClearModelCooldowns(1)
	handler.manager.Current().Revision++
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusOK)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true})
}

func TestResponsesContinuationFailureExhaustsBoundCandidate(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		storedResponse("first"),
		{StatusCode: http.StatusServiceUnavailable, Body: []byte(`{"error":{"message":"upstream unavailable"}}`)},
		storedResponse("must-not-retry"),
	}}
	handler, engine, sink := newContinuationFixture(t, forwarder)
	handler.manager.Current().Settings.RetryCount = 3
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	response := serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusServiceUnavailable)
	if response.Body.String() != string(forwarder.results[1].Body) {
		t.Fatal("the original upstream error was replaced")
	}
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	events := sink.snapshot()
	if len(events[1].Attempts) != 1 || events[1].Attempts[0].WillRetry || !events[1].AffinityHit {
		t.Fatalf("failure telemetry = %#v", events[1])
	}
}

func TestResponsesContinuationRejectsChangedCredentialIdentity(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("first")}}
	handler, engine, _ := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	ref, _ := handler.registry.CredentialRef(1)
	registry := handler.registry.(*state.CredentialRegistry)
	if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{{
		ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: ref.Version + 1,
		IdentityGeneration: ref.IdentityGeneration + 10, Fingerprint: ref.Fingerprint, EncryptedValue: ref.EncryptedValue,
	}}); err != nil {
		t.Fatal(err)
	}
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusServiceUnavailable)
	if len(forwarder.inputs) != 1 {
		t.Fatal("old ID inherited a replacement identity")
	}
}

func TestResponsesContinuationDoesNotLearnUnstoredOrUnrelatedResponses(t *testing.T) {
	for _, test := range []struct{ request, response string }{
		{`{"model":"gpt-4o","input":"initial","store":false}`, string(storedResponse("first").Body)},
		{`{"model":"gpt-4o","input":"initial"}`, `{"id":"first","object":"response","store":false}`},
		{`{"model":"gpt-4o","input":"initial"}`, `{"id":"first","object":"chat.completion"}`},
	} {
		forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: http.StatusOK, Body: []byte(test.response)}}}
		_, engine, _ := newContinuationFixture(t, forwarder)
		serveContinuation(t, engine, "gl-client", test.request, http.StatusOK)
		serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusBadRequest)
		if len(forwarder.inputs) != 1 {
			t.Fatal("unavailable response ID reached an upstream")
		}
	}
}

func TestResponsesContinuationRejectsConflictingResponseBeforeDelivery(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		storedResponse("shared-id"), storedResponse("shared-id"), storedResponse("continued"),
	}}
	_, engine, _ := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"first"}`, http.StatusOK)
	response := serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"independent"}`, http.StatusBadGateway)
	if bytes.Contains(response.Body.Bytes(), []byte("shared-id")) {
		t.Fatal("conflicting response ID was exposed")
	}
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"shared-id","input":"continue"}`, http.StatusOK)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two", "sk-one"})
}

func TestResponsesContinuationRejectsLegacyOverrideBeforeForward(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("first")}}
	handler, engine, _ := newContinuationFixture(t, forwarder)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	rules, err := parameteroverride.Compile([]any{map[string]any{"set": map[string]any{"previous_response_id": "wrong"}}})
	if err != nil {
		t.Fatal(err)
	}
	group := handler.manager.Current().Groups[1]
	group.ParameterOverrides = rules
	handler.manager.Current().Groups[1] = group
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"first","input":"continue"}`, http.StatusServiceUnavailable)
	if len(forwarder.inputs) != 1 {
		t.Fatal("legacy override dispatched a changed continuation")
	}
}

func TestResponsesContinuationResumesFromRuntimeCheckpoint(t *testing.T) {
	before, engine, _ := newContinuationFixture(t, &scriptedForwarder{results: []UpstreamResult{storedResponse("before-restart")}})
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	dir := t.TempDir()
	checkpoint := app.NewFileRuntimeStateCheckpoint(dir, nil, nil, before.responseBindings)
	if err := checkpoint.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	forwarder := &scriptedForwarder{results: []UpstreamResult{storedResponse("new-root"), storedResponse("continued")}}
	after, restarted, _ := newContinuationFixture(t, forwarder)
	if err := app.NewFileRuntimeStateCheckpoint(dir, nil, nil, after.responseBindings).Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	serveContinuation(t, restarted, "gl-client", `{"model":"gpt-4o","input":"another root"}`, http.StatusOK)
	serveContinuation(t, restarted, "gl-client", `{"model":"gpt-4o","previous_response_id":"before-restart","input":"continue"}`, http.StatusOK)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
}

func TestResponsesContinuationLearnsCompressedJSONResponse(t *testing.T) {
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	if _, err := zipper.Write(storedResponse("compressed-response").Body); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	var credentials []uint
	executor := fakeExecutionExecutor{unary: func(_ context.Context, spec execution.AttemptSpec) execution.AttemptResult {
		credentials = append(credentials, spec.Credential.ID)
		return execution.AttemptResult{
			StatusCode: http.StatusOK, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
			Body:   compressed.Bytes(),
		}
	}}
	_, engine, _ := newContinuationFixture(t, NewExecutionForwarder(executor))
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial"}`, http.StatusOK)
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","previous_response_id":"compressed-response","input":"continue"}`, http.StatusOK)
	if fmt.Sprint(credentials) != "[1 1]" {
		t.Fatalf("compressed response resumed through credentials %v", credentials)
	}
}

func storedResponse(id string) UpstreamResult {
	return UpstreamResult{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(fmt.Sprintf(`{"id":%q,"object":"response","status":"completed","store":true,"output":[]}`, id)),
	}
}

func newContinuationFixture(t *testing.T, forwarder AttemptForwarder) (*Handler, *gin.Engine, *recordingRequestLogSink) {
	t.Helper()
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	handler.dialects = dialect.NewSet(dialect.NewOpenAIResponses())
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return handler, engine, sink
}

func serveContinuation(t *testing.T, engine http.Handler, key, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	return response
}
