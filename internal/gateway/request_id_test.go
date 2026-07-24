package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
)

func TestHandlerGeneratesCanonicalRequestIDAndRejectsSpoofing(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header: http.Header{
			requestIDHeader: {"upstream-spoof"},
		},
		Body:           []byte(`{"ok":true}`),
		RequestWritten: true,
	}}}
	limiter := &recordingAccessKeyRPMLimiter{
		decisions: []ratelimit.LimitDecision{{Allowed: true}},
	}
	sink := &recordingRequestLogSink{}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, limiter, sink, "sk-one",
	)
	handler.requestNow = newSteppingRequestClock()

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set(requestIDHeader, "client-spoof")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	requestID := recorder.Header().Get(requestIDHeader)
	canonicalUUIDv4 := regexp.MustCompile(
		`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
	)
	if !canonicalUUIDv4.MatchString(requestID) {
		t.Fatalf("%s = %q, want canonical lowercase UUID v4", requestIDHeader, requestID)
	}
	if requestID == "client-spoof" || requestID == "upstream-spoof" {
		t.Fatalf("%s trusted a spoofed value: %q", requestIDHeader, requestID)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].RequestID != requestID {
		t.Fatalf("events = %#v, want one event with response request ID %q", events, requestID)
	}
}

func TestHandlerRequestIDGenerationFailurePreservesDataPlaneAndSkipsEmit(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       []byte(`{"ok":true}`),
	}}}
	limiter := &recordingAccessKeyRPMLimiter{
		decisions: []ratelimit.LimitDecision{{Allowed: true}},
	}
	sink := &recordingRequestLogSink{}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, limiter, sink, "sk-one",
	)
	handler.newRequestID = func() (string, error) {
		return "", errors.New("entropy unavailable")
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("response = %d %s, want unchanged upstream success", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get(requestIDHeader); got != "" {
		t.Fatalf("%s = %q, want empty after generation failure", requestIDHeader, got)
	}
	if len(limiter.snapshot()) != 1 || len(forwarder.inputs) != 1 {
		t.Fatalf(
			"limiter/forward calls = %d/%d, want 1/1",
			len(limiter.snapshot()), len(forwarder.inputs),
		)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("events = %#v, want no emit without request ID", events)
	}
}

func TestNewUpstreamRequestRemovesReservedRequestIDAfterHeaderRules(t *testing.T) {
	openAI := dialect.NewOpenAI(http.DefaultClient)
	input := ForwardInput{
		Dialect: openAI,
		Group: state.GroupView{
			UpstreamURL: "https://upstream.example.com",
			HeaderRules: state.HeaderRules{
				Set: map[string]string{requestIDHeader: "group-spoof-${API_KEY}"},
			},
		},
		APIKey: "opaque-provider-secret",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: http.Header{requestIDHeader: {"client-spoof"}},
			Body:   []byte(`{"model":"gpt-4o"}`),
		},
	}

	request, _, replay, err := newUpstreamRequest(context.Background(), input, false)
	if err != nil {
		t.Fatalf("newUpstreamRequest() error = %v", err)
	}
	t.Cleanup(replay.release)
	if got := request.Header.Values(requestIDHeader); len(got) != 0 {
		t.Fatalf("upstream %s = %#v, want reserved header removed", requestIDHeader, got)
	}
}
