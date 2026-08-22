package bifrost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
)

type effectiveProviderConfig struct {
	provider              schemas.ModelProvider
	providerConfig        *schemas.ProviderConfig
	canonical             []byte
	fingerprint           string
	baseCanonical         []byte
	baseFingerprint       string
	credentialPartitionID uint
	targetBaseURL         string
	custom                bool
}

func buildEffectiveProviderConfig(
	resolved channel.ResolvedTarget,
	allowPrivateNetwork bool,
) (effectiveProviderConfig, error) {
	provider, baseURL, custom, err := resolveSDKProviderConfig(resolved)
	if err != nil {
		return effectiveProviderConfig{}, err
	}
	config := buildProviderConfig(provider, baseURL, custom, schemas.OpenAI, allowPrivateNetwork)
	return newEffectiveProviderConfig(provider, baseURL, custom, config)
}

func buildProviderConfig(
	provider schemas.ModelProvider,
	baseURL string,
	custom bool,
	baseProvider schemas.ModelProvider,
	allowPrivateNetwork bool,
) *schemas.ProviderConfig {
	config := providerConfig(baseURL, custom, baseProvider, allowPrivateNetwork)
	if !custom {
		config = providerConfig(baseURL, false, provider, allowPrivateNetwork)
	}
	config.CheckAndSetDefaults()
	return config
}

func newEffectiveProviderConfig(
	provider schemas.ModelProvider,
	baseURL string,
	custom bool,
	config *schemas.ProviderConfig,
) (effectiveProviderConfig, error) {
	canonical, err := json.Marshal(struct {
		Provider schemas.ModelProvider   `json:"provider"`
		Config   *schemas.ProviderConfig `json:"config"`
	}{Provider: provider, Config: config})
	if err != nil {
		return effectiveProviderConfig{}, fmt.Errorf("encode provider runtime config: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return effectiveProviderConfig{
		provider:       provider,
		providerConfig: cloneProviderConfig(config),
		canonical:      canonical,
		fingerprint:    hex.EncodeToString(digest[:]),
		targetBaseURL:  baseURL,
		custom:         custom,
	}, nil
}

func buildEffectiveProviderConfigForAttempt(
	resolved channel.ResolvedTarget,
	spec execution.AttemptSpec,
	allowPrivateNetwork bool,
) (effectiveProviderConfig, error) {
	base, err := buildEffectiveProviderConfig(resolved, allowPrivateNetwork)
	if err != nil {
		return base, err
	}
	if resolved.ProviderKind != channel.ProviderDeepSeek ||
		spec.ClientProtocol != protocol.OpenAIResponses ||
		(spec.Operation != execution.OperationResponsesCreate && spec.Operation != execution.OperationProbe) ||
		channel.RouteMode(spec.RouteMode) != channel.RouteNative {
		return applyAttemptProxy(base, spec.Proxy)
	}

	provider := customProviderKey(schemas.OpenAI, base.targetBaseURL)
	config := buildProviderConfig(provider, base.targetBaseURL, true, schemas.OpenAI, allowPrivateNetwork)
	config.CustomProviderConfig.AllowedRequests.Responses = true
	config.CustomProviderConfig.AllowedRequests.ResponsesStream = true
	config.CustomProviderConfig.AllowedRequests.ChatCompletion = false
	config.CustomProviderConfig.AllowedRequests.ChatCompletionStream = false
	config.CustomProviderConfig.RequestPathOverrides[schemas.ResponsesRequest] = base.targetBaseURL + "/responses"
	config.CustomProviderConfig.RequestPathOverrides[schemas.ResponsesStreamRequest] = base.targetBaseURL + "/responses"
	config.CheckAndSetDefaults()
	base, err = newEffectiveProviderConfig(provider, base.targetBaseURL, true, config)
	if err != nil {
		return effectiveProviderConfig{}, err
	}
	return applyAttemptProxy(base, spec.Proxy)
}

func applyAttemptProxy(
	base effectiveProviderConfig,
	effective outboundproxy.Effective,
) (effectiveProviderConfig, error) {
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return effectiveProviderConfig{}, fmt.Errorf("normalize attempt proxy: %w", err)
	}
	if effective.Config.Mode == outboundproxy.ModeDirect {
		return base, nil
	}
	config := cloneProviderConfig(base.providerConfig)
	proxy := &schemas.ProxyConfig{}
	switch effective.Config.Mode {
	case outboundproxy.ModeEnvironment:
		proxy.Type = schemas.EnvProxy
	case outboundproxy.ModeCustom:
		endpoint, parseErr := url.Parse(effective.Config.URL)
		if parseErr != nil || endpoint.Host == "" {
			return effectiveProviderConfig{}, fmt.Errorf("normalize attempt proxy: invalid endpoint")
		}
		switch endpoint.Scheme {
		case "http":
			proxy.Type = schemas.HTTPProxy
		case "socks5":
			proxy.Type = schemas.Socks5Proxy
		default:
			return effectiveProviderConfig{}, fmt.Errorf("normalize attempt proxy: unsupported transport")
		}
		username, password := "", ""
		if endpoint.User != nil {
			username = endpoint.User.Username()
			password, _ = endpoint.User.Password()
		}
		endpoint.User = nil
		proxy.URL = schemas.NewSecretVar(endpoint.String())
		proxy.Username = schemas.NewSecretVar(username)
		proxy.Password = schemas.NewSecretVar(password)
	default:
		return effectiveProviderConfig{}, fmt.Errorf("normalize attempt proxy: unsupported mode")
	}
	config.ProxyConfig = proxy
	partitioned, err := newEffectiveProviderConfig(base.provider, base.targetBaseURL, base.custom, config)
	if err != nil {
		return effectiveProviderConfig{}, err
	}
	partitioned.baseCanonical = append([]byte(nil), base.canonical...)
	partitioned.baseFingerprint = base.fingerprint
	return partitioned, nil
}

