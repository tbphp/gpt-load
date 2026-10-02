package bifrost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestCompatibleStreamIsProgressiveAndCancelable(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			received := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"first token\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-received:
				case <-request.Context().Done():
					return
				case <-time.After(2 * time.Second):
					t.Error("response was buffered before emitting text")
					return
				}
				_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			var data bytes.Buffer
			signaled := false
			result := runtime.ExecuteStream(t.Context(), compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi"}`, true), func(event execution.StreamEvent) error {
				data.Write(event.Data)
				if !signaled && strings.Contains(string(event.Data), "first token") {
					signaled = true
					close(received)
					if canceled {
						return context.Canceled
					}
				}
				return nil
			})
			if !signaled || (!canceled && result.Error != nil) || (canceled && (result.Error == nil || result.Error.Kind != execution.ErrorKindCanceled)) {
				t.Fatalf("progression/cancellation failed: %+v", result)
			}
			if strings.Contains(data.String(), "response.completed") == canceled {
				t.Fatalf("unexpected terminal event: %s", data.String())
			}
		})
	}
}

func TestCompatibleStreamRejectsMissingFinish(t *testing.T) {
	for _, doneMarker := range []bool{false, true} {
		t.Run(fmt.Sprintf("done=%t", doneMarker), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
				if doneMarker {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
			var data bytes.Buffer
			result := runtime.ExecuteStream(t.Context(), compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi"}`, true), func(event execution.StreamEvent) error {
				data.Write(event.Data)
				return nil
			})
			if result.Error == nil || !strings.Contains(data.String(), "partial") || strings.Contains(data.String(), "response.completed") {
				t.Fatalf("truncation reported success: result=%+v data=%s", result, data.String())
			}
		})
	}
}

func TestCompatiblePreservesHTTPErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/stream=%t", status, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "37")
					w.Header().Set("X-Request-Id", "compatible-rejected")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"rejected `+testAPIKey+`"}}`)
				}))
				defer server.Close()
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
				spec := compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi"}`, stream)
				var actualStatus int
				var header http.Header
				var failure *execution.ErrorEvidence
				if stream {
					result := runtime.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error {
						t.Error("HTTP rejection emitted a client stream event")
						return nil
					})
					actualStatus, header, failure = result.StatusCode, result.Header, result.Error
					assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
				} else {
					result := runtime.Execute(t.Context(), spec)
					actualStatus, header, failure = result.StatusCode, result.Header, result.Error
					assertNoPrivateLeak(t, result, testAPIKey, "gptload-custom-")
				}
				if actualStatus != status || failure == nil || failure.Kind != execution.ErrorKindHTTP || failure.StatusCode != status {
					t.Fatalf("HTTP error changed: status=%d error=%+v", actualStatus, failure)
				}
				if header.Get("Retry-After") != "37" || failure.RetryAfter != 37*time.Second || header.Get("X-Request-Id") != "compatible-rejected" {
					t.Fatalf("error evidence lost: headers=%v error=%+v", header, failure)
				}
				if status == http.StatusUnauthorized && (failure.Hint != execution.FailureHintInvalidCredential || failure.ScopeHint != execution.ErrorScopeCredential) {
					t.Fatalf("credential failure classification changed: %+v", failure)
				}
				if status == http.StatusTooManyRequests && failure.Hint != execution.FailureHintRateLimited {
					t.Fatalf("rate-limit classification changed: %+v", failure)
				}
			})
		}
	}
}
