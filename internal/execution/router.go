package execution

import "context"

// Router preserves one global execution contract while dispatching the
// already-selected attempt by its product connection type.
type Router struct {
	apiKey       Executor
	subscription Executor
}

func NewRouter(apiKey Executor, subscription Executor) *Router {
	return &Router{apiKey: apiKey, subscription: subscription}
}

func (r *Router) Execute(ctx context.Context, spec AttemptSpec) AttemptResult {
	executor := r.executor(spec.ConnectionType)
	if executor == nil {
		return AttemptResult{
			DispatchState: DispatchNotSent,
			Error:         &ErrorEvidence{Kind: ErrorKindInvalidRequest, Summary: "unsupported connection type"},
		}
	}
	return executor.Execute(ctx, spec)
}

func (r *Router) ExecuteStream(ctx context.Context, spec AttemptSpec, sink StreamSink) StreamResult {
	executor := r.executor(spec.ConnectionType)
	if executor == nil {
		return StreamResult{
			DispatchState: DispatchNotSent,
			Error:         &ErrorEvidence{Kind: ErrorKindInvalidRequest, Summary: "unsupported connection type"},
		}
	}
	return executor.ExecuteStream(ctx, spec, sink)
}

func (r *Router) executor(connectionType string) Executor {
	if r == nil {
		return nil
	}
	switch connectionType {
	case "", "api_key":
		return r.apiKey
	case "subscription":
		return r.subscription
	default:
		return nil
	}
}

// RetireCredential releases any executor-owned cache for one durable account.
func (r *Router) RetireCredential(credentialID uint) {
	for _, executor := range []Executor{r.apiKey, r.subscription} {
		if retire, ok := executor.(interface{ RetireCredential(uint) }); ok {
			retire.RetireCredential(credentialID)
		}
	}
}

var _ Executor = (*Router)(nil)
