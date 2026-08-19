// Package container assembles the 2.0 dependency graph with dig.
package container

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/control"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	bifrostexecutor "gpt-load/internal/execution/bifrost"
	cpaexecutor "gpt-load/internal/execution/cpa"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/provideradapter"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/releasecheck"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/subscription"
	subscriptionproviders "gpt-load/internal/subscription/providers"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/webui"

	"github.com/sirupsen/logrus"
)

// BuildContainer creates the 2.0 runtime foundation dependency graph.
func BuildContainer() (*dig.Container, error) {
	dependencyContainer := dig.New()

	providers := []any{
		config.Load,
		func(cfg *config.Config) (encryption.Service, error) {
			return encryption.NewServiceWithKeyFile(cfg.EncryptionKey, cfg.DataDir)
		},
		func(cfg *config.Config) (*gorm.DB, error) {
			db, err := storage.OpenWithSource(cfg.DatabaseDSN, cfg.DatabaseMetadata.Source)
			if err == nil {
				logrus.WithField("event", "startup.database_open").Info("database opened")
			}
			return db, err
		},
		httplifecycle.NewCoordinator,
		app.NewEngineWithLifecycle,
		webui.NewServer,
		state.NewCredentialRegistry,
		channel.CompileRegistry,
		control.NewPriceRuntime,
		control.NewCatalogBootstrap,
		func(bootstrap *control.CatalogBootstrap) *catalog.Runtime { return bootstrap.Runtime },
		health.NewStatsStore,
		health.NewMutationCoordinator,
		ratelimit.NewAccessKeyRPM,
		func(limiter *ratelimit.AccessKeyRPM) gateway.AccessKeyRPMLimiter {
			return limiter
		},
		func(manager *state.Manager) requestlog.RetentionPolicyProvider {
			return retentionSnapshotProvider{manager: manager}
		},
		func(runtime *control.PriceRuntime) gateway.PriceTableProvider {
			return priceRuntimeProvider{runtime: runtime}
		},
		requestlog.NewService,
		func(service *requestlog.Service) telemetry.RequestLogSink {
			return service
		},
		func(service *requestlog.Service) control.RequestLogReader {
			return service
		},
		func(service *requestlog.Service) control.UsageStatReader {
			return service
		},
		func(service *requestlog.Service) control.HomeStatisticsReader {
			return service
		},
		func(service *requestlog.Service) control.RequestLogStatsReader {
			return service
		},
		func(service *requestlog.Service) control.RequestLogCleaner {
			return service
		},
		func(service *requestlog.Service) app.RequestLogRuntime {
			return service
		},
		func(
			cfg *config.Config,
			registry *state.CredentialRegistry,
			stats *health.StatsStore,
		) app.RuntimeStateCheckpoint {
			return app.NewFileRuntimeStateCheckpoint(cfg.DataDir, registry, stats)
		},
		control.NewRuntime,
		func(runtime *control.Runtime) app.ControlRuntime { return runtime },
		httpclient.NewHTTPClientManager,
		releasecheck.NewClient,
		releasecheck.NewChecker,
		func(manager *httpclient.HTTPClientManager) *catalog.Client {
			return catalog.NewClient(manager, "")
		},
		redact.New,
		func(manager *httpclient.HTTPClientManager) *http.Client {
			return manager.GetClient(&httpclient.Config{
				ConnectTimeout:        15 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,
				ResponseHeaderTimeout: 120 * time.Second,
				DisableCompression:    true,
				WriteBufferSize:       32 * 1024,
				ReadBufferSize:        32 * 1024,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   15 * time.Second,
				ExpectContinueTimeout: time.Second,
			})
		},
		dialect.NewOpenAI,
		dialect.NewOpenAIResponses,
		dialect.NewAnthropic,
		dialect.NewGemini,
		func(
			openAI *dialect.OpenAI,
			openAIResponses *dialect.OpenAIResponses,
			anthropic *dialect.Anthropic,
			gemini *dialect.Gemini,
		) dialect.Set {
			return dialect.NewSet(openAI, openAIResponses, anthropic, gemini)
		},
		func(registry *channel.Registry) (*bifrostexecutor.RuntimeManager, error) {
			return bifrostexecutor.NewManagedRuntime(registry)
		},
		func(adapters *provideradapter.Registry) *state.Manager {
			manager := state.NewManager()
			manager.SetSnapshotReconciler(providerRuntimeSnapshotReconciler{adapters: adapters})
			return manager
		},
		newSubscriptionRuntime,
		subscription.NewCredentialManager,
		cpaexecutor.NewAdapter,
		newProviderAdapterRegistry,
		func(registry *provideradapter.Registry) execution.Executor { return registry },
		func(runtime *bifrostexecutor.RuntimeManager) app.ExecutionRuntime { return runtime },
		gateway.NewExecutionForwarder,
		func(forwarder *gateway.ExecutionForwarder) gateway.AttemptForwarder { return forwarder },
		gateway.NewHandlerWithLifecycle,
		control.NewService,
		control.NewCatalogSyncCoordinator,
		func(service *control.Service) app.StartupBootstrap { return service },
		func(service *control.Service) app.StartupRecovery { return service },
		func(
			cfg *config.Config,
			service *control.Service,
			checker *releasecheck.Checker,
		) *control.Server {
			return control.NewServerWithReleaseUpdateReader(cfg, service, checker)
		},
		newHTTPRegistry,
		func(
			db *gorm.DB,
			manager *state.Manager,
			registry *state.CredentialRegistry,
			channelRegistry *channel.Registry,
			subscriptions *subscriptionruntime.Runtime,
			encryptionService encryption.Service,
		) app.RuntimeStateLoader {
			return stateloader.NewWithCredentialValidation(
				db,
				manager,
				registry,
				channelRegistry,
				subscriptions,
				encryptionService,
			)
		},
		app.NewApp,
	}

	for _, provider := range providers {
		if err := dependencyContainer.Provide(provider); err != nil {
			return nil, err
		}
	}
	if err := dependencyContainer.Invoke(func(
		engine *gin.Engine,
		registry *httproute.Registry,
	) error {
		return registry.Bind(engine)
	}); err != nil {
		return nil, fmt.Errorf("register HTTP routes: %w", err)
	}
	return dependencyContainer, nil
}