func buildDeepSeekResponsesConfig(
	resolved channel.ResolvedTarget,
	allowPrivateNetwork bool,
) (effectiveProviderConfig, error) {
	return buildEffectiveProviderConfigForAttempt(
		resolved,
		execution.AttemptSpec{
			ClientProtocol: protocol.OpenAIResponses,
			Operation:      execution.OperationResponsesCreate,
			RouteMode:      execution.RouteNative,
		},
		allowPrivateNetwork,
	)
}

func resolveSDKProviderConfig(resolved channel.ResolvedTarget) (schemas.ModelProvider, string, bool, error) {
	if preset, ok := sdkProviderSpecFor(resolved.ProviderKind); ok {
		baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
		if err != nil {
			return "", "", false, err
		}
		if !configured {
			baseURL, _, err = sdkDefaultBaseURL(resolved.ProviderKind)
			if err != nil {
				return "", "", false, err
			}
		}
		return preset.provider, baseURL, false, nil
	}
	if resolved.ProviderKind != channel.ProviderOpenAICompatible {
		return "", "", false, fmt.Errorf("unsupported provider kind %q", resolved.ProviderKind)
	}
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		return "", "", false, fmt.Errorf("compatible provider base URL is required")
	}
	return customProviderKey(schemas.OpenAI, baseURL), baseURL, true, nil
}

func targetBaseURL(raw json.RawMessage) (string, bool, error) {
	var target struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(raw, &target); err != nil {
		return "", false, fmt.Errorf("decode provider target: %w", err)
	}
	if target.BaseURL == "" {
		return "", false, nil
	}
	return target.BaseURL, true, nil
}

type managedProviderRuntime interface {
	execution.Executor
	BeginShutdown() <-chan struct{}
}

type managedRuntimeFactory func(context.Context, effectiveProviderConfig) (managedProviderRuntime, error)

type runtimeLease struct {
	runtime managedProviderRuntime
	release func()
	once    sync.Once
}

func (lease *runtimeLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		if lease.release != nil {
			lease.release()
		}
	})
}

type runtimeManager struct {
	factory managedRuntimeFactory

	mu             sync.Mutex
	entries        map[string]*runtimeEntry
	active         map[string][]byte
	activeKnown    bool
	closed         bool
	shuttingDown   int
	shutdownDone   chan struct{}
	shutdownClosed atomic.Bool
}

// RuntimeManager owns lazily-created Bifrost cores keyed by canonical provider
// construction config, with credential partitions only where the SDK caches
// credential-derived objects inside a Core.
type RuntimeManager struct {
	registry *channel.Registry
	options  runtimeOptions
	pool     *runtimeManager

	mu      sync.Mutex
	started bool
	closed  bool
	rootCtx context.Context
	cancel  context.CancelFunc
}

