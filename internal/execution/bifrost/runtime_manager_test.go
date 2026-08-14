package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestEffectiveProviderConfigUsesSDKProviderAndCanonicalDefaultBaseURL(t *testing.T) {
	registry := channel.NewRegistry()

	defaultOpenAI := effectiveConfigForTest(t, registry, channel.OpenAI, nil)
	explicitOpenAI := effectiveConfigForTest(t, registry, channel.OpenAI, json.RawMessage(`{"base_url":"https://api.openai.com/"}`))
	if defaultOpenAI.provider != schemas.OpenAI || defaultOpenAI.custom {
		t.Fatalf("OpenAI config = provider %q custom %t", defaultOpenAI.provider, defaultOpenAI.custom)
	}
	if defaultOpenAI.providerConfig.NetworkConfig.BaseURL != "https://api.openai.com" {
		t.Fatalf("OpenAI default base URL = %q", defaultOpenAI.providerConfig.NetworkConfig.BaseURL)
	}
	if defaultOpenAI.fingerprint != explicitOpenAI.fingerprint ||
		string(defaultOpenAI.canonical) != string(explicitOpenAI.canonical) {
		t.Fatalf("default and explicit OpenAI configs differ: %q/%q", defaultOpenAI.fingerprint, explicitOpenAI.fingerprint)
	}

	for channelID, wantProvider := range map[channel.ID]schemas.ModelProvider{
		channel.DeepSeek:   schemas.DeepSeek,
		channel.OpenRouter: schemas.OpenRouter,
		channel.Groq:       schemas.Groq,
		channel.XAI:        schemas.XAI,
	} {
		config := effectiveConfigForTest(t, registry, channelID, nil)
		if config.provider != wantProvider || config.custom {
			t.Errorf("%s config = provider %q custom %t", channelID, config.provider, config.custom)
		}
	}

	compatible := effectiveConfigForTest(t, registry, channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://relay.example/v1"}`))
	if compatible.provider == schemas.OpenAI || !compatible.custom || compatible.targetBaseURL != "https://relay.example/v1" {
		t.Fatalf("compatible config = provider %q custom %t base %q", compatible.provider, compatible.custom, compatible.targetBaseURL)
	}
}

func TestDeepSeekResponsesUsesDedicatedOpenAIRuntimeConfig(t *testing.T) {
	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(channel.DeepSeek, json.RawMessage(`{"base_url":"https://deepseek.example/api"}`))
	if err != nil {
		t.Fatal(err)
	}
	base, err := buildEffectiveProviderConfig(resolved, true)
	if err != nil {
		t.Fatal(err)
	}
	responses, err := buildDeepSeekResponsesConfig(resolved, true)
	if err != nil {
		t.Fatal(err)
	}
	if base.provider != schemas.DeepSeek || base.custom || responses.provider == base.provider || !responses.custom {
		t.Fatalf("base/responses configs = %+v/%+v", base, responses)
	}
	custom := responses.providerConfig.CustomProviderConfig
	if custom == nil || custom.BaseProviderType != schemas.OpenAI || custom.AllowedRequests == nil ||
		!custom.AllowedRequests.Responses || !custom.AllowedRequests.ResponsesStream ||
		custom.AllowedRequests.ChatCompletion || custom.AllowedRequests.ChatCompletionStream {
		t.Fatalf("DeepSeek Responses custom config = %+v", custom)
	}
	if custom.RequestPathOverrides[schemas.ResponsesRequest] != "https://deepseek.example/api/responses" ||
		custom.RequestPathOverrides[schemas.ResponsesStreamRequest] != "https://deepseek.example/api/responses" {
		t.Fatalf("DeepSeek Responses path overrides = %#v", custom.RequestPathOverrides)
	}
}

func TestRuntimeManagerPartitionsAzureEntraByCredentialIdentity(t *testing.T) {
	registry := channel.NewRegistry()
	target, err := registry.Resolve(
		channel.AzureOpenAI,
		json.RawMessage(`{"endpoint":"https://resource.openai.azure.com"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newRuntimeManager(runtimeOptions{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := execution.AttemptSpec{
		ChannelID:    string(channel.AzureOpenAI),
		TargetKind:   string(target.ProviderKind),
		TargetConfig: target.TargetConfig,
	}
	spec.Credential = execution.NewCredentialSnapshot(
		1,
		10,
		100,
		[]byte(`{"client_id":"client","client_secret":"secret-one","tenant_id":"tenant"}`),
	)
	first, failure := manager.configForAttempt(spec)
	if failure != nil {
		t.Fatalf("first config failure = %+v", failure)
	}
	spec.Credential = execution.NewCredentialSnapshot(
		2,
		20,
		200,
		[]byte(`{"client_id":"client","client_secret":"secret-two","tenant_id":"tenant"}`),
	)
	second, failure := manager.configForAttempt(spec)
	if failure != nil {
		t.Fatalf("second config failure = %+v", failure)
	}
	if first.fingerprint == second.fingerprint || bytes.Equal(first.canonical, second.canonical) {
		t.Fatal("distinct Entra credential identities shared one provider runtime config")
	}
	for _, secret := range []string{"secret-one", "secret-two"} {
		if bytes.Contains(first.canonical, []byte(secret)) || bytes.Contains(second.canonical, []byte(secret)) {
			t.Fatalf("provider runtime config contains credential secret %q", secret)
		}
	}

	spec.Credential = execution.NewCredentialSnapshot(3, 30, 300, []byte(`{"api_key":"azure-key-one"}`))
	apiKeyFirst, failure := manager.configForAttempt(spec)
	if failure != nil {
		t.Fatalf("first API-key config failure = %+v", failure)
	}
	spec.Credential = execution.NewCredentialSnapshot(4, 40, 400, []byte(`{"api_key":"azure-key-two"}`))
	apiKeySecond, failure := manager.configForAttempt(spec)
	if failure != nil {
		t.Fatalf("second API-key config failure = %+v", failure)
	}
	if apiKeyFirst.fingerprint != apiKeySecond.fingerprint ||
		!bytes.Equal(apiKeyFirst.canonical, apiKeySecond.canonical) {
		t.Fatal("Azure API keys unexpectedly partitioned provider runtimes")
	}
}

func TestRuntimeManagerReusesOneRuntimePerEffectiveConfig(t *testing.T) {
	registry := channel.NewRegistry()
	config := effectiveConfigForTest(t, registry, channel.DeepSeek, json.RawMessage(`{"base_url":"https://one.example/v1"}`))
	other := effectiveConfigForTest(t, registry, channel.DeepSeek, json.RawMessage(`{"base_url":"https://two.example/v1"}`))
	var creates atomic.Int64
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		creates.Add(1)
		return newFakeManagedRuntime(), nil
	})
	manager.reconcile([]effectiveProviderConfig{config, other})

	first, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	third, err := manager.acquire(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Release()

	if first.runtime != second.runtime {
		t.Fatal("same effective config did not reuse one runtime")
	}
	if first.runtime == third.runtime {
		t.Fatal("different effective configs shared one runtime")
	}
	if creates.Load() != 2 {
		t.Fatalf("runtime creates = %d, want 2", creates.Load())
	}
}

func TestRuntimeManagerInitializesSameConfigOnceConcurrentlyAndRetriesFailure(t *testing.T) {
	config := effectiveConfigForTest(t, channel.NewRegistry(), channel.OpenAI, nil)
	var creates atomic.Int64
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		call := creates.Add(1)
		if call == 1 {
			return nil, errors.New("init failed")
		}
		return newFakeManagedRuntime(), nil
	})
	manager.reconcile([]effectiveProviderConfig{config})
	if _, err := manager.acquire(context.Background(), config); err == nil {
		t.Fatal("first acquire error = nil")
	}

	const callers = 24
	leases := make(chan *runtimeLease, callers)
	errorsFound := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, err := manager.acquire(context.Background(), config)
			if err != nil {
				errorsFound <- err
				return
			}
			leases <- lease
		}()
	}
	wait.Wait()
	close(errorsFound)
	close(leases)
	for err := range errorsFound {
		t.Errorf("concurrent acquire error = %v", err)
	}
	var shared managedProviderRuntime
	for lease := range leases {
		if shared == nil {
			shared = lease.runtime
		} else if shared != lease.runtime {
			t.Error("concurrent acquire returned different runtimes")
		}
		lease.Release()
	}
	if creates.Load() != 2 {
		t.Fatalf("runtime creates = %d, want one failure and one successful retry", creates.Load())
	}
}

func TestRuntimeManagerSharesInitializationFailureWithCurrentWaiters(t *testing.T) {
	config := effectiveConfigForTest(t, channel.NewRegistry(), channel.OpenAI, nil)
	initStarted := make(chan struct{})
	releaseInit := make(chan struct{})
	initFailure := errors.New("shared init failure")
	var creates atomic.Int64
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		if creates.Add(1) == 1 {
			close(initStarted)
			<-releaseInit
			return nil, initFailure
		}
		return newFakeManagedRuntime(), nil
	})
	manager.reconcile([]effectiveProviderConfig{config})

	const callers = 12
	errorsFound := make(chan error, callers)
	go func() {
		_, err := manager.acquire(context.Background(), config)
		errorsFound <- err
	}()
	<-initStarted
	for range callers - 1 {
		go func() {
			_, err := manager.acquire(context.Background(), config)
			errorsFound <- err
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(releaseInit)
	for range callers {
		if err := <-errorsFound; !errors.Is(err, initFailure) {
			t.Errorf("concurrent acquire error = %v, want shared initialization failure", err)
		}
	}
	if creates.Load() != 1 {
		t.Fatalf("runtime creates during failed wave = %d, want 1", creates.Load())
	}

	lease, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatalf("subsequent acquire error = %v", err)
	}
	lease.Release()
	if creates.Load() != 2 {
		t.Fatalf("runtime creates after retry = %d, want 2", creates.Load())
	}
	<-manager.beginShutdown()
}

func TestRuntimeManagerRetiresOnlyAfterLastLeaseAndRejectsAfterShutdown(t *testing.T) {
	config := effectiveConfigForTest(t, channel.NewRegistry(), channel.Groq, nil)
	created := make(chan *fakeManagedRuntime, 2)
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		runtime := newFakeManagedRuntime()
		created <- runtime
		return runtime, nil
	})
	manager.reconcile([]effectiveProviderConfig{config})
	lease, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	runtime := <-created
	manager.reconcile(nil)
	if runtime.shutdowns.Load() != 0 {
		t.Fatal("runtime shut down while a lease was in flight")
	}
	lease.Release()
	if runtime.shutdowns.Load() != 1 {
		t.Fatalf("shutdowns after release = %d, want 1", runtime.shutdowns.Load())
	}

	<-manager.beginShutdown()
	if _, err := manager.acquire(context.Background(), config); err == nil {
		t.Fatal("acquire after shutdown error = nil")
	}
}

func TestRuntimeManagerRetiresCredentialPartitionAfterLastLease(t *testing.T) {
	base := effectiveConfigForTest(
		t,
		channel.NewRegistry(),
		channel.AzureOpenAI,
		json.RawMessage(`{"endpoint":"https://resource.openai.azure.com"}`),
	)
	partition, err := partitionProviderRuntime(
		base,
		execution.NewCredentialSnapshot(17, 1, 23, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newFakeManagedRuntime()
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		return runtime, nil
	})
	manager.reconcile([]effectiveProviderConfig{base})
	lease, err := manager.acquire(context.Background(), partition)
	if err != nil {
		t.Fatal(err)
	}
	manager.retireCredential(17)
	if runtime.shutdowns.Load() != 0 {
		t.Fatal("credential runtime shut down while a lease was in flight")
	}
	lease.Release()
	if runtime.shutdowns.Load() != 1 {
		t.Fatalf("credential runtime shutdowns = %d, want 1", runtime.shutdowns.Load())
	}
	<-manager.beginShutdown()
}

func TestRuntimeManagerKeepsRuntimesRetiringWhenReconcileArrivesAfterShutdown(t *testing.T) {
	config := effectiveConfigForTest(t, channel.NewRegistry(), channel.Groq, nil)
	runtime := newFakeManagedRuntime()
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		return runtime, nil
	})
	manager.reconcile([]effectiveProviderConfig{config})
	lease, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	done := manager.beginShutdown()
	manager.reconcile([]effectiveProviderConfig{config})
	lease.Release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete after the final lease was released")
	}
	if got := runtime.shutdowns.Load(); got != 1 {
		t.Fatalf("runtime shutdowns = %d, want 1", got)
	}
}

func TestRuntimeManagerTreatsUnknownDraftConfigAsEphemeral(t *testing.T) {
	config := effectiveConfigForTest(t, channel.NewRegistry(), channel.XAI, nil)
	runtime := newFakeManagedRuntime()
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		return runtime, nil
	})
	manager.reconcile(nil)
	lease, err := manager.acquire(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.shutdowns.Load() != 0 {
		t.Fatal("ephemeral runtime shut down before its lease was released")
	}
	lease.Release()
	if runtime.shutdowns.Load() != 1 {
		t.Fatalf("ephemeral runtime shutdowns = %d, want 1", runtime.shutdowns.Load())
	}
}

func TestRuntimeManagerDoesNotImposeACoreCountLimit(t *testing.T) {
	registry := channel.NewRegistry()
	configs := make([]effectiveProviderConfig, 0, 100)
	for index := range 100 {
		configs = append(configs, effectiveConfigForTest(
			t,
			registry,
			channel.DeepSeek,
			json.RawMessage(`{"base_url":"https://runtime-`+fmt.Sprint(index)+`.example"}`),
		))
	}
	var creates atomic.Int64
	manager := newRuntimeManagerPool(func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error) {
		creates.Add(1)
		return newFakeManagedRuntime(), nil
	})
	manager.reconcile(configs)
	for _, config := range configs {
		lease, err := manager.acquire(context.Background(), config)
		if err != nil {
			t.Fatalf("acquire(%s) error = %v", config.fingerprint, err)
		}
		lease.Release()
	}
	manager.mu.Lock()
	entryCount := len(manager.entries)
	manager.mu.Unlock()
	if entryCount != len(configs) || creates.Load() != int64(len(configs)) {
		t.Fatalf("entries/creates = %d/%d, want %d/%d", entryCount, creates.Load(), len(configs), len(configs))
	}
	<-manager.beginShutdown()
}

func TestRuntimeManagerRetiringOneCoreDoesNotStopAnother(t *testing.T) {
	registry := channel.NewRegistry()
	firstConfig := effectiveConfigForTest(t, registry, channel.DeepSeek, json.RawMessage(`{"base_url":"https://first.example"}`))
	secondConfig := effectiveConfigForTest(t, registry, channel.DeepSeek, json.RawMessage(`{"base_url":"https://second.example"}`))
	created := make(map[string]*fakeManagedRuntime)
	manager := newRuntimeManagerPool(func(_ context.Context, config effectiveProviderConfig) (managedProviderRuntime, error) {
		runtime := newFakeManagedRuntime()
		created[config.fingerprint] = runtime
		return runtime, nil
	})
	manager.reconcile([]effectiveProviderConfig{firstConfig, secondConfig})
	for _, config := range []effectiveProviderConfig{firstConfig, secondConfig} {
		lease, err := manager.acquire(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}

	manager.reconcile([]effectiveProviderConfig{secondConfig})
	if got := created[firstConfig.fingerprint].shutdowns.Load(); got != 1 {
		t.Fatalf("retired runtime shutdowns = %d, want 1", got)
	}
	if got := created[secondConfig.fingerprint].shutdowns.Load(); got != 0 {
		t.Fatalf("active runtime shutdowns = %d, want 0", got)
	}
	lease, err := manager.acquire(context.Background(), secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if lease.runtime != created[secondConfig.fingerprint] {
		t.Fatal("active runtime was replaced while another Core retired")
	}
	lease.Release()
	<-manager.beginShutdown()
}

func TestProductionRuntimeManagerIsolatesDeepSeekURLsAndDirectKeys(t *testing.T) {
	serverKeys := func(label string) (*httptest.Server, *sync.Mutex, *[]string) {
		var mu sync.Mutex
		keys := make([]string, 0, 2)
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/chat/completions" {
				t.Errorf("%s path = %q", label, request.URL.Path)
			}
			mu.Lock()
			keys = append(keys, request.Header.Get("Authorization"))
			mu.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"chat","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"`+label+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}))
		return server, &mu, &keys
	}
	firstServer, firstMu, firstKeys := serverKeys("first")
	defer firstServer.Close()
	secondServer, secondMu, secondKeys := serverKeys("second")
	defer secondServer.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	for _, test := range []struct {
		url          string
		credentialID uint
		key          string
	}{
		{url: firstServer.URL, credentialID: 1, key: "key-one"},
		{url: firstServer.URL, credentialID: 2, key: "key-two"},
		{url: secondServer.URL, credentialID: 3, key: "key-three"},
	} {
		result := manager.Execute(context.Background(), deepSeekAttempt(t, test.url, test.credentialID, test.key))
		if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
			t.Fatalf("Execute(%s) = %+v validation=%v", test.url, result, validationErr)
		}
	}

	manager.pool.mu.Lock()
	entryCount := len(manager.pool.entries)
	configuredProviderCounts := make([]int, 0, entryCount)
	for _, entry := range manager.pool.entries {
		runtime, ok := entry.runtime.(*Runtime)
		if !ok {
			manager.pool.mu.Unlock()
			t.Fatalf("runtime type = %T", entry.runtime)
		}
		providers, providerErr := runtime.account.GetConfiguredProviders()
		if providerErr != nil {
			manager.pool.mu.Unlock()
			t.Fatalf("GetConfiguredProviders() error = %v", providerErr)
		}
		configuredProviderCounts = append(configuredProviderCounts, len(providers))
	}
	manager.pool.mu.Unlock()
	if entryCount != 2 {
		t.Fatalf("runtime entries = %d, want one per unique URL", entryCount)
	}
	for _, count := range configuredProviderCounts {
		if count != 1 {
			t.Fatalf("configured providers per Core = %d, want 1", count)
		}
	}
	firstMu.Lock()
	gotFirstKeys := append([]string(nil), (*firstKeys)...)
	firstMu.Unlock()
	secondMu.Lock()
	gotSecondKeys := append([]string(nil), (*secondKeys)...)
	secondMu.Unlock()
	if len(gotFirstKeys) != 2 || gotFirstKeys[0] != "Bearer key-one" || gotFirstKeys[1] != "Bearer key-two" {
		t.Fatalf("first endpoint keys = %#v", gotFirstKeys)
	}
	if len(gotSecondKeys) != 1 || gotSecondKeys[0] != "Bearer key-three" {
		t.Fatalf("second endpoint keys = %#v", gotSecondKeys)
	}
}

