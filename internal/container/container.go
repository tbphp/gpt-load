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
	"gpt-load/internal/control"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/webui"
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
			return storage.Open(cfg.DatabaseDSN)
		},
		app.NewEngine,
		webui.NewServer,
		state.NewManager,
		state.NewKeyRegistry,
		health.NewStatsStore,
		control.NewRuntime,
		func(runtime *control.Runtime) app.ControlRuntime { return runtime },
		httpclient.NewHTTPClientManager,
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
		dialect.NewAnthropic,
		dialect.NewGemini,
		func(
			openAI *dialect.OpenAI,
			anthropic *dialect.Anthropic,
			gemini *dialect.Gemini,
		) dialect.Set {
			return dialect.NewSet(openAI, anthropic, gemini)
		},
		gateway.NewForwarder,
		func(forwarder *gateway.Forwarder) gateway.AttemptForwarder { return forwarder },
		gateway.NewHandler,
		control.NewService,
		control.NewServer,
		func(db *gorm.DB, manager *state.Manager, registry *state.KeyRegistry) app.RuntimeStateLoader {
			return stateloader.New(db, manager, registry)
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
		gatewayHandler *gateway.Handler,
		controlServer *control.Server,
		webUIServer *webui.Server,
	) {
		gatewayHandler.RegisterRoutes(engine)
		controlServer.RegisterRoutes(engine)
		webUIServer.RegisterRoutes(engine)
	}); err != nil {
		return nil, fmt.Errorf("register HTTP routes: %w", err)
	}
	return dependencyContainer, nil
}
