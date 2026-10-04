package cpa

import (
	"bytes"
	"context"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/subscription"
	"gpt-load/internal/subscription/providers/codex"
)

func TestAdapterRecordsCreditOnlyHTTPResponses(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-streaming", true: "streaming"}[stream], func(t *testing.T) {
			adapter, _, _, crypt, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			manager := adapter.credentials.(*subscription.CredentialManager)
			at := time.UnixMilli(2000)
			signals := map[string]string{
				"X-Codex-Credits-Balance": "62500", "X-Codex-Credits-Has-Credits": "True", "X-Codex-Credits-Unlimited": "False",
			}
			fake := &fakeExecutor{result: codex.ExecuteResponse{
				Payload: []byte(`{"id":"resp_1","model":"gpt-5","output":[]}`), QuotaObservedAt: at, QuotaSignals: signals,
			}}
			if stream {
				chunks := make(chan codex.ExecuteStreamChunk)
				close(chunks)
				fake.stream = &codex.ExecuteStreamResponse{Chunks: chunks, QuotaObservedAt: at, QuotaSignals: signals}
			}
			setCodexExecutor(t, adapter, fake)
			if stream {
				adapter.ExecuteStream(t.Context(), validSpec(t, row, crypt), func(execution.StreamEvent) error { return nil })
			} else {
				adapter.Execute(t.Context(), validSpec(t, row, crypt))
			}
			dirty := manager.DirtyPassiveQuotaObservations(1)
			if len(dirty) != 1 || dirty[0].Credits == nil || dirty[0].Credits.Balance != "62500" || *dirty[0].Credits.ObservedAtMS != at.UnixMilli() {
				t.Fatalf("credit-only HTTP response was not recorded: %#v", dirty)
			}
		})
	}
}

func TestWebsocketRecordsCreditOnlyEventsBeforeForwarding(t *testing.T) {
	adapter, _, _, crypt, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	manager := adapter.credentials.(*subscription.CredentialManager)
	for _, balance := range []string{"62500", "0"} {
		event := []byte(`{"type":"codex.rate_limits","credits":{"balance":"` + balance + `","has_credits":true,"unlimited":false}}`)
		session := &observedWebsocketSession{
			WebsocketSession: &quotaEventSession{event: event}, adapter: adapter, spec: validSpec(t, row, crypt),
		}
		result := session.ExecuteTurn(t.Context(), nil, func(_ context.Context, forwarded []byte) error {
			if !bytes.Equal(event, forwarded) {
				t.Fatal("credit observation changed the forwarded event")
			}
			dirty := manager.DirtyPassiveQuotaObservations(1)
			if len(dirty) != 1 || dirty[0].Credits == nil || dirty[0].Credits.Balance != balance {
				t.Fatalf("credit-only WebSocket event was not recorded: %#v", dirty)
			}
			return nil
		})
		if result.Error != nil {
			t.Fatalf("WebSocket result = %+v", result)
		}
	}
}
