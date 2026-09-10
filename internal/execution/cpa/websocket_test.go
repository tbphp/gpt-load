package cpa

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

func TestWebsocketRejectsUnsupportedSubscriptionWithoutPreparingCredential(t *testing.T) {
	a := NewAdapter(nil, channel.NewRegistry())
	opener, ok := any(a).(execution.WebsocketOpener)
	if !ok {
		t.Fatal("subscription WS adapter unavailable")
	}
	s, result := opener.OpenWebsocket(context.Background(), execution.AttemptSpec{ChannelID: string(channel.Claude)})
	if s != nil || result.Error == nil || result.DispatchState != execution.DispatchNotSent {
		t.Fatalf("unsupported=%+v", result)
	}
}

func TestWebsocketPreparesCodexCredentialBeforeOpeningSession(t *testing.T) {
	adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	preparer := &fakeCredentialPreparer{delegate: adapter.credentials}
	adapter.credentials = preparer
	s, result := adapter.OpenWebsocket(t.Context(), validSpec(t, row, service))
	if result.Error != nil || s == nil {
		t.Fatalf("open=%+v", result)
	}
	defer s.Close()
	if preparer.calls != 1 || result.DispatchState != execution.DispatchNotSent {
		t.Fatal("credential preparation or deferred dial contract changed")
	}
	select {
	case <-s.Done():
		t.Fatal("new session already closed")
	default:
	}
}
