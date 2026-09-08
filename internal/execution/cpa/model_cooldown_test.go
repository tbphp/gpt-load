package cpa

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/subscription/providers/codex"
)

func TestAdapterErrorResultsPreserveValidResponseMetadata(t *testing.T) {
	for _, test := range []struct {
		name            string
		err             error
		responseStarted bool
	}{
		{name: "quota rejection", err: statusError{status: http.StatusTooManyRequests, message: `{"error":{"type":"usage_limit_reached"}}`}, responseStarted: true},
		{name: "dial failure", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			headers := http.Header{"Retry-After": {"172800"}}
			setCodexExecutor(t, adapter, &fakeExecutor{err: test.err,
				result: codex.ExecuteResponse{Headers: headers}, stream: &codex.ExecuteStreamResponse{Headers: headers}})
			spec := validSpec(t, row, keyService)
			unary := adapter.Execute(t.Context(), spec)
			if err := unary.Validate(); err != nil {
				t.Errorf("invalid unary error result: %v", err)
			}
			stream := adapter.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { t.Fatal("error emitted a successful stream event"); return nil })
			if err := stream.Validate(); err != nil {
				t.Errorf("invalid stream error result: %v", err)
			}
			if unary.ResponseStarted != test.responseStarted || stream.ResponseStarted != test.responseStarted {
				t.Fatal("response-started state changed")
			}
			if test.responseStarted {
				if unary.Header.Get("Retry-After") != "172800" || stream.Header.Get("Retry-After") != "172800" {
					t.Fatal("lost explicit recovery time")
				}
			} else if len(unary.Header) != 0 || len(stream.Header) != 0 {
				t.Fatal("unsent request acquired response headers")
			}
		})
	}
}
