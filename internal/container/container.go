// Package container provides a dependency injection container for the application.
package container

import (
	"gpt-load/internal/app"
	"gpt-load/internal/channel"
	"gpt-load/internal/config"
	"gpt-load/internal/db"
	"gpt-load/internal/handler"
	"gpt-load/internal/handlers"
	"gpt-load/internal/httpclient"
	"gpt-load/internal/keypool"
	"gpt-load/internal/proxy"
	"gpt-load/internal/router"
	"gpt-load/internal/services"
	"gpt-load/internal/store"

	"go.uber.org/dig"
)

// BuildContainer creates a new dependency injection container and provides all the application's services.
func BuildContainer() (*dig.Container, error) {
	container := dig.New()

	// Infrastructure Services
	if err := container.Provide(config.NewManager); err != nil {
		return nil, err
	}
	if err := container.Provide(db.NewDB); err != nil {
		return nil, err
	}
	if err := container.Provide(config.NewSystemSettingsManager); err != nil {
		return nil, err
	}
	if err := container.Provide(store.NewStore); err != nil {
		return nil, err
	}
	if err := container.Provide(httpclient.NewHTTPClientManager); err != nil {
		return nil, err
	}
	if err := container.Provide(channel.NewFactory); err != nil {
		return nil, err
	}

	// Business Services
	if err := container.Provide(services.NewTaskService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewKeyManualValidationService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewKeyService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewKeyImportService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewLogService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewLogCleanupService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewRequestLogService); err != nil {
		return nil, err
	}
	if err := container.Provide(services.NewGroupManager); err != nil {
		return nil, err
	}
	// 提供ProviderManager而不是直接的KeyProvider
	if err := container.Provide(keypool.NewProviderManager); err != nil {
		return nil, err
	}
	// 提供一个适配器函数，将ProviderManager转换为KeyProvider接口
	if err := container.Provide(func(manager *keypool.ProviderManager) *keypool.KeyProvider {
		// 获取当前提供者，如果是EnhancedKeyProvider，则返回其内部的legacyProvider
		// 如果是传统KeyProvider，则直接返回
		if enhanced := manager.GetEnhancedProvider(); enhanced != nil {
			return enhanced.GetLegacyProvider()
		}
		return manager.GetLegacyProvider()
	}); err != nil {
		return nil, err
	}
	if err := container.Provide(keypool.NewKeyValidator); err != nil {
		return nil, err
	}
	if err := container.Provide(keypool.NewCronChecker); err != nil {
		return nil, err
	}
	if err := container.Provide(keypool.NewPoolManager); err != nil {
		return nil, err
	}

	// Handlers
	if err := container.Provide(handlers.NewPoolHandler); err != nil {
		return nil, err
	}
	if err := container.Provide(handlers.NewRateLimitHandler); err != nil {
		return nil, err
	}
	if err := container.Provide(handler.NewServer); err != nil {
		return nil, err
	}
	if err := container.Provide(handler.NewCommonHandler); err != nil {
		return nil, err
	}

	// Proxy & Router
	if err := container.Provide(proxy.NewProxyServer); err != nil {
		return nil, err
	}
	if err := container.Provide(router.NewRouter); err != nil {
		return nil, err
	}

	// Application Layer
	if err := container.Provide(app.NewApp); err != nil {
		return nil, err
	}

	return container, nil
}
