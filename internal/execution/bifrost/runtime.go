// Package bifrost adapts the official Bifrost Core SDK to the neutral execution contract.
package bifrost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	core "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

const (
	providerConcurrency = 32
	providerBufferSize  = 256
	// Keep the SDK transport timeout at the largest whole-second value that
	// fits time.Duration so each attempt context remains the effective owner.
	providerTimeoutSecs              = 9_223_372_036
	defaultMaxUnaryResponseBodyBytes = execution.DefaultUnaryResponseBodyLimitBytes
)

var errKeyPoolDisabled = errors.New("Bifrost key pool is disabled; DirectKey is required")

// Runtime owns one long-lived Bifrost Core and executes attempts with explicit credentials.
type Runtime struct {
	core                      *core.Bifrost
	account                   *directAccount
	registry                  *channel.Registry
	allowPrivate              bool
	maxUnaryResponseBodyBytes int64
	stateMu                   sync.Mutex
	shutdownOnce              sync.Once
	shutdownDone              chan struct{}
	closed                    atomic.Bool
	fixedConfig               *effectiveProviderConfig
	logger                    schemas.Logger
}

func (r *Runtime) unaryResponseBodyLimit(spec execution.AttemptSpec) int64 {
	if r != nil && r.maxUnaryResponseBodyBytes > 0 &&
		r.maxUnaryResponseBodyBytes != defaultMaxUnaryResponseBodyBytes {
		return r.maxUnaryResponseBodyBytes
	}
	return execution.UnaryResponseBodyLimit(spec.ClientProtocol)
}

func newConfiguredRuntime(
	ctx context.Context,
	options runtimeOptions,
	registry *channel.Registry,
	config effectiveProviderConfig,
) (*Runtime, error) {
	runtime, err := newRuntimeShell(options, registry)
	if err != nil {
		return nil, err
	}
	runtime.account = newDirectAccount()
	runtime.account.setConfig(config.provider, cloneProviderConfig(config.providerConfig))
	cloned := cloneEffectiveProviderConfig(config)
	runtime.fixedConfig = &cloned
	if err := runtime.Start(ctx); err != nil {
		return nil, err
	}
	return runtime, nil
}

type runtimeOptions struct {
	allowPrivateNetwork       bool
	maxUnaryResponseBodyBytes int64
	logger                    schemas.Logger
}

func normalizeRuntimeOptions(options runtimeOptions) runtimeOptions {
	if options.logger == nil {
		options.logger = core.NewNoOpLogger()
	}
	return options
}

// NewRuntime initializes the production runtime manager. Individual Bifrost
// cores are created lazily for effective provider configurations.
func NewRuntime(ctx context.Context, registry *channel.Registry) (*RuntimeManager, error) {
	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
	if err != nil {
		return nil, err
	}
	if err := manager.Start(ctx); err != nil {
		return nil, err
	}
	return manager, nil
}

// NewManagedRuntime creates an unstarted production runtime manager. The
// application lifecycle must call Start before accepting requests.
func NewManagedRuntime(registry *channel.Registry) (*RuntimeManager, error) {
	return newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
}

func newRuntimeShell(options runtimeOptions, registry *channel.Registry) (*Runtime, error) {
	options = normalizeRuntimeOptions(options)
	if registry == nil {
		return nil, fmt.Errorf("initialize execution runtime: channel registry is required")
	}
	maxUnaryResponseBodyBytes := options.maxUnaryResponseBodyBytes
	if maxUnaryResponseBodyBytes <= 0 {
		maxUnaryResponseBodyBytes = defaultMaxUnaryResponseBodyBytes
	}
	return &Runtime{
		account:                   newDirectAccount(),
		registry:                  registry,
		allowPrivate:              options.allowPrivateNetwork,
		maxUnaryResponseBodyBytes: maxUnaryResponseBodyBytes,
		shutdownDone:              make(chan struct{}),
		logger:                    options.logger,
	}, nil
}

// Start initializes Bifrost after the application has restored its runtime
// state and before the HTTP listener accepts requests. It is idempotent after
// a successful start and cannot restart a stopped runtime.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("start execution runtime: runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.closed.Load() {
		return errors.New("start execution runtime: runtime is shut down")
	}
	if r.core != nil {
		return nil
	}
	bifrostCore, err := core.Init(ctx, schemas.BifrostConfig{
		Account:         r.account,
		LLMPlugins:      []schemas.LLMPlugin{},
		MCPPlugins:      []schemas.MCPPlugin{},
		Logger:          r.logger,
		InitialPoolSize: 64,
	})
	if err != nil {
		return fmt.Errorf("initialize execution runtime: %w", err)
	}
	r.core = bifrostCore
	return nil
}

// BeginShutdown rejects new execution and starts releasing the shared SDK
// runtime without waiting for provider workers that may be blocked in the SDK.
func (r *Runtime) BeginShutdown() <-chan struct{} {
	if r == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	r.shutdownOnce.Do(func() {
		r.stateMu.Lock()
		r.closed.Store(true)
		bifrostCore := r.core
		r.stateMu.Unlock()
		if bifrostCore == nil {
			close(r.shutdownDone)
			return
		}
		go func() {
			bifrostCore.Shutdown()
			close(r.shutdownDone)
		}()
	})
	return r.shutdownDone
}