func TestProductionRuntimeManagerUsesNativeProviderPathContracts(t *testing.T) {
	tests := []struct {
		channelID channel.ID
		wantPath  string
	}{
		{channelID: channel.DeepSeek, wantPath: "/chat/completions"},
		{channelID: channel.OpenRouter, wantPath: "/v1/chat/completions"},
		{channelID: channel.Groq, wantPath: "/v1/chat/completions"},
		{channelID: channel.XAI, wantPath: "/v1/chat/completions"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, test.wantPath)
				}
				if request.URL.RawQuery != "trace=%2F&cursor=next" {
					t.Errorf("query = %q", request.URL.RawQuery)
				}
				if got := request.Header.Get("Authorization"); got != "Bearer native-key" {
					t.Errorf("Authorization = %q", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"id":"chat","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer server.Close()

			manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Shutdown)

			spec := openAIChatAttempt(t, test.channelID, server.URL, "native-key")
			spec.RawQuery = "trace=%2F&cursor=next"
			result := manager.Execute(context.Background(), spec)
			if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
				t.Fatalf("Execute() = %+v validation=%v", result, validationErr)
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestProductionRuntimeManagerUsesDeclaredResponsesOperation(t *testing.T) {
	tests := []struct {
		channelID channel.ID
		wantPath  string
		response  string
	}{
		{
			channelID: channel.DeepSeek,
			wantPath:  "/responses",
			response:  `{"id":"resp","object":"response","created_at":1,"status":"completed","model":"served","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			channelID: channel.OpenRouter,
			wantPath:  "/v1/responses",
			response:  `{"id":"resp","object":"response","created_at":1,"status":"completed","model":"served","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
		{
			channelID: channel.Groq,
			wantPath:  "/v1/chat/completions",
			response:  `{"id":"chat","object":"chat.completion","created":1,"model":"served","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		},
		{
			channelID: channel.XAI,
			wantPath:  "/v1/responses",
			response:  `{"id":"resp","object":"response","created_at":1,"status":"completed","model":"served","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, test.wantPath)
				}
				if request.URL.RawQuery != "trace=%2F&cursor=next" {
					t.Errorf("query = %q", request.URL.RawQuery)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.response)
			}))
			defer server.Close()

			manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Shutdown)

			spec := openAIResponsesAttempt(t, test.channelID, server.URL)
			spec.RawQuery = "trace=%2F&cursor=next"
			result := manager.Execute(context.Background(), spec)
			if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
				t.Fatalf("Execute() = %+v validation=%v", result, validationErr)
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestProductionRuntimeManagerUsesDeepSeekNativeAnthropicEndpoint(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/anthropic/v1/messages" {
			t.Errorf("request target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Api-Key") != "native-key" || request.Header.Get("Authorization") != "" {
			t.Errorf("credential headers = x-api-key:%q authorization:%q", request.Header.Get("X-Api-Key"), request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"msg","type":"message","role":"assistant","model":"served","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := deepSeekAnthropicAttempt(t, server.URL)
	result := manager.Execute(context.Background(), spec)
	if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
		t.Fatalf("Execute() = %+v validation=%v", result, validationErr)
	}
	if calls.Load() != 1 || result.UpstreamAPI != execution.UpstreamAPIAnthropicMessages {
		t.Fatalf("calls/result = %d/%+v", calls.Load(), result)
	}
}

func TestProductionRuntimeManagerRejectsDeepSeekAnthropicContainerBeforeDispatch(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := deepSeekAnthropicAttempt(t, server.URL)
	spec.RouteRequirement = execution.RouteRequirementNative
	spec.Body = []byte(`{"model":"client-model","max_tokens":16,"container":{"id":"container_123"},"messages":[{"role":"user","content":"hello"}]}`)
	result := manager.Execute(context.Background(), spec)
	if validationErr := result.Validate(); validationErr != nil {
		t.Fatalf("Execute() validation = %v; result=%+v", validationErr, result)
	}
	if result.DispatchState != execution.DispatchNotSent || result.ResponseStarted || result.Error == nil ||
		result.Error.Kind != execution.ErrorKindConversionUnsupported ||
		result.Error.Code != execution.ErrorCodeCriticalSemanticLoss {
		t.Fatalf("Execute() = %+v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestProductionRuntimeManagerPreservesDeepSeekAnthropicEffort(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			OutputConfig *struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request body: %v; body=%s", err, body)
		}
		if payload.OutputConfig == nil || payload.OutputConfig.Effort != "high" {
			t.Errorf("output_config = %+v; body=%s", payload.OutputConfig, body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"msg","type":"message","role":"assistant","model":"served","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := deepSeekAnthropicAttempt(t, server.URL)
	spec.Body = []byte(`{"model":"client-model","max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}]}`)
	result := manager.Execute(context.Background(), spec)
	if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
		t.Fatalf("Execute() = %+v validation=%v", result, validationErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestProductionRuntimeManagerPreservesDeepSeekAnthropicEffortStream(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var payload struct {
			Stream       bool `json:"stream"`
			OutputConfig *struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request body: %v; body=%s", err, body)
			return
		}
		if !payload.Stream || payload.OutputConfig == nil || payload.OutputConfig.Effort != "high" {
			t.Errorf("stream/output_config = %t/%+v; body=%s", payload.Stream, payload.OutputConfig, body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, anthropicResponsesStreamFixture)
	}))
	defer server.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := deepSeekAnthropicAttempt(t, server.URL)
	spec.Body = []byte(`{"model":"client-model","max_tokens":8192,"stream":true,"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"hello"}]}`)
	result := manager.ExecuteStream(context.Background(), spec, func(execution.StreamEvent) error { return nil })
	if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
		t.Fatalf("ExecuteStream() = %+v validation=%v", result, validationErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

func TestProductionRuntimeManagerUsesDeepSeekNativeResponsesStream(t *testing.T) {
	const upstreamStream = "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_stream\",\"object\":\"response\",\"status\":\"in_progress\",\"model\":\"served\",\"output\":[]}}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"served\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/responses" {
			t.Errorf("request target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer native-key" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil || payload["stream"] != true {
			t.Errorf("stream request = %s err=%v", body, err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, upstreamStream)
	}))
	defer server.Close()

	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)

	spec := openAIResponsesAttempt(t, channel.DeepSeek, server.URL)
	var data bytes.Buffer
	result := manager.ExecuteStream(context.Background(), spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			data.Write(event.Data)
		}
		return nil
	})
	if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
		t.Fatalf("ExecuteStream() = %+v validation=%v", result, validationErr)
	}
	if !strings.Contains(data.String(), "event: response.created") ||
		!strings.Contains(data.String(), "event: response.completed") ||
		result.UpstreamAPI != execution.UpstreamAPIOpenAIResponses {
		t.Fatalf("stream/result = %q/%+v", data.String(), result)
	}
}

func TestProductionRuntimeManagerUsesNativeProviderListModelsPaths(t *testing.T) {
	tests := []struct {
		channelID channel.ID
		wantPath  string
	}{
		{channelID: channel.DeepSeek, wantPath: "/models"},
		{channelID: channel.OpenRouter, wantPath: "/v1/models"},
		{channelID: channel.Groq, wantPath: "/v1/models"},
		{channelID: channel.XAI, wantPath: "/v1/models"},
	}
	for _, test := range tests {
		t.Run(string(test.channelID), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != test.wantPath {
					t.Errorf("path = %q, want %q", request.URL.Path, test.wantPath)
				}
				if request.URL.RawQuery != "cursor=%2F&limit=10" {
					t.Errorf("query = %q", request.URL.RawQuery)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"model-one","object":"model"}]}`)
			}))
			defer server.Close()

			manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, channel.NewRegistry())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(manager.Shutdown)

			spec := listModelsAttempt(t, test.channelID, server.URL)
			spec.RawQuery = "cursor=%2F&limit=10"
			result := manager.Execute(context.Background(), spec)
			if validationErr := result.Validate(); validationErr != nil || result.Error != nil {
				t.Fatalf("Execute() = %+v validation=%v", result, validationErr)
			}
			if calls.Load() != 1 || !bytes.Contains(result.Body, []byte(`"id":"model-one"`)) {
				t.Fatalf("calls/body = %d/%s", calls.Load(), result.Body)
			}
		})
	}
}

func deepSeekAttempt(t *testing.T, baseURL string, credentialID uint, apiKey string) execution.AttemptSpec {
	t.Helper()
	target, err := channel.NewRegistry().Resolve(channel.DeepSeek, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion)
	if !ok {
		t.Fatal("DeepSeek Chat route is missing")
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "manager-request",
		AttemptID:      "manager-attempt",
		Sequence:       1,
		ChannelID:      string(channel.DeepSeek),
		TargetKind:     string(target.ProviderKind),
		TargetConfig:   target.TargetConfig,
		RouteMode:      execution.RouteMode(mode),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ClientModel:    "client-model",
		UpstreamModel:  "deepseek-chat",
		Method:         http.MethodPost,
		Path:           "/v1/chat/completions",
		Header:         make(http.Header),
		Body:           []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`),
		Credential: execution.NewCredentialSnapshot(
			credentialID,
			1,
			1,
			[]byte(`{"api_key":"`+apiKey+`"}`),
		),
	})
}

