package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httproute"
	"gpt-load/internal/platform/i18n"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
)

var (
	controlI18nOnce         sync.Once
	controlI18nErr          error
	testIdempotencySequence atomic.Uint64
)

type blockingDecryptService struct {
	encryption.Service
	started chan<- struct{}
	release <-chan struct{}
}

func (service blockingDecryptService) Decrypt(ciphertext string) (string, error) {
	close(service.started)
	<-service.release
	return service.Service.Decrypt(ciphertext)
}

func loadCreatedGroupModels(t *testing.T, fixture serviceFixture, groupID uint) []GroupModel {
	t.Helper()
	var group models.Group
	if err := fixture.db.First(&group, groupID).Error; err != nil {
		t.Fatalf("query group %d: %v", groupID, err)
	}
	var result []GroupModel
	if err := json.Unmarshal(group.Models, &result); err != nil {
		t.Fatalf("decode group %d models: %v", groupID, err)
	}
	if result == nil {
		result = make([]GroupModel, 0)
	}
	return result
}

func createGroupWithCredentials(t *testing.T, fixture serviceFixture, credentials string) uint {
	t.Helper()
	name := fmt.Sprintf("credential-group-%d", testIdempotencySequence.Add(1))
	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: &name, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Credentials: credentials,
	})
	if err != nil {
		t.Fatalf("CreateGroup(%q) error = %v", name, err)
	}
	return result.GroupID
}

// RegisterRoutes keeps package tests concise while production registration
// remains exclusively owned by the shared HTTP route registry.
func (s *Server) RegisterRoutes(engine *gin.Engine) {
	registry, err := httproute.NewRegistry(s.HTTPModule())
	if err != nil {
		panic(err)
	}
	if err := registry.Bind(engine); err != nil {
		panic(err)
	}
}

func registerGatewayRoutes(t *testing.T, engine *gin.Engine, handler *gateway.Handler) {
	t.Helper()
	registry, err := httproute.NewRegistry(handler.HTTPModule())
	if err != nil {
		t.Fatalf("NewRegistry(gateway) error = %v", err)
	}
	if err := registry.Bind(engine); err != nil {
		t.Fatalf("Bind(gateway) error = %v", err)
	}
}

func setRequiredTestIdempotencyHeader(request *http.Request) {
	sequence := testIdempotencySequence.Add(1)
	request.Header.Set(
		"Idempotency-Key",
		fmt.Sprintf("00000000-0000-4000-8000-%012x", sequence),
	)
}

type serviceFixture struct {
	db              *gorm.DB
	manager         *state.Manager
	registry        *state.CredentialRegistry
	channelRegistry *channel.Registry
	priceRuntime    *PriceRuntime
	catalogRuntime  *catalog.Runtime
	encryption      encryption.Service
	stats           *health.StatsStore
	mutations       *health.MutationCoordinator
	requestLogStats *staticRequestLogStatsReader
	service         *Service
}

type staticRequestLogStatsReader struct {
	value requestlog.Stats
	fn    func() requestlog.Stats
}

func mustBuildCompileInput(t *testing.T, db *gorm.DB) state.CompileInput {
	t.Helper()
	input, err := stateloader.BuildCompileInput(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func (reader *staticRequestLogStatsReader) Stats() requestlog.Stats {
	if reader.fn != nil {
		return reader.fn()
	}
	return reader.value
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

func mustEnsureInitialPrices(t *testing.T, fixture serviceFixture) {
	t.Helper()
	if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}
	if fixture.priceRuntime.Load() == nil {
		t.Fatal("EnsureInitialState() did not publish PriceTable")
	}
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
	registry := state.NewCredentialRegistry()
	channelRegistry := channel.NewRegistry()
	keyService, err := encryption.NewService("control-test-master-key-material-2026")
	if err != nil {
		t.Fatalf("encryption.NewService() error = %v", err)
	}
	if _, err := manager.Publish(state.CompileInput{}); err != nil {
		t.Fatalf("manager.Publish(empty) error = %v", err)
	}
	stats := health.NewStatsStore()
	mutations := health.NewMutationCoordinator()
	subscriptionCredentials := subscription.NewCodexCredentialManager(db, keyService, registry, mutations)
	requestLogStats := &staticRequestLogStatsReader{}
	priceRuntime := NewPriceRuntime()
	catalogRuntime := &catalog.Runtime{}
	return serviceFixture{
		db: db, manager: manager, registry: registry, channelRegistry: channelRegistry, encryption: keyService,
		priceRuntime: priceRuntime, catalogRuntime: catalogRuntime,
		stats: stats, mutations: mutations, requestLogStats: requestLogStats,
		service: NewService(
			db,
			manager,
			registry,
			priceRuntime,
			catalogRuntime,
			nil,
			keyService,
			controlHTTPExecutor{},
			subscriptionCredentials,
			nil,
			nil,
			nil,
			stats,
			mutations,
			requestLogStats,
			channelRegistry,
		),
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