func newRuntimeManager(options runtimeOptions, registry *channel.Registry) (*RuntimeManager, error) {
	options = normalizeRuntimeOptions(options)
	if _, err := newRuntimeShell(options, registry); err != nil {
		return nil, err
	}
	manager := &RuntimeManager{
		registry: registry,
		options:  options,
	}
	manager.pool = newRuntimeManagerPool(func(_ context.Context, config effectiveProviderConfig) (managedProviderRuntime, error) {
		manager.mu.Lock()
		rootCtx := manager.rootCtx
		manager.mu.Unlock()
		if rootCtx == nil {
			return nil, fmt.Errorf("provider runtime manager is not started")
		}
		return newConfiguredRuntime(rootCtx, manager.options, manager.registry, config)
	})
	return manager, nil
}

type runtimeEntry struct {
	config       effectiveProviderConfig
	runtime      managedProviderRuntime
	ready        chan struct{}
	initializing bool
	initErr      error
	refs         int
	retiring     bool
}

func newRuntimeManagerPool(factory managedRuntimeFactory) *runtimeManager {
	return &runtimeManager{
		factory:      factory,
		entries:      make(map[string]*runtimeEntry),
		active:       make(map[string][]byte),
		shutdownDone: make(chan struct{}),
	}
}

func (manager *runtimeManager) acquire(
	ctx context.Context,
	config effectiveProviderConfig,
) (*runtimeLease, error) {
	if manager == nil || manager.factory == nil {
		return nil, fmt.Errorf("provider runtime manager is unavailable")
	}
	for {
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return nil, fmt.Errorf("provider runtime manager is shut down")
		}
		if entry, exists := manager.entries[config.fingerprint]; exists {
			if !bytes.Equal(entry.config.canonical, config.canonical) {
				manager.mu.Unlock()
				return nil, fmt.Errorf("provider runtime fingerprint collision")
			}
			if entry.initializing {
				ready := entry.ready
				manager.mu.Unlock()
				select {
				case <-ready:
					manager.mu.Lock()
					initErr := entry.initErr
					manager.mu.Unlock()
					if initErr != nil {
						return nil, initErr
					}
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			entry.refs++
			manager.mu.Unlock()
			return manager.lease(entry), nil
		}
		entry := &runtimeEntry{
			config:       cloneEffectiveProviderConfig(config),
			ready:        make(chan struct{}),
			initializing: true,
			refs:         1,
			retiring:     manager.activeKnown && !manager.configIsActive(config),
		}
		manager.entries[config.fingerprint] = entry
		manager.mu.Unlock()

		runtime, err := manager.factory(ctx, cloneEffectiveProviderConfig(config))
		manager.mu.Lock()
		entry.initializing = false
		if err != nil {
			entry.initErr = err
			delete(manager.entries, config.fingerprint)
			close(entry.ready)
			manager.maybeCloseShutdownLocked()
			manager.mu.Unlock()
			return nil, err
		}
		entry.runtime = runtime
		close(entry.ready)
		manager.mu.Unlock()
		return manager.lease(entry), nil
	}
}

func (manager *runtimeManager) lease(entry *runtimeEntry) *runtimeLease {
	return &runtimeLease{
		runtime: entry.runtime,
		release: func() { manager.release(entry) },
	}
}

func (manager *runtimeManager) release(entry *runtimeEntry) {
	manager.mu.Lock()
	if entry.refs > 0 {
		entry.refs--
	}
	retired := manager.retireEntryLocked(entry)
	manager.mu.Unlock()
	manager.startShutdown(retired)
}

func (manager *runtimeManager) reconcile(configs []effectiveProviderConfig) {
	if manager == nil {
		return
	}
	active := make(map[string][]byte, len(configs))
	for _, config := range configs {
		active[config.fingerprint] = append([]byte(nil), config.canonical...)
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.activeKnown = true
	manager.active = active
	retired := make([]managedProviderRuntime, 0)
	for _, entry := range manager.entries {
		entry.retiring = !manager.configIsActive(entry.config)
		if runtime := manager.retireEntryLocked(entry); runtime != nil {
			retired = append(retired, runtime)
		}
	}
	manager.mu.Unlock()
	for _, runtime := range retired {
		manager.startShutdown(runtime)
	}
}

func (manager *runtimeManager) beginShutdown() <-chan struct{} {
	if manager == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	manager.mu.Lock()
	manager.closed = true
	manager.activeKnown = true
	manager.active = map[string][]byte{}
	retired := make([]managedProviderRuntime, 0, len(manager.entries))
	for _, entry := range manager.entries {
		entry.retiring = true
		if runtime := manager.retireEntryLocked(entry); runtime != nil {
			retired = append(retired, runtime)
		}
	}
	manager.maybeCloseShutdownLocked()
	done := manager.shutdownDone
	manager.mu.Unlock()
	for _, runtime := range retired {
		manager.startShutdown(runtime)
	}
	return done
}

func (manager *runtimeManager) configIsActive(config effectiveProviderConfig) bool {
	fingerprint := config.fingerprint
	canonical := config.canonical
	if config.baseFingerprint != "" {
		fingerprint = config.baseFingerprint
		canonical = config.baseCanonical
	}
	activeCanonical, exists := manager.active[fingerprint]
	return exists && bytes.Equal(activeCanonical, canonical)
}

func (manager *runtimeManager) retireCredential(credentialID uint) {
	if manager == nil || credentialID == 0 {
		return
	}
	manager.mu.Lock()
	retired := make([]managedProviderRuntime, 0)
	for _, entry := range manager.entries {
		if entry.config.credentialPartitionID != credentialID {
			continue
		}
		entry.retiring = true
		if runtime := manager.retireEntryLocked(entry); runtime != nil {
			retired = append(retired, runtime)
		}
	}
	manager.mu.Unlock()
	for _, runtime := range retired {
		manager.startShutdown(runtime)
	}
}

func (manager *runtimeManager) retireEntryLocked(entry *runtimeEntry) managedProviderRuntime {
	if entry == nil || entry.initializing || !entry.retiring || entry.refs != 0 || entry.runtime == nil {
		return nil
	}
	current, exists := manager.entries[entry.config.fingerprint]
	if !exists || current != entry {
		return nil
	}
	delete(manager.entries, entry.config.fingerprint)
	manager.shuttingDown++
	return entry.runtime
}

func (manager *runtimeManager) startShutdown(runtime managedProviderRuntime) {
	if runtime == nil {
		return
	}
	done := runtime.BeginShutdown()
	go func() {
		<-done
		manager.mu.Lock()
		manager.shuttingDown--
		manager.maybeCloseShutdownLocked()
		manager.mu.Unlock()
	}()
}

func (manager *runtimeManager) maybeCloseShutdownLocked() {
	if !manager.closed || len(manager.entries) != 0 || manager.shuttingDown != 0 || manager.shutdownClosed.Load() {
		return
	}
	manager.shutdownClosed.Store(true)
	close(manager.shutdownDone)
}

func cloneEffectiveProviderConfig(source effectiveProviderConfig) effectiveProviderConfig {
	clone := source
	clone.providerConfig = cloneProviderConfig(source.providerConfig)
	clone.canonical = append([]byte(nil), source.canonical...)
	clone.baseCanonical = append([]byte(nil), source.baseCanonical...)
	return clone
}

// Start enables lazy provider initialization. It does not create a Bifrost core.
func (manager *RuntimeManager) Start(ctx context.Context) error {
	if manager == nil {
		return fmt.Errorf("start provider runtime manager: manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return fmt.Errorf("start provider runtime manager: manager is shut down")
	}
	if manager.started {
		return nil
	}
	manager.rootCtx, manager.cancel = context.WithCancel(ctx)
	manager.started = true
	return nil
}

// Execute acquires exactly one runtime lease for the frozen attempt target.
func (manager *RuntimeManager) Execute(parent context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	config, failure := manager.configForAttempt(spec)
	if failure != nil {
		return *failure
	}
	lease, err := manager.pool.acquire(parent, config)
	if err != nil {
		return notSentUnaryFailure(execution.ErrorKindInternal, "initialize provider runtime")
	}
	defer lease.Release()
	return lease.runtime.Execute(parent, spec)
}

// ExecuteStream holds the runtime lease through the complete synchronous stream lifecycle.
func (manager *RuntimeManager) ExecuteStream(
	parent context.Context,
	spec execution.AttemptSpec,
	sink execution.StreamSink,
) execution.StreamResult {
	config, failure := manager.configForAttempt(spec)
	if failure != nil {
		return streamFromAttemptFailure(*failure)
	}
	lease, err := manager.pool.acquire(parent, config)
	if err != nil {
		return notSentStreamFailure(execution.ErrorKindInternal, "initialize provider runtime")
	}
	defer lease.Release()
	return lease.runtime.ExecuteStream(parent, spec, sink)
}

func (manager *RuntimeManager) configForAttempt(spec execution.AttemptSpec) (effectiveProviderConfig, *execution.AttemptResult) {
	if manager == nil || manager.registry == nil || manager.pool == nil {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "provider runtime manager is unavailable")
		return effectiveProviderConfig{}, &failure
	}
	manager.mu.Lock()
	started := manager.started
	closed := manager.closed
	manager.mu.Unlock()
	if !started || closed {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "provider runtime manager is not running")
		return effectiveProviderConfig{}, &failure
	}
	resolved, err := manager.registry.ResolveExecutionTarget(channel.ID(spec.ChannelID), spec.TargetConfig)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid channel target")
		return effectiveProviderConfig{}, &failure
	}
	config, err := buildEffectiveProviderConfigForAttempt(resolved, spec, manager.options.allowPrivateNetwork)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid provider runtime config")
		return effectiveProviderConfig{}, &failure
	}
	if resolved.ProviderKind == channel.ProviderAzureOpenAI {
		credential, credentialErr := manager.registry.ValidateCredential(
			channel.ID(spec.ChannelID),
			spec.Credential.Data(),
		)
		if credentialErr != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid credential")
			return effectiveProviderConfig{}, &failure
		}
		apiKey, _ := credential.Value("api_key")
		if apiKey != "" {
			return config, nil
		}
		config, err = partitionProviderRuntime(config, spec.Credential)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInternal, "partition provider runtime")
			return effectiveProviderConfig{}, &failure
		}
	}
	return config, nil
}