func openAIChatAttempt(t *testing.T, channelID channel.ID, baseURL string, apiKey string) execution.AttemptSpec {
	t.Helper()
	registry := channel.NewRegistry()
	target, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationChatCompletion)
	if !ok {
		t.Fatalf("%s Chat route is missing", channelID)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "native-path-request",
		AttemptID:      "native-path-attempt",
		Sequence:       1,
		ChannelID:      string(channelID),
		TargetKind:     string(target.ProviderKind),
		TargetConfig:   target.TargetConfig,
		RouteMode:      execution.RouteMode(mode),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ClientModel:    "client-model",
		UpstreamModel:  "upstream-model",
		Method:         http.MethodPost,
		Path:           "/v1/chat/completions",
		Header:         make(http.Header),
		Body:           []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`),
		Credential:     execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"`+apiKey+`"}`)),
	})
}

func openAIResponsesAttempt(t *testing.T, channelID channel.ID, baseURL string) execution.AttemptSpec {
	t.Helper()
	registry := channel.NewRegistry()
	target, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate)
	if !ok {
		t.Fatalf("%s Responses route is missing", channelID)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "responses-path-request",
		AttemptID:      "responses-path-attempt",
		Sequence:       1,
		ChannelID:      string(channelID),
		TargetKind:     string(target.ProviderKind),
		TargetConfig:   target.TargetConfig,
		RouteMode:      execution.RouteMode(mode),
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesCreate,
		ClientModel:    "upstream-model",
		UpstreamModel:  "upstream-model",
		Method:         http.MethodPost,
		Path:           "/v1/responses",
		Header:         make(http.Header),
		Body:           []byte(`{"model":"upstream-model","input":"hello","stream":false}`),
		Credential:     execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"native-key"}`)),
	})
}

func deepSeekAnthropicAttempt(t *testing.T, baseURL string) execution.AttemptSpec {
	t.Helper()
	registry := channel.NewRegistry()
	target, err := registry.Resolve(channel.DeepSeek, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.Anthropic, execution.OperationChatCompletion)
	if !ok {
		t.Fatal("DeepSeek Anthropic route is missing")
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "deepseek-anthropic-request",
		AttemptID:      "deepseek-anthropic-attempt",
		Sequence:       1,
		ChannelID:      string(channel.DeepSeek),
		TargetKind:     string(target.ProviderKind),
		TargetConfig:   target.TargetConfig,
		RouteMode:      execution.RouteMode(mode),
		ClientProtocol: protocol.Anthropic,
		Operation:      execution.OperationChatCompletion,
		ClientModel:    "client-model",
		UpstreamModel:  "upstream-model",
		Method:         http.MethodPost,
		Path:           "/v1/messages",
		Header:         make(http.Header),
		Body:           []byte(`{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`),
		Credential:     execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"native-key"}`)),
	})
}

