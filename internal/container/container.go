// Package container assembles the 2.0 dependency graph with dig.
package container

import (
	"go.uber.org/dig"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"
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
		func(cfg *config.Config) (store.Store, error) {
			return store.NewStore(cfg.RedisDSN)
		},
		app.NewEngine,
		state.NewManager,
		state.NewKeyRegistry,
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
	return dependencyContainer, nil
}