func partitionProviderRuntime(
	config effectiveProviderConfig,
	credential execution.CredentialSnapshot,
) (effectiveProviderConfig, error) {
	activeBaseCanonical := config.baseCanonical
	activeBaseFingerprint := config.baseFingerprint
	if activeBaseFingerprint == "" {
		activeBaseCanonical = config.canonical
		activeBaseFingerprint = config.fingerprint
	}
	canonical, err := json.Marshal(struct {
		Base               json.RawMessage `json:"base"`
		CredentialID       uint            `json:"credential_id"`
		IdentityGeneration uint64          `json:"identity_generation"`
	}{
		Base:               config.canonical,
		CredentialID:       credential.ID,
		IdentityGeneration: credential.IdentityGeneration,
	})
	if err != nil {
		return effectiveProviderConfig{}, fmt.Errorf("encode provider runtime partition: %w", err)
	}
	digest := sha256.Sum256(canonical)
	config.baseCanonical = append([]byte(nil), activeBaseCanonical...)
	config.baseFingerprint = activeBaseFingerprint
	config.credentialPartitionID = credential.ID
	config.canonical = canonical
	config.fingerprint = hex.EncodeToString(digest[:])
	return config, nil
}

// Reconcile marks the canonical configs referenced by the latest state snapshot as active.
func (manager *RuntimeManager) Reconcile(targets []channel.ResolvedTarget) error {
	if manager == nil || manager.pool == nil {
		return fmt.Errorf("reconcile provider runtimes: manager is unavailable")
	}
	configs := make([]effectiveProviderConfig, 0, len(targets)*2)
	for _, target := range targets {
		config, err := buildEffectiveProviderConfig(target, manager.options.allowPrivateNetwork)
		if err != nil {
			return fmt.Errorf("reconcile provider runtime %q: %w", target.ChannelID, err)
		}
		configs = append(configs, config)
		if target.ProviderKind == channel.ProviderDeepSeek {
			responsesMode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate)
			if ok && responsesMode == channel.RouteNative {
				responsesConfig, responsesErr := buildDeepSeekResponsesConfig(target, manager.options.allowPrivateNetwork)
				if responsesErr != nil {
					return fmt.Errorf("reconcile provider runtime %q Responses: %w", target.ChannelID, responsesErr)
				}
				configs = append(configs, responsesConfig)
			}
		}
	}
	manager.pool.reconcile(configs)
	return nil
}

// BeginShutdown rejects new acquisitions and begins bounded-independent Core shutdowns.
func (manager *RuntimeManager) BeginShutdown() <-chan struct{} {
	if manager == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	manager.mu.Lock()
	manager.closed = true
	if manager.cancel != nil {
		manager.cancel()
	}
	manager.mu.Unlock()
	return manager.pool.beginShutdown()
}

// Shutdown waits until all leased runtimes finish and their Core shutdowns return.
func (manager *RuntimeManager) Shutdown() {
	<-manager.BeginShutdown()
}

// RetireCredential retires provider runtimes that cache objects derived from
// the specified logical credential. In-flight leases finish before shutdown.
func (manager *RuntimeManager) RetireCredential(credentialID uint) {
	if manager == nil || manager.pool == nil {
		return
	}
	manager.pool.retireCredential(credentialID)
}

var _ execution.Executor = (*RuntimeManager)(nil)
