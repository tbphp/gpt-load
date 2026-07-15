// Package container assembles the M0 dependency graph with dig.
package container

import (
	"gpt-load/internal/app"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"

	"go.uber.org/dig"
	"gorm.io/gorm"
)

// BuildContainer creates the 2.0 M0 dependency graph.
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
		func(cfg *config.Config) (store.Store, error) {
			return store.NewStore(cfg.RedisDSN)
		},
		app.NewEngine,
		app.NewApp,
	}

	for _, provider := range providers {
		if err := dependencyContainer.Provide(provider); err != nil {
			return nil, err
		}
	}
	return dependencyContainer, nil
}
