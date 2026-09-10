package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestWSSessionIsExplicitAndHTTPExecutorIsUnchanged(t *testing.T) {
	session, err := NewWSSession(WSSessionOptions{
		CredentialID: "test", Credential: Credential{Type: Provider, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "test-account"},
		ProxyURL: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := session.ExecuteTurn(ctx, json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	var failure *WSError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &failure) || result.DispatchState != "not_sent" {
		t.Fatalf("cancellation contract lost: %v", err)
	}
	httpExecutor := NewExecutor().(*executor)
	if httpExecutor.bridge == nil {
		t.Fatal("existing HTTP executor changed")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
	if !errors.As(err, &failure) || failure.Code != "session_closed" {
		t.Fatalf("closed session reused: %v", err)
	}
}
