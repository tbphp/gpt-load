package control

import (
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/state"
	"gpt-load/internal/storage"
)

var (
	controlI18nOnce sync.Once
	controlI18nErr  error
)

type serviceFixture struct {
	db         *gorm.DB
	manager    *state.Manager
	registry   *state.KeyRegistry
	encryption encryption.Service
	service    *Service
}

func initControlI18n(t *testing.T) {
	t.Helper()
	controlI18nOnce.Do(func() {
		gin.SetMode(gin.ReleaseMode)
		controlI18nErr = i18n.Init()
	})
	if controlI18nErr != nil {
		t.Fatalf("i18n.Init() error = %v", controlI18nErr)
	}
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	db := openControlTestDB(t)
	manager := state.NewManager()
	registry := state.NewKeyRegistry()
	keyService, err := encryption.NewService("control-test-master-key-material-2026")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	if _, err := manager.Publish(state.CompileInput{}); err != nil {
		t.Fatalf("manager.Publish(empty) error = %v", err)
	}
	return serviceFixture{
		db: db, manager: manager, registry: registry, encryption: keyService,
		service: NewService(db, manager, registry, keyService, dialect.NewSet()),
	}
}

func openControlTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close control test database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("storage.AutoMigrate() error = %v", err)
	}
	return db
}

func assertGroupCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	var got int64
	if err := db.Table("groups").Count(&got).Error; err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if got != want {
		t.Fatalf("group count = %d, want %d", got, want)
	}
}