type providerRuntimeSnapshotReconciler struct {
	adapters *provideradapter.Registry
}

func newSubscriptionRuntime(registry *channel.Registry) (*subscriptionruntime.Runtime, error) {
	return subscriptionruntime.NewRuntime(registry, subscriptionproviders.Implementations()...)
}

func newProviderAdapterRegistry(
	channels *channel.Registry,
	bifrost *bifrostexecutor.RuntimeManager,
	cpa *cpaexecutor.Adapter,
) (*provideradapter.Registry, error) {
	bindings := []provideradapter.Binding{
		{ProviderKind: channel.ProviderOpenAI, Adapter: bifrost},
		{ProviderKind: channel.ProviderAnthropic, Adapter: bifrost},
		{ProviderKind: channel.ProviderGemini, Adapter: bifrost},
		{ProviderKind: channel.ProviderOpenAICompatible, Adapter: bifrost},
		{ProviderKind: channel.ProviderAzureOpenAI, Adapter: bifrost},
		{ProviderKind: channel.ProviderAWSBedrock, Adapter: bifrost},
		{ProviderKind: channel.ProviderGoogleVertex, Adapter: bifrost},
		{ProviderKind: channel.ProviderDeepSeek, Adapter: bifrost},
		{ProviderKind: channel.ProviderOpenRouter, Adapter: bifrost},
		{ProviderKind: channel.ProviderGroq, Adapter: bifrost},
		{ProviderKind: channel.ProviderXAI, Adapter: bifrost},
		{ProviderKind: channel.ProviderCodex, Adapter: cpa},
		{ProviderKind: channel.ProviderClaude, Adapter: cpa},
		{ProviderKind: channel.ProviderAntigravity, Adapter: cpa},
		{ProviderKind: channel.ProviderGrok, Adapter: cpa},
	}
	return provideradapter.NewRegistry(channels, bindings)
}

func (reconciler providerRuntimeSnapshotReconciler) ReconcileConfigSnapshot(snapshot *state.ConfigSnapshot) error {
	if reconciler.adapters == nil {
		return fmt.Errorf("reconcile provider runtimes: adapter registry is unavailable")
	}
	targets := make([]channel.ResolvedTarget, 0)
	if snapshot != nil {
		targets = make([]channel.ResolvedTarget, 0, len(snapshot.Groups))
		for _, group := range snapshot.Groups {
			targets = append(targets, group.ResolvedTarget)
		}
	}
	return reconciler.adapters.ReconcileTargets(targets)
}

func newHTTPRegistry(
	gatewayHandler *gateway.Handler,
	controlServer *control.Server,
	webUIServer *webui.Server,
) (*httproute.Registry, error) {
	return httproute.NewRegistry(
		app.HTTPModule(),
		controlServer.HTTPModule(),
		gatewayHandler.HTTPModule(),
		webUIServer.HTTPModule(),
	)
}

type priceRuntimeProvider struct {
	runtime *control.PriceRuntime
}

func (provider priceRuntimeProvider) Load() *pricing.Table {
	return provider.runtime.Load()
}
