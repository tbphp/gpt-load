package bifrost

import (
	"context"
	"encoding/json"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

type testRuntimeOptions struct {
	allowPrivateNetwork       bool
	openAIBaseURL             string
	anthropicBaseURL          string
	geminiBaseURL             string
	maxUnaryResponseBodyBytes int64
}

type testRuntime struct {
	manager  *RuntimeManager
	baseURLs map[channel.ID]string
	registry *channel.Registry
}

func newRuntimeForTest(t *testing.T, options testRuntimeOptions) *testRuntime {
	t.Helper()
	registry := channel.NewRegistry()
	manager, err := newRuntimeManager(runtimeOptions{
		allowPrivateNetwork:       options.allowPrivateNetwork,
		maxUnaryResponseBodyBytes: options.maxUnaryResponseBodyBytes,
	}, registry)
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start runtime manager: %v", err)
	}
	t.Cleanup(manager.Shutdown)
	return &testRuntime{
		manager:  manager,
		registry: registry,
		baseURLs: map[channel.ID]string{
			channel.OpenAI:    options.openAIBaseURL,
			channel.Anthropic: options.anthropicBaseURL,
			channel.Gemini:    options.geminiBaseURL,
		},
	}
}

func (runtime *testRuntime) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	return runtime.manager.Execute(ctx, runtime.withBaseURL(spec))
}

func (runtime *testRuntime) ExecuteStream(
	ctx context.Context,
	spec execution.AttemptSpec,
	sink execution.StreamSink,
) execution.StreamResult {
	return runtime.manager.ExecuteStream(ctx, runtime.withBaseURL(spec), sink)
}

func (runtime *testRuntime) Capabilities() execution.CapabilitySet {
	return runtime.manager.Capabilities()
}

func (runtime *testRuntime) Shutdown() {
	runtime.manager.Shutdown()
}

func (runtime *testRuntime) prepare(spec execution.AttemptSpec, stream bool) (preparedAttempt, *execution.AttemptResult) {
	spec = runtime.withBaseURL(spec)
	config, failure := runtime.manager.configForAttempt(spec)
	if failure != nil {
		return preparedAttempt{}, failure
	}
	lease, err := runtime.manager.pool.acquire(context.Background(), config)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "initialize provider runtime")
		return preparedAttempt{}, &failure
	}
	defer lease.Release()
	providerRuntime, ok := lease.runtime.(*Runtime)
	if !ok {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "invalid provider runtime")
		return preparedAttempt{}, &failure
	}
	return providerRuntime.prepare(spec, stream)
}

func (runtime *testRuntime) keyPoolCalls() uint64 {
	var calls uint64
	runtime.manager.pool.mu.Lock()
	defer runtime.manager.pool.mu.Unlock()
	for _, entry := range runtime.manager.pool.entries {
		if providerRuntime, ok := entry.runtime.(*Runtime); ok {
			calls += providerRuntime.account.keyPoolCalls.Load()
		}
	}
	return calls
}

func (runtime *testRuntime) withBaseURL(spec execution.AttemptSpec) execution.AttemptSpec {
	baseURL := runtime.baseURLs[channel.ID(spec.ChannelID)]
	if baseURL == "" {
		return spec
	}
	clone := spec.Clone()
	clone.TargetConfig = json.RawMessage(`{"base_url":"` + baseURL + `"}`)
	return freezeTestAttempt(clone)
}
