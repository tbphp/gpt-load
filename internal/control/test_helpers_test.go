package control

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

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
	return newServiceFixtureWithDSN(t, ":memory:")
}

func newFileServiceFixture(t *testing.T) (serviceFixture, string) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "control.db")
	return newServiceFixtureWithDSN(t, dsn), dsn
}

func newServiceFixtureWithDSN(t *testing.T, dsn string) serviceFixture {
	t.Helper()
	db := openControlTestDBWithDSN(t, dsn)
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
		service: NewService(db, manager, registry, keyService, dialect.NewSet(), nil),
	}
}

func openControlTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return openControlTestDBWithDSN(t, ":memory:")
}

func openControlTestDBWithDSN(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("storage.Open(%q) error = %v", dsn, err)
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

func holdRollbackJournalReadLock(t *testing.T, appDB *gorm.DB, dsn string) func() {
	t.Helper()
	if err := appDB.Exec("PRAGMA busy_timeout = 1").Error; err != nil {
		t.Fatalf("set app busy_timeout: %v", err)
	}
	var mode string
	if err := appDB.Raw("PRAGMA journal_mode = DELETE").Scan(&mode).Error; err != nil {
		t.Fatalf("set rollback journal: %v", err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("journal_mode = %q, want delete", mode)
	}

	blocker, err := gorm.Open(
		gormsqlite.Open(dsn+"?_pragma=busy_timeout(1)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	blockerSQL, err := blocker.DB()
	if err != nil {
		t.Fatalf("blocker DB(): %v", err)
	}
	readTx := blocker.Begin()
	if readTx.Error != nil {
		t.Fatal(readTx.Error)
	}
	var count int64
	if err := readTx.Table("groups").Count(&count).Error; err != nil {
		t.Fatal(err)
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			if err := readTx.Rollback().Error; err != nil {
				t.Errorf("release read lock: %v", err)
			}
			if err := blockerSQL.Close(); err != nil {
				t.Errorf("close blocker: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
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
