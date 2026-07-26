package requestlog

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

type staticRetentionPolicy struct{ days int }

func (policy staticRetentionPolicy) RequestLogRetentionDays() int {
	return policy.days
}

func newRequestLogTestService(db *gorm.DB) *Service {
	return NewService(
		db,
		redact.New(),
		staticRetentionPolicy{days: 7},
		newStaticPriceTableProvider(),
	)
}

type staticPriceTableProvider struct {
	table *pricing.Table
}

func (provider *staticPriceTableProvider) Load() *pricing.Table {
	if provider == nil {
		return nil
	}
	return provider.table
}

func newStaticPriceTableProvider() *staticPriceTableProvider {
	table, err := pricing.Compile(pricing.BuiltinRules())
	if err != nil {
		panic(err)
	}
	return &staticPriceTableProvider{table: table}
}

type publishingPriceTableProvider struct {
	table atomic.Pointer[pricing.Table]
	loads atomic.Uint64
}

func (provider *publishingPriceTableProvider) Load() *pricing.Table {
	provider.loads.Add(1)
	return provider.table.Load()
}

func (provider *publishingPriceTableProvider) Publish(table *pricing.Table) {
	provider.table.Store(table)
}

func compileRequestLogTestPriceTable(t *testing.T, model string, prices pricing.Prices) *pricing.Table {
	t.Helper()
	table, err := pricing.Compile([]pricing.Rule{{
		Pattern: model,
		Prices:  prices,
		Source:  pricing.SourceUser,
	}})
	if err != nil {
		t.Fatalf("pricing.Compile() error = %v", err)
	}
	return table
}

type batchWriterFunc func(context.Context, []models.RequestLog) error

func (fn batchWriterFunc) WriteBatch(ctx context.Context, rows []models.RequestLog) error {
	return fn(ctx, rows)
}

type manualTimer struct {
	ch   chan time.Time
	once sync.Once
}

func newManualTimer() *manualTimer {
	return &manualTimer{ch: make(chan time.Time, 1)}
}

func (timer *manualTimer) C() <-chan time.Time {
	return timer.ch
}

func (timer *manualTimer) Stop() bool {
	stopped := false
	timer.once.Do(func() {
		stopped = true
	})
	return stopped
}

func (timer *manualTimer) Fire() {
	timer.ch <- time.Unix(1, 0)
}

type manualTimerFactory struct {
	created chan *manualTimer
}

func newManualTimerFactory() *manualTimerFactory {
	return &manualTimerFactory{created: make(chan *manualTimer, 32)}
}

func (factory *manualTimerFactory) New(time.Duration) workerTimer {
	timer := newManualTimer()
	factory.created <- timer
	return timer
}

func receiveValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for test coordination")
		var zero T
		return zero
	}
}

func waitGroupDone(t *testing.T, group *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	receiveValue(t, done)
}

func openRequestLogFileDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "requestlog.db")
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
			t.Errorf("close request log database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("storage.AutoMigrate() error = %v", err)
	}
	return db, dsn
}

func holdRequestLogRollbackJournalReadLock(
	t *testing.T,
	appDB *gorm.DB,
	dsn string,
) func() {
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
	if err := readTx.Table("request_logs").Count(&count).Error; err != nil {
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

func aggregationRow(
	id string,
	completedAt time.Time,
	groupID uint,
	model string,
) models.RequestLog {
	return models.RequestLog{
		ID:            id,
		CreatedAt:     completedAt,
		AccessKeyID:   1,
		GroupID:       groupID,
		Protocol:      string(protocol.OpenAI),
		ClientModel:   "client-model",
		UpstreamModel: model,
		Status:        string(telemetry.RequestStatusSuccess),
		StatusCode:    200,
		DurationMs:    10,
		InputTokens:   1,
		OutputTokens:  2,
		Cost:          0.25,
		UsageState:    string(usage.StateComplete),
		CostState:     string(pricing.CostStatePriced),
		Attempts:      models.JSON(`[]`),
	}
}

func aggregationRequestID(index int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", 900000+index)
}

func testEvent(id string) telemetry.RequestEvent {
	return telemetry.RequestEvent{
		RequestID:     id,
		CompletedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		AccessKeyID:   42,
		ClientModel:   "client-model",
		UpstreamModel: "upstream-model",
		Status:        telemetry.RequestStatusSuccess,
		StatusCode:    200,
		DurationMs:    25,
		Attempts: []telemetry.Attempt{{
			Sequence:        1,
			GroupID:         7,
			GroupName:       "primary",
			KeyID:           8,
			KeyMask:         "sk-...mask",
			UpstreamModel:   "upstream-model",
			StatusCode:      200,
			DurationMs:      20,
			FailureCategory: telemetry.FailureCategoryOK,
			Action:          telemetry.ActionTerminate,
		}},
		Usage: telemetry.UsageObservation{
			GroupID: 7,
			Result: usage.Result{
				State: usage.StateNotApplicable,
			},
		},
	}
}
