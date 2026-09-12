package cpa

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/subscription"
)

type quotaEventSession struct {
	execution.WebsocketSession
	event []byte
}

func (s *quotaEventSession) ExecuteTurn(ctx context.Context, _ []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	if err := emit(ctx, s.event); err != nil {
		return execution.WebsocketResult{Error: &execution.ErrorEvidence{Code: "consumer_failed"}}
	}
	return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
}

func TestWebsocketQuotaEventsRefreshBeforeTurnCompletesWithoutHeaders(t *testing.T) {
	adapter, _, _, crypt, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	manager := adapter.credentials.(*subscription.CredentialManager)
	event, err := os.ReadFile("../../subscription/providers/codex/testdata/quota-ws-account.json")
	if err != nil {
		t.Fatal(err)
	}
	session := &observedWebsocketSession{
		WebsocketSession: &quotaEventSession{event: event}, adapter: adapter, spec: validSpec(t, row, crypt),
	}
	var lastVersion uint64
	for turn := 0; turn < 2; turn++ {
		startedAt := time.Now().UnixMilli()
		result := session.ExecuteTurn(t.Context(), nil, func(_ context.Context, forwarded []byte) error {
			if !bytes.Equal(forwarded, event) {
				t.Fatal("quota observation changed the forwarded event")
			}
			dirty := manager.DirtyPassiveQuotaObservations(1)
			if len(dirty) != 1 || len(dirty[0].Windows) != 2 {
				t.Fatalf("quota event was not recorded before downstream delivery: %#v", dirty)
			}
			if dirty[0].ObservedAtMS < startedAt || dirty[0].Version <= lastVersion {
				t.Fatalf("reused WS session did not refresh quota evidence: %#v", dirty[0])
			}
			lastVersion = dirty[0].Version
			return nil
		})
		if result.Error != nil || len(result.Header) != 0 {
			t.Fatalf("unexpected WS result: %+v", result)
		}
	}
}