// Shutdown releases the shared SDK runtime and waits for completion. Direct
// runtime owners may use it outside the process-level bounded shutdown path.
func (r *Runtime) Shutdown() {
	<-r.BeginShutdown()
}

func providerConfig(baseURL string, custom bool, baseProvider schemas.ModelProvider, allowPrivateNetwork bool) *schemas.ProviderConfig {
	networkBaseURL := baseURL
	if custom && baseProvider == schemas.OpenAI {
		// OpenAI passthrough always appends /v1 before the request path. Channel
		// base_url is a complete API prefix, so remove only that exact suffix.
		networkBaseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	config := &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        networkBaseURL,
			DefaultRequestTimeoutInSeconds: providerTimeoutSecs,
			MaxRetries:                     0,
			StreamIdleTimeoutInSeconds:     120,
			AllowPrivateNetwork:            allowPrivateNetwork,
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: providerConcurrency,
			BufferSize:  providerBufferSize,
		},
		SendBackRawRequest:      false,
		SendBackRawResponse:     false,
		StoreRawRequestResponse: false,
	}
	if custom {
		customConfig := &schemas.CustomProviderConfig{
			BaseProviderType: baseProvider,
			AllowedRequests: &schemas.AllowedRequests{
				ListModels:           true,
				ChatCompletion:       true,
				ChatCompletionStream: true,
				Passthrough:          true,
				PassthroughStream:    true,
			},
		}
		if baseProvider == schemas.OpenAI {
			// Channel base_url is the complete API prefix (commonly ending in /v1),
			// while Bifrost's OpenAI default assumes a provider origin and appends /v1.
			customConfig.RequestPathOverrides = map[schemas.RequestType]string{
				schemas.ChatCompletionRequest:       baseURL + "/chat/completions",
				schemas.ChatCompletionStreamRequest: baseURL + "/chat/completions",
			}
		}
		config.CustomProviderConfig = customConfig
	}
	return config
}

type directAccount struct {
	mu           sync.RWMutex
	configs      map[schemas.ModelProvider]*schemas.ProviderConfig
	keyPoolCalls atomic.Uint64
}

func newDirectAccount() *directAccount {
	return &directAccount{configs: make(map[schemas.ModelProvider]*schemas.ProviderConfig)}
}

func (a *directAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	providers := make([]schemas.ModelProvider, 0, len(a.configs))
	for provider := range a.configs {
		providers = append(providers, provider)
	}
	return providers, nil
}

func (a *directAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	a.keyPoolCalls.Add(1)
	return nil, errKeyPoolDisabled
}

func (a *directAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	config, ok := a.configs[provider]
	if !ok {
		return nil, fmt.Errorf("provider config is not registered")
	}
	return cloneProviderConfig(config), nil
}

func (a *directAccount) setConfig(provider schemas.ModelProvider, config *schemas.ProviderConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.configs[provider]; !exists {
		a.configs[provider] = cloneProviderConfig(config)
	}
}

func cloneProviderConfig(source *schemas.ProviderConfig) *schemas.ProviderConfig {
	if source == nil {
		return nil
	}
	clone := *source
	clone.NetworkConfig.ExtraHeaders = cloneStringMap(source.NetworkConfig.ExtraHeaders)
	clone.NetworkConfig.BetaHeaderOverrides = cloneBoolMap(source.NetworkConfig.BetaHeaderOverrides)
	if source.ProxyConfig != nil {
		proxy := *source.ProxyConfig
		proxy.URL = cloneSecretVar(source.ProxyConfig.URL)
		proxy.Username = cloneSecretVar(source.ProxyConfig.Username)
		proxy.Password = cloneSecretVar(source.ProxyConfig.Password)
		proxy.CACertPEM = cloneSecretVar(source.ProxyConfig.CACertPEM)
		clone.ProxyConfig = &proxy
	}
	if source.CustomProviderConfig != nil {
		custom := *source.CustomProviderConfig
		if source.CustomProviderConfig.AllowedRequests != nil {
			allowed := *source.CustomProviderConfig.AllowedRequests
			custom.AllowedRequests = &allowed
		}
		custom.RequestPathOverrides = make(map[schemas.RequestType]string, len(source.CustomProviderConfig.RequestPathOverrides))
		for key, value := range source.CustomProviderConfig.RequestPathOverrides {
			custom.RequestPathOverrides[key] = value
		}
		clone.CustomProviderConfig = &custom
	}
	if source.OpenAIConfig != nil {
		openAIConfig := *source.OpenAIConfig
		clone.OpenAIConfig = &openAIConfig
	}
	return &clone
}

func cloneSecretVar(source *schemas.SecretVar) *schemas.SecretVar {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	clone := make(map[string]bool, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

var _ execution.Executor = (*Runtime)(nil)
var _ schemas.Account = (*directAccount)(nil)
