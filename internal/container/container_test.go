package container

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/store"
)

func TestBuildContainerResolvesS2DependencyGraph(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("DATA_DIR", dataDir)
	t.Setenv("DATABASE_DSN", ":memory:")
	t.Setenv("ENCRYPTION_KEY", "")
	t.Setenv("REDIS_DSN", "")

	dependencyContainer, err := BuildContainer()
	if err != nil {
		t.Fatalf("BuildContainer() error = %v", err)
	}

	var resolved bool
	err = dependencyContainer.Invoke(func(
		_ *app.App,
		_ *config.Config,
		_ encryption.Service,
		db *gorm.DB,
		storageStore store.Store,
		_ *gin.Engine,
		manager *state.Manager,
		registry *state.KeyRegistry,
		runtimeState app.RuntimeStateLoader,
	) {
		t.Cleanup(func() {
			_ = storageStore.Close()
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
		if err := storage.AutoMigrate(db); err != nil {
			t.Fatalf("AutoMigrate() error = %v", err)
		}
		if err := runtimeState.Load(context.Background()); err != nil {
			t.Fatalf("runtimeState.Load() error = %v", err)
		}
		snapshot := manager.Current()
		if snapshot == nil || snapshot.Revision != 1 {
			t.Fatalf("current snapshot = %#v, want revision 1", snapshot)
		}
		if got := registry.CollectCandidates(nil, nil); len(got) != 0 {
			t.Fatalf("empty registry candidates = %#v", got)
		}
		resolved = true
	})
	if err != nil {
		t.Fatalf("resolve S2 dependency graph: %v", err)
	}
	if !resolved {
		t.Fatal("S2 dependency graph was not invoked")
	}
	if _, err := os.Stat(filepath.Join(dataDir, encryption.KeyFileName)); err != nil {
		t.Fatalf("container did not initialize encryption keyfile: %v", err)
	}
}
