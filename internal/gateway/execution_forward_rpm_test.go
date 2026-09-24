package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/rpm"
)

func TestExecutionForwarderRecordsEachCredentialAttemptBeforeCompletion(t *testing.T) {
	store := rpm.NewStore()
	want := map[uint]int64{7: 1, 8: 1}
	forwarder := NewExecutionForwarder(fakeExecutionExecutor{unary: func(_ context.Context, spec execution.AttemptSpec) execution.AttemptResult {
		if got := store.Current(rpm.Credential, spec.Credential.ID, time.Now()).Requests; got != want[spec.Credential.ID] {
			t.Fatalf("credential %d RPM during execution = %d, want %d", spec.Credential.ID, got, want[spec.Credential.ID])
		}
		return execution.AttemptResult{DispatchState: execution.DispatchMaybeSent, ResponseStarted: true, StatusCode: 200, Body: []byte(`{"ok":true}`)}
	}})
	forwarder.SetRPMStore(store)

	input := executionForwardInput()
	forwarder.Forward(t.Context(), input)
	want[7] = 2
	input.AttemptID = "retry-on-same-credential"
	forwarder.Forward(t.Context(), input)
	input.Credential = execution.NewCredentialSnapshot(8, 2, 3, []byte(`{"api_key":"other"}`))
	input.AttemptID = "fallback-to-another-credential"
	forwarder.Forward(t.Context(), input)

	input.Request = nil
	forwarder.Forward(t.Context(), input)
	if got := store.Current(rpm.Credential, 7, time.Now()).Requests; got != 2 {
		t.Fatalf("credential 7 RPM after local rejection = %d, want 2", got)
	}
	if got := store.Current(rpm.Credential, 8, time.Now()).Requests; got != 1 {
		t.Fatalf("credential 8 RPM after local rejection = %d, want 1", got)
	}
}

func TestExecutionForwarderRecordsStreamingAttemptBeforeCompletion(t *testing.T) {
	store := rpm.NewStore()
	forwarder := NewExecutionForwarder(fakeExecutionExecutor{stream: func(_ context.Context, spec execution.AttemptSpec, _ execution.StreamSink) execution.StreamResult {
		if got := store.Current(rpm.Credential, spec.Credential.ID, time.Now()).Requests; got != 1 {
			t.Fatalf("stream RPM during execution = %d, want 1", got)
		}
		return execution.StreamResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, OriginHint: execution.ErrorOriginUpstream}}
	}})
	forwarder.SetRPMStore(store)
	forwarder.ForwardStream(t.Context(), executionForwardInput(), httptest.NewRecorder())
}

type rpmWebsocketExecutor struct {
	fakeExecutionExecutor
	session execution.WebsocketSession
}

func (executor rpmWebsocketExecutor) OpenWebsocket(context.Context, execution.AttemptSpec) (execution.WebsocketSession, execution.WebsocketResult) {
	return executor.session, execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
}

type rpmWebsocketSession struct {
	onTurn func()
	done   chan struct{}
}

func (session *rpmWebsocketSession) ExecuteTurn(context.Context, []byte, func(context.Context, []byte) error) execution.WebsocketResult {
	session.onTurn()
	return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
}

func (session *rpmWebsocketSession) Done() <-chan struct{} { return session.done }
func (session *rpmWebsocketSession) Close() error          { close(session.done); return nil }

func TestExecutionForwarderRecordsWebsocketBusinessTurnsOnce(t *testing.T) {
	store := rpm.NewStore()
	turns := int64(0)
	underlying := &rpmWebsocketSession{done: make(chan struct{}), onTurn: func() {
		turns++
		if got := store.Current(rpm.Credential, 7, time.Now()).Requests; got != turns {
			t.Fatalf("WebSocket RPM during turn = %d, want %d", got, turns)
		}
	}}
	forwarder := NewExecutionForwarder(rpmWebsocketExecutor{session: underlying})
	forwarder.SetRPMStore(store)
	session, result := forwarder.OpenWebsocket(t.Context(), executionForwardInput())
	if session == nil || result.Error != nil {
		t.Fatalf("OpenWebsocket() = %#v, %#v", session, result)
	}
	defer session.Close()
	if got := store.Current(rpm.Credential, 7, time.Now()).Requests; got != 0 {
		t.Fatalf("WebSocket handshake counted as business turn: %d", got)
	}
	session.ExecuteTurn(t.Context(), nil, nil)
	session.ExecuteTurn(t.Context(), nil, nil)
	if turns != 2 {
		t.Fatalf("executed turns = %d, want 2", turns)
	}
}
