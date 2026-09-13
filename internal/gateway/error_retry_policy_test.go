package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

func TestAnthropicPrechargeFailureRetriesWithoutCredentialPenalty(t *testing.T) {
	for _, errorType := range []string{"", "invalid_request_error"} {
		t.Run("type="+errorType, func(t *testing.T) {
			var mu sync.Mutex
			var credentials []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Header.Get("X-Api-Key")
				mu.Lock()
				credentials = append(credentials, key)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if key == "sk-one" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":%q,"message":"预扣费额度失败，余额 0.10，需要预扣 0.20"}}`, errorType)
					return
				}
				_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"model-a","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer upstream.Close()
			engine, registry := newDialectGatewayEngine(t, protocol.Anthropic, "model-a", dialect.NewSet(dialect.NewAnthropic()),
				dialectGatewayGroup{id: 1, name: "precharge", upstreamURL: upstream.URL, apiKeys: []string{"sk-one", "sk-two"}},
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"model-a","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			mu.Lock()
			defer mu.Unlock()
			if response.Code != http.StatusOK || fmt.Sprint(credentials) != "[sk-one sk-two]" {
				t.Fatalf("response=%d attempts=%v body=%s", response.Code, credentials, response.Body.String())
			}
			if candidates := registry.CollectCredentialCandidates([]uint{1}, nil, time.Now()); len(candidates) != 2 {
				t.Fatalf("available credentials=%d, want both", len(candidates))
			}
			entries, err := registry.SnapshotGroupCredentialEntriesExact(1, []uint{1, 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.FailureCount != 0 || entry.Blacklisted || !entry.CooldownUntil.IsZero() || len(entry.ModelCooldowns) != 0 {
					t.Fatalf("credential %d unexpectedly penalized", entry.ID)
				}
			}
		})
	}
}

func TestUnknownResponsesCancellationFailureDoesNotSwitchCredential(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":{"message":"resource rejected"}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"resp_1","status":"cancelled"}`)
	}))
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.OpenAIResponses, "model-a", dialect.NewSet(dialect.NewOpenAIResponses()),
		dialectGatewayGroup{id: 1, name: "resource-retry", channelID: channel.OpenAI,
			params: json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, upstream.URL+"/v1")), apiKeys: []string{"sk-one", "sk-two"}},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses/resp_1/cancel", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || attempts.Load() != 1 || !strings.Contains(response.Body.String(), "resource rejected") {
		t.Fatalf("status=%d attempts=%d body=%s", response.Code, attempts.Load(), response.Body.String())
	}
}

func TestUnknownErrorRetryRecordsAttemptsAndHonorsBudget(t *testing.T) {
	for _, retryCount := range []int{0, 1} {
		t.Run(fmt.Sprintf("retries=%d", retryCount), func(t *testing.T) {
			failure := completeScriptedUpstreamResult(UpstreamResult{
				StatusCode: http.StatusForbidden, RequestWritten: true,
				Body: []byte(`{"error":{"message":"unclassified upstream rejection"}}`),
			})
			forwarder := &scriptedForwarder{results: []UpstreamResult{failure, successfulAffinityResult()}}
			handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
			manager.Current().Settings.RetryCount = retryCount
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			engine := newAffinityTestEngine(t, handler)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			events := sink.snapshot()
			if len(events) != 1 || len(events[0].Attempts) != retryCount+1 || len(forwarder.inputs) != retryCount+1 {
				t.Fatalf("attempts=%d events=%#v", len(forwarder.inputs), events)
			}
			first := events[0].Attempts[0]
			if first.RetryDirective != "next_candidate" || first.Effect != "none" ||
				first.WillRetry != (retryCount > 0) || first.FailureCategory != "ambiguous" {
				t.Fatalf("first attempt=%#v", first)
			}
			if retryCount == 0 && (response.Code != http.StatusForbidden || response.Body.String() != string(failure.Body)) {
				t.Fatalf("terminal response=%d %s", response.Code, response.Body.String())
			}
			if retryCount > 0 && (response.Code != http.StatusOK || forwarder.inputs[0].APIKey == forwarder.inputs[1].APIKey) {
				t.Fatalf("retry did not switch credential: status=%d", response.Code)
			}
		})
	}
}

func TestUnknownFirstStreamErrorRetriesBeforeOutput(t *testing.T) {
	var mu sync.Mutex
	var attempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Authorization")
		mu.Lock()
		attempts = append(attempts, key)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if key == "Bearer sk-one" {
			_, _ = fmt.Fprint(w, "data: "+`{"error":{"type":"new_provider_error","code":"unrecognized_condition","message":"first candidate failed"}}`+"\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: "+`{"id":"test","object":"chat.completion.chunk","model":"model-a","choices":[{"index":0,"delta":{"content":"recovered"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.OpenAICompletions, "model-a", dialect.NewSet(dialect.NewOpenAI()),
		dialectGatewayGroup{id: 1, name: "stream-retry", upstreamURL: upstream.URL, apiKeys: []string{"sk-one", "sk-two"}},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-a","stream":true,"messages":[]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	mu.Lock()
	defer mu.Unlock()
	if response.Code != http.StatusOK || fmt.Sprint(attempts) != "[Bearer sk-one Bearer sk-two]" ||
		!strings.Contains(response.Body.String(), "recovered") || strings.Contains(response.Body.String(), "unrecognized_condition") {
		t.Fatalf("status=%d attempts=%v body=%s", response.Code, attempts, response.Body.String())
	}
}
