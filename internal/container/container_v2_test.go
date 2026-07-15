package container

import (
	"os"
	"path/filepath"
	"testing"

	"gpt-load/internal/app"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/storage/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestBuildContainerResolvesM0DependencyGraph(t *testing.T) {
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
	) {
		resolved = true
		t.Cleanup(func() {
			_ = storageStore.Close()
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
		})
	})
	if err != nil {
		t.Fatalf("resolve M0 dependency graph: %v", err)
	}
	if !resolved {
		t.Fatal("M0 dependency graph was not invoked")
	}
	if _, err := os.Stat(filepath.Join(dataDir, encryption.KeyFileName)); err != nil {
		t.Fatalf("container did not initialize encryption keyfile: %v", err)
	}
}
