package execution

import (
	"context"
	"testing"
)

type routerExecutor struct{ called *int }

func (e routerExecutor) Execute(context.Context, AttemptSpec) AttemptResult {
	*e.called++
	return AttemptResult{DispatchState: DispatchMaybeSent, ResponseStarted: true, StatusCode: 200}
}

func (e routerExecutor) ExecuteStream(context.Context, AttemptSpec, StreamSink) StreamResult {
	*e.called++
	return StreamResult{DispatchState: DispatchMaybeSent, ResponseStarted: true, StatusCode: 200}
}

func TestRouterDispatchesOnlySelectedConnectionType(t *testing.T) {
	t.Parallel()
	apiCalls, subscriptionCalls := 0, 0
	router := NewRouter(routerExecutor{called: &apiCalls}, routerExecutor{called: &subscriptionCalls})
	router.Execute(t.Context(), AttemptSpec{ConnectionType: "subscription"})
	router.ExecuteStream(t.Context(), AttemptSpec{ConnectionType: "api_key"}, func(StreamEvent) error { return nil })
	if apiCalls != 1 || subscriptionCalls != 1 {
		t.Fatalf("api=%d subscription=%d", apiCalls, subscriptionCalls)
	}
	result := router.Execute(t.Context(), AttemptSpec{ConnectionType: "unknown"})
	if result.DispatchState != DispatchNotSent || result.Error == nil {
		t.Fatalf("result = %#v", result)
	}
}