func listModelsAttempt(t *testing.T, channelID channel.ID, baseURL string) execution.AttemptSpec {
	t.Helper()
	registry := channel.NewRegistry()
	target, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.OpenAICompletions, execution.OperationListModels)
	if !ok {
		t.Fatalf("%s ListModels route is missing", channelID)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:      "models-path-request",
		AttemptID:      "models-path-attempt",
		Sequence:       1,
		ChannelID:      string(channelID),
		TargetKind:     string(target.ProviderKind),
		TargetConfig:   target.TargetConfig,
		RouteMode:      execution.RouteMode(mode),
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationListModels,
		Method:         http.MethodGet,
		Path:           "/v1/models",
		Header:         make(http.Header),
		Credential:     execution.NewCredentialSnapshot(1, 1, 1, []byte(`{"api_key":"native-key"}`)),
	})
}

func effectiveConfigForTest(
	t *testing.T,
	registry *channel.Registry,
	channelID channel.ID,
	params json.RawMessage,
) effectiveProviderConfig {
	t.Helper()
	resolved, err := registry.Resolve(channelID, params)
	if err != nil {
		t.Fatalf("Resolve(%s) error = %v", channelID, err)
	}
	config, err := buildEffectiveProviderConfig(resolved, true)
	if err != nil {
		t.Fatalf("buildEffectiveProviderConfig(%s) error = %v", channelID, err)
	}
	return config
}

type fakeManagedRuntime struct {
	shutdowns atomic.Int64
	done      chan struct{}
	once      sync.Once
}

func newFakeManagedRuntime() *fakeManagedRuntime {
	return &fakeManagedRuntime{done: make(chan struct{})}
}

func (*fakeManagedRuntime) Execute(context.Context, execution.AttemptSpec) execution.AttemptResult {
	return execution.AttemptResult{}
}

func (*fakeManagedRuntime) ExecuteStream(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult {
	return execution.StreamResult{}
}

func (runtime *fakeManagedRuntime) BeginShutdown() <-chan struct{} {
	runtime.once.Do(func() {
		runtime.shutdowns.Add(1)
		close(runtime.done)
	})
	return runtime.done
}
