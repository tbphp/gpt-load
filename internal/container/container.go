// Package container assembles the 2.0 dependency graph with dig.
package container

import (
	"context"
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
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
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
		state.NewManager,
		state.NewCredentialRegistry,
		channel.NewRegistry,
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
		func(registry *channel.Registry) (*bifrostexecutor.Runtime, error) {
			return bifrostexecutor.NewRuntime(context.Background(), registry)
		},
		func(runtime *bifrostexecutor.Runtime) execution.Executor { return runtime },
		func(runtime *bifrostexecutor.Runtime) app.ExecutionRuntime { return runtime },
		gateway.NewExecutionForwarder,
		func(forwarder *gateway.ExecutionForwarder) gateway.AttemptForwarder { return forwarder },
		gateway.NewHandlerWithLifecycle,
		control.NewService,
		control.NewCatalogSyncCoordinator,
		func(service *control.Service) app.StartupBootstrap { return service },
		func(service *control.Service) app.StartupRecovery { return service },
		control.NewServer,
		newHTTPRegistry,
		func(
			db *gorm.DB,
			manager *state.Manager,
			registry *state.CredentialRegistry,
			channelRegistry *channel.Registry,
			encryptionService encryption.Service,
		) app.RuntimeStateLoader {
			return stateloader.NewWithCredentialValidation(
				db,
				manager,
				registry,
				channelRegistry,
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
