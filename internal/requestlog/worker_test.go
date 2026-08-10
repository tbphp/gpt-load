package requestlog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestUsageStatUpsertUsesDialectSpecificGORMConflictSQL(t *testing.T) {
	stat := models.UsageStat{
		BucketStartMS: 1_784_894_400_000,
		AccessKeyID:   1,
		GroupID:       2,
		Model:         "model",
	}
	tests := []struct {
		name           string
		dialector      gorm.Dialector
		mustContain    string
		mustNotContain string
	}{
		{
			name:           "sqlite",
			dialector:      gormsqlite.Open(":memory:"),
			mustContain:    "ON CONFLICT",
			mustNotContain: "ON DUPLICATE KEY UPDATE",
		},
		{
			name: "mysql",
			dialector: gormmysql.New(gormmysql.Config{
				DSN:                       "user:password@tcp(127.0.0.1:3306)/gpt_load",
				SkipInitializeWithVersion: true,
			}),
			mustContain:    "ON DUPLICATE KEY UPDATE",
			mustNotContain: "ON CONFLICT",
		},
		{
			name: "postgres",
			dialector: gormpostgres.New(gormpostgres.Config{
				DSN:                  "host=127.0.0.1 user=user password=password dbname=gpt_load sslmode=disable",
				PreferSimpleProtocol: true,
			}),
			mustContain:    "ON CONFLICT",
			mustNotContain: "ON DUPLICATE KEY UPDATE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(test.dialector, &gorm.Config{
				DryRun:                 true,
				DisableAutomaticPing:   true,
				SkipDefaultTransaction: true,
				Logger:                 logger.Default.LogMode(logger.Silent),
			})
			if err != nil {
				t.Fatalf("gorm.Open() error = %v", err)
			}
			result := db.Clauses(usageStatUpsertClause()).Create(&stat)
			if result.Error != nil {
				t.Fatalf("Create() error = %v", result.Error)
			}
			sql := result.Statement.SQL.String()
			if !strings.Contains(sql, test.mustContain) {
				t.Fatalf("generated SQL = %q, want %q", sql, test.mustContain)
			}
			if strings.Contains(sql, test.mustNotContain) {
				t.Fatalf("generated SQL = %q, must not contain %q", sql, test.mustNotContain)
			}
		})
	}
}

func TestServiceFlushesAtBatchSizeAndDelayInFIFOOrder(t *testing.T) {
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 2)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("batch-%03d", index)))
	}
	firstTimer := receiveValue(t, timers.created)
	firstRows := receiveValue(t, writes)
	if len(firstRows) != batchSize {
		t.Fatalf("batch-size flush rows = %d, want %d", len(firstRows), batchSize)
	}
	for index, row := range firstRows {
		wantID := fmt.Sprintf("batch-%03d", index)
		if row.ID != wantID {
			t.Fatalf("batch row %d ID = %q, want %q", index, row.ID, wantID)
		}
	}
	if firstTimer.Stop() {
		t.Fatal("batch-size flush did not stop its one-shot timer")
	}

	service.Emit(testEvent("delay-1"))
	service.Emit(testEvent("delay-2"))
	secondTimer := receiveValue(t, timers.created)
	secondTimer.Fire()
	secondRows := receiveValue(t, writes)
	if len(secondRows) != 2 || secondRows[0].ID != "delay-1" || secondRows[1].ID != "delay-2" {
		t.Fatalf("delay flush rows = %+v, want FIFO delay-1/delay-2", secondRows)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceDropsFailedBatchAndContinues(t *testing.T) {
	writeFailure := errors.New("write failed")
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 2)
	var callMu sync.Mutex
	call := 0
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			copied := append([]models.RequestLog(nil), rows...)
			writes <- copied
			callMu.Lock()
			defer callMu.Unlock()
			call++
			if call == 1 {
				return writeFailure
			}
			return nil
		}),
		redact.New(),
		timers.New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	service.Emit(testEvent("failed"))
	receiveValue(t, timers.created).Fire()
	receiveValue(t, writes)
	service.Emit(testEvent("continued"))
	receiveValue(t, timers.created).Fire()
	secondRows := receiveValue(t, writes)
	if len(secondRows) != 1 || secondRows[0].ID != "continued" {
		t.Fatalf("second write rows = %+v", secondRows)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 1 || stats.DroppedPersistFailedTotal != 1 ||
		stats.PersistedTotal != 1 || stats.DroppedTotal != 1 ||
		stats.LastWriteFailureAt.IsZero() {
		t.Fatalf("stats after failed batch and continuation = %+v", stats)
	}
}

func TestServiceDropsInvalidProjectionWithoutDiscardingValidRows(t *testing.T) {
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 1)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	invalid := testEvent("invalid-completion")
	invalid.CompletedAt = time.Unix(-1, 0)
	service.Emit(invalid)
	service.Emit(testEvent("valid-completion"))
	receiveValue(t, timers.created).Fire()

	rows := receiveValue(t, writes)
	if len(rows) != 1 || rows[0].ID != "valid-completion" {
		t.Fatalf("persisted rows = %+v, want only valid-completion", rows)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 1 ||
		stats.DroppedPersistFailedTotal != 1 ||
		stats.PersistedTotal != 1 ||
		stats.DroppedTotal != 1 ||
		stats.LastWriteFailureAt.IsZero() {
		t.Fatalf("stats after projection failure = %+v", stats)
	}
}

func TestServiceStopDrainsAndIsIdempotent(t *testing.T) {
	writes := make(chan []models.RequestLog, 2)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for _, id := range []string{"drain-1", "drain-2", "drain-3"} {
		service.Emit(testEvent(id))
	}

	startStops := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-startStops
			results <- service.Stop(context.Background())
		}()
	}
	close(startStops)
	waitGroupDone(t, &group)
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Stop() error = %v", err)
		}
	}

	rows := receiveValue(t, writes)
	if len(rows) != 3 {
		t.Fatalf("drained rows = %d, want 3", len(rows))
	}
	select {
	case duplicate := <-writes:
		t.Fatalf("Stop flushed duplicate batch: %+v", duplicate)
	default:
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != 3 || stats.QueueDepth != 0 {
		t.Fatalf("stats after drain = %+v", stats)
	}
}

func TestServiceStopDeadlineSeparatesPersistAndShutdownDrops(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	service := newService(
		batchWriterFunc(func(ctx context.Context, _ []models.RequestLog) error {
			writeStarted <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("attempted-%03d", index)))
	}
	receiveValue(t, writeStarted)
	for index := 0; index < 3; index++ {
		service.Emit(testEvent(fmt.Sprintf("shutdown-drop-%d", index)))
	}

	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	err := service.Stop(stopContext)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want wrapped context.Canceled", err)
	}
	if err := service.Stop(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() after worker cancellation error = %v, want stable context.Canceled", err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 1 || stats.DroppedPersistFailedTotal != batchSize ||
		stats.DroppedShutdownTotal != 3 || stats.DroppedTotal != batchSize+3 ||
		stats.PersistedTotal != 0 || stats.QueueDepth != 0 {
		t.Fatalf("deadline stats = %+v", stats)
	}
}

func TestServiceStopDeadlineIsHardBoundaryWhenWriterIgnoresCancellation(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			writeStarted <- struct{}{}
			<-releaseWrite
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("hard-deadline-%03d", index)))
	}
	receiveValue(t, writeStarted)

	stopContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(stopContext)
	}()

	var stopErr error
	select {
	case stopErr = <-stopResult:
	case <-time.After(250 * time.Millisecond):
		close(releaseWrite)
		receiveValue(t, stopResult)
		t.Fatal("Stop() waited for a writer that ignored cancellation")
	}
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		close(releaseWrite)
		t.Fatalf("Stop() error = %v, want wrapped context.DeadlineExceeded", stopErr)
	}

	close(releaseWrite)
	if err := service.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() after worker completion error = %v, want stable deadline error", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != batchSize ||
		stats.DroppedPersistFailedTotal != 0 || stats.DroppedShutdownTotal != 0 {
		t.Fatalf("stats after delayed successful write = %+v", stats)
	}
}

func TestServiceConcurrentStopHonorsOwnDeadlineWithoutDuplicateDrain(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	var writeCalls atomic.Uint64
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			writeCalls.Add(1)
			writeStarted <- struct{}{}
			<-releaseWrite
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	service.Emit(testEvent("single-drain"))

	backgroundStop := make(chan error, 1)
	go func() {
		backgroundStop <- service.Stop(context.Background())
	}()
	receiveValue(t, writeStarted)

	shortContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	shortStop := make(chan error, 1)
	go func() {
		shortStop <- service.Stop(shortContext)
	}()

	var shortErr error
	select {
	case shortErr = <-shortStop:
	case <-time.After(250 * time.Millisecond):
		close(releaseWrite)
		receiveValue(t, shortStop)
		receiveValue(t, backgroundStop)
		t.Fatal("second Stop() ignored its own deadline while a shared drain was active")
	}
	if !errors.Is(shortErr, context.DeadlineExceeded) {
		close(releaseWrite)
		t.Fatalf("second Stop() error = %v, want wrapped context.DeadlineExceeded", shortErr)
	}

	close(releaseWrite)
	if err := receiveValue(t, backgroundStop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("background Stop() error = %v, want shared deadline error", err)
	}
	if got := writeCalls.Load(); got != 1 {
		t.Fatalf("WriteBatch calls = %d, want one shared drain/flush", got)
	}
	if err := service.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("completed Stop() error = %v, want stable deadline error", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != 1 || stats.DroppedTotal != 0 {
		t.Fatalf("stats after shared drain = %+v", stats)
	}
}

func TestWriteBatchInsertsOnlyNewRequestLogsAndAggregatesUsage(t *testing.T) {
	db := openRequestLogQueryDB(t)
	hour := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	existing := aggregationRow(aggregationRequestID(1), hour, 7, "aggregate-model")
	existing.UncachedInputTokens = 99
	if err := db.Create(&existing).Error; err != nil {
		t.Fatalf("create existing RequestLog: %v", err)
	}
	baseline := models.UsageStat{
		BucketStartMS:        1_784_894_400_000,
		AccessKeyID:          1,
		GroupID:              7,
		Model:                "aggregate-model",
		RequestCount:         10,
		SuccessCount:         10,
		UncachedInputTokens:  100,
		OutputTokens:         200,
		EstimatedCostNanoUSD: 1_000_000_000,
	}
	if err := db.Create(&baseline).Error; err != nil {
		t.Fatalf("create baseline UsageStat: %v", err)
	}

	first := aggregationRow(aggregationRequestID(2), hour.Add(time.Minute), 7, "aggregate-model")
	first.UncachedInputTokens = 3
	first.OutputTokens = 5
	first.EstimatedCostNanoUSD = 500_000_000
	duplicate := first
	duplicate.GroupID = 999
	duplicate.UpstreamModel = "duplicate-must-lose"
	duplicate.UncachedInputTokens = 1_000
	replay := existing
	replay.GroupID = 999
	replay.UpstreamModel = "existing-must-win"
	second := aggregationRow(aggregationRequestID(3), hour.Add(2*time.Minute), 7, "aggregate-model")
	second.UncachedInputTokens = 7
	second.OutputTokens = 11
	second.EstimatedCostNanoUSD = 750_000_000
	rows := []models.RequestLog{first, duplicate, replay, second}

	var requestLogQueries, usageStatQueries int
	countQueries := true
	const callbackName = "test:batch_writer_query_count"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if !countQueries {
			return
		}
		switch tx.Statement.Table {
		case "request_logs":
			requestLogQueries++
		case "usage_stats":
			usageStatQueries++
		}
	}); err != nil {
		t.Fatalf("register query counter: %v", err)
	}
	if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if requestLogQueries != 1 || usageStatQueries != 1 {
		t.Fatalf("existing queries = request_logs:%d usage_stats:%d, want one each", requestLogQueries, usageStatQueries)
	}
	countQueries = false

	var persisted []models.RequestLog
	if err := db.Order("id ASC").Find(&persisted).Error; err != nil {
		t.Fatalf("query RequestLogs: %v", err)
	}
	if len(persisted) != 3 {
		t.Fatalf("RequestLog count = %d, want existing plus two new", len(persisted))
	}
	var persistedFirst models.RequestLog
	if err := db.First(&persistedFirst, "id = ?", first.ID).Error; err != nil {
		t.Fatalf("query first RequestLog: %v", err)
	}
	if persistedFirst.GroupID != 7 || persistedFirst.UpstreamModel != "aggregate-model" ||
		persistedFirst.UncachedInputTokens != 3 {
		t.Fatalf("first-write-wins row = %+v", persistedFirst)
	}
	var persistedExisting models.RequestLog
	if err := db.First(&persistedExisting, "id = ?", existing.ID).Error; err != nil {
		t.Fatalf("query existing RequestLog: %v", err)
	}
	if persistedExisting.GroupID != 7 || persistedExisting.UpstreamModel != "aggregate-model" ||
		persistedExisting.UncachedInputTokens != 99 {
		t.Fatalf("existing row was updated: %+v", persistedExisting)
	}

	var stat models.UsageStat
	if err := db.Where(
		"bucket_start_ms = ? AND access_key_id = ? AND group_id = ? AND model = ?",
		int64(1_784_894_400_000),
		1,
		7,
		"aggregate-model",
	).
		Take(&stat).Error; err != nil {
		t.Fatalf("query UsageStat: %v", err)
	}
	if stat.RequestCount != 12 || stat.SuccessCount != 12 || stat.FailureCount != 0 ||
		stat.UncachedInputTokens != 110 || stat.OutputTokens != 216 ||
		stat.EstimatedCostNanoUSD != 2_250_000_000 {
		t.Fatalf("UsageStat = %+v, want baseline plus only two new rows", stat)
	}

	if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err != nil {
		t.Fatalf("replay WriteBatch() error = %v", err)
	}
	var replayedStat models.UsageStat
	if err := db.First(&replayedStat, stat.ID).Error; err != nil {
		t.Fatalf("query replayed UsageStat: %v", err)
	}
	if !reflect.DeepEqual(replayedStat, stat) {
		t.Fatalf("replay changed UsageStat: got %+v want %+v", replayedStat, stat)
	}
}

func TestWriteBatchAggregatesStatusQualityAndCompletePricedTotals(t *testing.T) {
	db := openRequestLogQueryDB(t)
	hour := time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC)

	complete := aggregationRow(aggregationRequestID(10), hour, 8, "quality-model")
	complete.UncachedInputTokens = 1
	complete.OutputTokens = 2
	complete.CacheReadTokens = 3
	complete.CacheWrite5MTokens = 4
	complete.CacheWrite1HTokens = 5
	complete.EstimatedCostNanoUSD = 1_250_000_000

	completeUnpriced := aggregationRow(aggregationRequestID(15), hour, 8, "quality-model")
	completeUnpriced.CostState = string(pricing.CostStateUnpriced)
	completeUnpriced.PricingCompleteness = string(pricing.CompletenessUnavailable)
	completeUnpriced.UncachedInputTokens = 400
	completeUnpriced.OutputTokens = 500
	completeUnpriced.EstimatedCostNanoUSD = 0

	missing := aggregationRow(aggregationRequestID(11), hour, 8, "quality-model")
	missing.Status = string(telemetry.RequestStatusError)
	missing.UsageState = string(usage.StateMissing)
	missing.CostState = string(pricing.CostStateUnpriced)
	missing.PricingCompleteness = string(pricing.CompletenessUnavailable)
	missing.UncachedInputTokens = 100
	missing.EstimatedCostNanoUSD = 0

	partialPriced := aggregationRow(aggregationRequestID(12), hour, 8, "quality-model")
	partialPriced.Status = string(telemetry.RequestStatusIncomplete)
	partialPriced.UsageState = string(usage.StatePartial)
	partialPriced.PricingCompleteness = string(pricing.CompletenessPartial)
	partialPriced.UncachedInputTokens = 200
	partialPriced.CacheWriteUnknownTokens = 7
	partialPriced.OutputTokens = 300
	partialPriced.EstimatedCostNanoUSD = 2_500_000_000

	partialUnpriced := aggregationRow(aggregationRequestID(13), hour, 8, "quality-model")
	partialUnpriced.Status = string(telemetry.RequestStatusCanceled)
	partialUnpriced.UsageState = string(usage.StatePartial)
	partialUnpriced.CostState = string(pricing.CostStateUnpriced)
	partialUnpriced.PricingCompleteness = string(pricing.CompletenessUnavailable)
	partialUnpriced.EstimatedCostNanoUSD = 0

	notApplicable := aggregationRow(aggregationRequestID(14), hour, 8, "quality-model")
	notApplicable.Status = string(telemetry.RequestStatusError)
	notApplicable.UsageState = string(usage.StateNotApplicable)
	notApplicable.CostState = string(pricing.CostStateNotApplicable)
	notApplicable.PricingCompleteness = string(pricing.CompletenessNotApplicable)
	notApplicable.UncachedInputTokens = 700
	notApplicable.OutputTokens = 800
	notApplicable.EstimatedCostNanoUSD = 0

	if err := (&gormBatchWriter{db: db}).WriteBatch(
		context.Background(),
		[]models.RequestLog{
			complete,
			completeUnpriced,
			missing,
			partialPriced,
			partialUnpriced,
			notApplicable,
		},
	); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	var stat models.UsageStat
	if err := db.Take(&stat).Error; err != nil {
		t.Fatalf("query UsageStat: %v", err)
	}
	if stat.RequestCount != 6 || stat.SuccessCount != 2 || stat.FailureCount != 4 ||
		stat.UsageMissingCount != 1 || stat.PartialCount != 2 ||
		stat.UnpricedRequestCount != 2 || stat.PricingPartialCount != 1 {
		t.Fatalf("status/quality counts = %+v", stat)
	}
	if stat.UncachedInputTokens != 602 || stat.OutputTokens != 804 || stat.CacheReadTokens != 3 ||
		stat.CacheWrite5MTokens != 4 || stat.CacheWrite1HTokens != 5 ||
		stat.CacheWriteUnknownTokens != 7 ||
		stat.EstimatedCostNanoUSD != 3_750_000_000 {
		t.Fatalf("complete/partial totals = %+v", stat)
	}
	var persistedPartial models.RequestLog
	if err := db.First(&persistedPartial, "id = ?", partialPriced.ID).Error; err != nil {
		t.Fatalf("query partial RequestLog: %v", err)
	}
	if persistedPartial.UncachedInputTokens != 200 || persistedPartial.OutputTokens != 300 ||
		persistedPartial.EstimatedCostNanoUSD != 2_500_000_000 {
		t.Fatalf("partial+priced RequestLog lost usage/cost: %+v", persistedPartial)
	}
	var persistedUnpriced models.RequestLog
	if err := db.First(&persistedUnpriced, "id = ?", completeUnpriced.ID).Error; err != nil {
		t.Fatalf("query complete+unpriced RequestLog: %v", err)
	}
	if persistedUnpriced.UncachedInputTokens != 400 || persistedUnpriced.OutputTokens != 500 ||
		persistedUnpriced.EstimatedCostNanoUSD != 0 {
		t.Fatalf("complete+unpriced RequestLog lost usage or gained cost: %+v", persistedUnpriced)
	}
}

func TestWriteBatchSeparatesHourAccessGroupAndUpstreamModelWithoutSkippingZeroDimensions(t *testing.T) {
	db := openRequestLogQueryDB(t)
	location := time.FixedZone("utc-plus-eight", 8*60*60)
	localHour := time.Date(2026, time.July, 24, 20, 0, 0, 0, location)
	rows := []models.RequestLog{
		aggregationRow(aggregationRequestID(20), localHour.Add(time.Minute), 9, "model-a"),
		aggregationRow(aggregationRequestID(21), localHour.Add(59*time.Minute), 9, "model-a"),
		aggregationRow(aggregationRequestID(22), localHour.Add(time.Hour), 9, "model-a"),
		aggregationRow(aggregationRequestID(23), localHour.Add(2*time.Minute), 10, "model-a"),
		aggregationRow(aggregationRequestID(24), localHour.Add(3*time.Minute), 9, "model-b"),
		aggregationRow(aggregationRequestID(25), localHour.Add(4*time.Minute), 0, "model-a"),
		aggregationRow(aggregationRequestID(26), localHour.Add(5*time.Minute), 9, ""),
	}
	differentAccess := aggregationRow(
		aggregationRequestID(27),
		localHour.Add(6*time.Minute),
		9,
		"model-a",
	)
	differentAccess.AccessKeyID = 2
	rows = append(rows, differentAccess)
	for index := range rows {
		rows[index].ClientModel = "shared-client-alias"
	}
	if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	var stats []models.UsageStat
	if err := db.Order("bucket_start_ms ASC").
		Order("access_key_id ASC").
		Order("group_id ASC").
		Order("model ASC").
		Find(&stats).Error; err != nil {
		t.Fatalf("query UsageStats: %v", err)
	}
	if len(stats) != 7 {
		t.Fatalf("UsageStat rows = %d, want seven isolated buckets: %+v", len(stats), stats)
	}
	type bucket struct {
		bucketMS int64
		access   uint
		group    uint
		model    string
		count    int64
	}
	got := make([]bucket, 0, len(stats))
	for _, stat := range stats {
		got = append(got, bucket{
			stat.BucketStartMS,
			stat.AccessKeyID,
			stat.GroupID,
			stat.Model,
			stat.RequestCount,
		})
	}
	want := []bucket{
		{1_784_894_400_000, 1, 0, "model-a", 1},
		{1_784_894_400_000, 1, 9, "", 1},
		{1_784_894_400_000, 1, 9, "model-a", 2},
		{1_784_894_400_000, 1, 9, "model-b", 1},
		{1_784_894_400_000, 1, 10, "model-a", 1},
		{1_784_894_400_000, 2, 9, "model-a", 1},
		{1_784_898_000_000, 1, 9, "model-a", 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buckets = %#v, want %#v", got, want)
	}
	var requestCount int64
	if err := db.Model(&models.RequestLog{}).Count(&requestCount).Error; err != nil {
		t.Fatalf("count RequestLogs: %v", err)
	}
	if requestCount != int64(len(rows)) {
		t.Fatalf("RequestLog count = %d, want %d including unattributable rows", requestCount, len(rows))
	}
}

func TestWriteBatchPersistsZeroAttemptRequestWithoutUsageAggregation(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completedAt := time.Date(2026, time.August, 8, 14, 1, 37, 0, time.Local)
	zeroAttempt := models.RequestLog{
		ID:                  aggregationRequestID(28),
		CompletedAtMS:       completedAt.UTC().UnixMilli(),
		AccessKeyID:         1,
		Protocol:            "openai-responses",
		ClientModel:         "luna",
		ModelConsistency:    string(telemetry.ModelConsistencyNotApplicable),
		Status:              string(telemetry.RequestStatusError),
		StatusCode:          503,
		ErrorCode:           "no_available_candidate",
		ErrorSummary:        "No available upstream candidate.",
		UsageState:          string(usage.StateNotApplicable),
		CostState:           string(pricing.CostStateNotApplicable),
		PricingCompleteness: string(pricing.CompletenessNotApplicable),
	}
	attempted := aggregationRow(
		aggregationRequestID(29),
		completedAt.Add(time.Second),
		9,
		"model-a",
	)

	if err := (&gormBatchWriter{db: db}).WriteBatch(
		context.Background(),
		[]models.RequestLog{zeroAttempt, attempted},
	); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	assertRequestLogAndUsageStatCounts(t, db, 2, 1)
	assertUsageJournalCount(t, db, 1)
	var persisted models.RequestLog
	if err := db.First(&persisted, "id = ?", zeroAttempt.ID).Error; err != nil {
		t.Fatalf("query zero-attempt RequestLog: %v", err)
	}
	if persisted.AttemptCount != 0 || persisted.ErrorCode != zeroAttempt.ErrorCode {
		t.Fatalf("zero-attempt RequestLog = %+v", persisted)
	}
	var stat models.UsageStat
	if err := db.First(&stat).Error; err != nil {
		t.Fatalf("query attempted UsageStat: %v", err)
	}
	if stat.GroupID != attempted.GroupID || stat.Model != attempted.UpstreamModel ||
		stat.RequestCount != 1 {
		t.Fatalf("attempted UsageStat = %+v", stat)
	}
}

func TestWriteBatchRollsBackRequestLogsAndStatsOnFailure(t *testing.T) {
	newRow := func() models.RequestLog {
		return aggregationRow(
			aggregationRequestID(30),
			time.Date(2026, time.July, 24, 14, 0, 0, 0, time.UTC),
			11,
			"rollback-model",
		)
	}

	t.Run("existing ID query", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		const callbackName = "test:reject_existing_id_query"
		if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "request_logs" {
				tx.AddError(errors.New("forced existing ID query failure"))
			}
		}); err != nil {
			t.Fatalf("register callback: %v", err)
		}
		if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), []models.RequestLog{newRow()}); err == nil {
			t.Fatal("WriteBatch() error = nil, want existing ID query failure")
		}
		assertRequestLogAndUsageStatCounts(t, db, 0, 0)
		assertUsageJournalCount(t, db, 0)
	})

	t.Run("RequestLog insert", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		if err := db.Exec(`
			CREATE TRIGGER reject_request_log
			BEFORE INSERT ON request_logs
			BEGIN
			  SELECT RAISE(ABORT, 'request log rejected');
			END
		`).Error; err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), []models.RequestLog{newRow()}); err == nil {
			t.Fatal("WriteBatch() error = nil, want RequestLog insert failure")
		}
		assertRequestLogAndUsageStatCounts(t, db, 0, 0)
		assertUsageJournalCount(t, db, 0)
	})

	t.Run("existing UsageStat query", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		const callbackName = "test:reject_existing_usage_stat_query"
		if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "usage_stats" {
				tx.AddError(errors.New("forced existing UsageStat query failure"))
			}
		}); err != nil {
			t.Fatalf("register callback: %v", err)
		}
		if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), []models.RequestLog{newRow()}); err == nil {
			t.Fatal("WriteBatch() error = nil, want UsageStat query failure")
		}
		assertRequestLogAndUsageStatCounts(t, db, 0, 0)
		assertUsageJournalCount(t, db, 0)
	})

	t.Run("existing UsageStat scan conversion", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		row := newRow()
		hourMS := row.CompletedAtMS - row.CompletedAtMS%3_600_000
		const invalidRequestCount = "not-an-integer"
		if err := db.Exec(`PRAGMA ignore_check_constraints = ON`).Error; err != nil {
			t.Fatalf("disable SQLite CHECK constraints: %v", err)
		}
		if err := db.Exec(`
			INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, estimated_cost_nano_usd
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`,
			hourMS,
			row.AccessKeyID,
			row.GroupID,
			row.ClientModel,
			invalidRequestCount,
			7,
			3_500_000_000,
		).Error; err != nil {
			t.Fatalf("insert incompatible UsageStat: %v", err)
		}
		if err := db.Exec(`PRAGMA ignore_check_constraints = OFF`).Error; err != nil {
			t.Fatalf("restore SQLite CHECK constraints: %v", err)
		}

		err := (&gormBatchWriter{db: db}).WriteBatch(
			context.Background(),
			[]models.RequestLog{row},
		)
		if err == nil || !strings.Contains(err.Error(), "query existing usage stats") {
			t.Fatalf("WriteBatch() error = %v, want UsageStat scan failure", err)
		}
		assertRequestLogAndUsageStatCounts(t, db, 0, 1)
		assertUsageJournalCount(t, db, 0)

		var persisted struct {
			RequestCount         string `gorm:"column:request_count"`
			SuccessCount         int64  `gorm:"column:success_count"`
			EstimatedCostNanoUSD int64  `gorm:"column:estimated_cost_nano_usd"`
		}
		if err := db.Raw(`
			SELECT
				CAST(request_count AS TEXT) AS request_count,
				success_count,
				estimated_cost_nano_usd
			FROM usage_stats
			WHERE bucket_start_ms = ? AND access_key_id = ?
				AND group_id = ? AND model = ?
		`, hourMS, row.AccessKeyID, row.GroupID, row.ClientModel).
			Scan(&persisted).Error; err != nil {
			t.Fatalf("read incompatible UsageStat after rollback: %v", err)
		}
		if persisted.RequestCount != invalidRequestCount ||
			persisted.SuccessCount != 7 ||
			persisted.EstimatedCostNanoUSD != 3_500_000_000 {
			t.Fatalf("existing UsageStat changed after scan failure: %+v", persisted)
		}
	})

	t.Run("UsageStat UPSERT", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		if err := db.Exec(`
			CREATE TRIGGER reject_usage_stat
			BEFORE INSERT ON usage_stats
			BEGIN
			  SELECT RAISE(ABORT, 'usage stat rejected');
			END
		`).Error; err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), []models.RequestLog{newRow()}); err == nil {
			t.Fatal("WriteBatch() error = nil, want UsageStat UPSERT failure")
		}
		assertRequestLogAndUsageStatCounts(t, db, 0, 0)
		assertUsageJournalCount(t, db, 0)
	})

	t.Run("commit", func(t *testing.T) {
		db, dsn := openRequestLogFileDB(t)
		release := holdRequestLogRollbackJournalReadLock(t, db, dsn)
		err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), []models.RequestLog{newRow()})
		if err == nil {
			t.Fatal("WriteBatch() error = nil, want COMMIT failure")
		}
		release()
		assertRequestLogAndUsageStatCounts(t, db, 0, 0)
		assertUsageJournalCount(t, db, 0)
	})
}

func TestWriteBatchRejectsIntegerAndCostOverflow(t *testing.T) {
	hour := time.Date(2026, time.July, 24, 15, 0, 0, 0, time.UTC)

	t.Run("batch token delta overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		first := aggregationRow(aggregationRequestID(40), hour, 12, "overflow-model")
		first.UncachedInputTokens = math.MaxInt64
		second := aggregationRow(aggregationRequestID(41), hour, 12, "overflow-model")
		second.UncachedInputTokens = 1
		assertBatchWriterRejectsRowsWithoutChanges(t, db, nil, []models.RequestLog{first, second})
	})

	t.Run("batch cost overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		first := aggregationRow(aggregationRequestID(42), hour, 12, "overflow-model")
		first.EstimatedCostNanoUSD = math.MaxInt64
		second := aggregationRow(aggregationRequestID(43), hour, 12, "overflow-model")
		assertBatchWriterRejectsRowsWithoutChanges(t, db, nil, []models.RequestLog{first, second})
	})

	t.Run("batch unknown cache-write token overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		first := aggregationRow(aggregationRequestID(49), hour, 12, "overflow-model")
		first.CacheWriteUnknownTokens = math.MaxInt64
		second := aggregationRow(aggregationRequestID(50), hour, 12, "overflow-model")
		second.CacheWriteUnknownTokens = 1
		assertBatchWriterRejectsRowsWithoutChanges(t, db, nil, []models.RequestLog{first, second})
	})

	t.Run("existing count overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS: 1_784_905_200_000,
			AccessKeyID:   1,
			GroupID:       12,
			Model:         "overflow-model",
			RequestCount:  math.MaxInt64,
		}
		row := aggregationRow(aggregationRequestID(44), hour, 12, "overflow-model")
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("existing token overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS:       1_784_905_200_000,
			AccessKeyID:         1,
			GroupID:             12,
			Model:               "overflow-model",
			UncachedInputTokens: math.MaxInt64,
		}
		row := aggregationRow(aggregationRequestID(45), hour, 12, "overflow-model")
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("existing cost overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS:        1_784_905_200_000,
			AccessKeyID:          1,
			GroupID:              12,
			Model:                "overflow-model",
			EstimatedCostNanoUSD: math.MaxInt64,
		}
		row := aggregationRow(aggregationRequestID(46), hour, 12, "overflow-model")
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("existing unknown cache-write token overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS:           1_784_905_200_000,
			AccessKeyID:             1,
			GroupID:                 12,
			Model:                   "overflow-model",
			CacheWriteUnknownTokens: math.MaxInt64,
		}
		row := aggregationRow(aggregationRequestID(51), hour, 12, "overflow-model")
		row.CacheWriteUnknownTokens = 1
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("existing pricing partial count overflow", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS:       1_784_905_200_000,
			AccessKeyID:         1,
			GroupID:             12,
			Model:               "overflow-model",
			PricingPartialCount: math.MaxInt64,
		}
		row := aggregationRow(aggregationRequestID(52), hour, 12, "overflow-model")
		row.PricingCompleteness = string(pricing.CompletenessPartial)
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("negative existing integer", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		existing := models.UsageStat{
			BucketStartMS: 1_784_905_200_000,
			AccessKeyID:   1,
			GroupID:       12,
			Model:         "overflow-model",
			FailureCount:  -1,
		}
		row := aggregationRow(aggregationRequestID(47), hour, 12, "overflow-model")
		assertBatchWriterRejectsRowsWithoutChanges(t, db, &existing, []models.RequestLog{row})
	})

	t.Run("negative row cost", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		row := aggregationRow(aggregationRequestID(48), hour, 12, "overflow-model")
		row.EstimatedCostNanoUSD = -1
		assertBatchWriterRejectsRowsWithoutChanges(t, db, nil, []models.RequestLog{row})
	})
}

func TestWriteBatchRollsBackEarlierBucketWhenLaterUpsertFails(t *testing.T) {
	db := openRequestLogQueryDB(t)
	hour := time.Date(2026, time.July, 24, 16, 0, 0, 0, time.UTC)
	if err := db.Exec(`
		CREATE TRIGGER reject_second_bucket
		BEFORE INSERT ON usage_stats
		WHEN NEW.model = 'model-b'
		BEGIN
		  SELECT CASE
		    WHEN COALESCE((
		      SELECT request_count
		      FROM usage_stats
		      WHERE bucket_start_ms = NEW.bucket_start_ms
		        AND access_key_id = NEW.access_key_id
		        AND group_id = NEW.group_id
		        AND model = 'model-a'
		    ), 0) != 1
		    THEN RAISE(ABORT, 'first bucket was not upserted')
		  END;
		  SELECT RAISE(ABORT, 'second bucket rejected');
		END
	`).Error; err != nil {
		t.Fatalf("create ordered rejection trigger: %v", err)
	}

	first := aggregationRow(aggregationRequestID(50), hour, 13, "model-a")
	second := aggregationRow(aggregationRequestID(51), hour, 13, "model-b")
	err := (&gormBatchWriter{db: db}).WriteBatch(
		context.Background(),
		[]models.RequestLog{second, first},
	)
	if err == nil || !strings.Contains(err.Error(), "second bucket rejected") {
		t.Fatalf("WriteBatch() error = %v, want rejection after first sorted bucket", err)
	}
	assertRequestLogAndUsageStatCounts(t, db, 0, 0)
}

func TestWorkerCountsDuplicateReplayAsSuccessfulDeliveryWithoutReaggregation(t *testing.T) {
	db := openRequestLogQueryDB(t)
	timers := newManualTimerFactory()
	service := NewService(
		db,
		redact.New(),
		staticRetentionPolicy{days: 7},
	)
	service.timerFactory = timers.New
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := testEvent(aggregationRequestID(60))
	event.UpstreamModel = "gpt-4o"
	event.UpstreamReportedModel = "gpt-4o"
	event.Attempts[0].UpstreamModel = "gpt-4o"
	event.Usage = telemetry.UsageObservation{
		GroupID: 14, ChannelID: channel.OpenAI, CredentialID: 8, AttemptSequence: 1,
		Result: usage.Result{
			State:  usage.StateComplete,
			Tokens: usage.Tokens{UncachedInput: 1_000_000, Output: 1_000_000},
		},
		Pricing: telemetry.PricingObservation{
			UpstreamModel: "gpt-4o",
			CostState:     string(pricing.CostStateUnpriced), PricingCompleteness: string(pricing.CompletenessUnavailable),
		},
	}
	event.Attempts[0].GroupID = 14
	for attempt := uint64(1); attempt <= 2; attempt++ {
		service.Emit(event)
		receiveValue(t, timers.created).Fire()
		waitForPersistedTotal(t, service, attempt)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	stats := service.Stats()
	if stats.PersistedTotal != 2 || stats.DroppedPersistFailedTotal != 0 ||
		stats.DroppedTotal != 0 {
		t.Fatalf("worker replay stats = %+v", stats)
	}
	assertRequestLogAndUsageStatCounts(t, db, 1, 1)
	var stat models.UsageStat
	if err := db.Take(&stat).Error; err != nil {
		t.Fatalf("query UsageStat: %v", err)
	}
	if stat.RequestCount != 1 {
		t.Fatalf("UsageStat.RequestCount = %d, want one after successful replay delivery", stat.RequestCount)
	}
}

func TestWriteBatchRollsBackUsageJournalWithFailedRequestLogTransaction(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := aggregationRow(
		aggregationRequestID(70),
		time.Date(2026, time.July, 24, 16, 30, 0, 0, time.UTC),
		17,
		"journal-model",
	)
	row.UncachedInputTokens = 12
	row.OutputTokens = 3

	if err := db.Exec(`CREATE TRIGGER reject_journal_request_log
		BEFORE INSERT ON request_logs
		BEGIN
			SELECT RAISE(FAIL, 'forced request log failure');
		END`).Error; err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}
	writer := &gormBatchWriter{db: db}
	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err == nil {
		t.Fatal("WriteBatch() error = nil, want request log transaction failure")
	}

	assertRequestLogAndUsageStatCounts(t, db, 0, 0)
	assertUsageJournalCount(t, db, 0)
}

func TestServiceStartDoesNotAggregateOrphanUsageJournal(t *testing.T) {
	db := openRequestLogQueryDB(t)
	journal := models.UsageAggregationJournal{
		RequestID:           aggregationRequestID(71),
		BucketStartMS:       1_784_901_600_000,
		AccessKeyID:         2,
		GroupID:             18,
		Model:               "startup-replay-model",
		RequestCount:        1,
		SuccessCount:        1,
		UncachedInputTokens: 21,
		OutputTokens:        8,
	}
	if err := db.Create(&journal).Error; err != nil {
		t.Fatalf("create pending journal: %v", err)
	}
	service := NewService(db, redact.New(), staticRetentionPolicy{days: 7})
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	assertRequestLogAndUsageStatCounts(t, db, 0, 0)
	if err := db.Where("request_id = ?", journal.RequestID).Take(&journal).Error; err != nil {
		t.Fatalf("query orphan journal: %v", err)
	}
	if journal.Applied {
		t.Fatalf("orphan journal was applied: %+v", journal)
	}
}

func assertRequestLogAndUsageStatCounts(
	t *testing.T,
	db *gorm.DB,
	wantRequestLogs int64,
	wantUsageStats int64,
) {
	t.Helper()
	var requestLogs, usageStats int64
	if err := db.Raw("SELECT COUNT(*) FROM request_logs").Scan(&requestLogs).Error; err != nil {
		t.Fatalf("count RequestLogs: %v", err)
	}
	if err := db.Raw("SELECT COUNT(*) FROM usage_stats").Scan(&usageStats).Error; err != nil {
		t.Fatalf("count UsageStats: %v", err)
	}
	if requestLogs != wantRequestLogs || usageStats != wantUsageStats {
		t.Fatalf("row counts = RequestLog:%d UsageStat:%d, want %d/%d",
			requestLogs, usageStats, wantRequestLogs, wantUsageStats)
	}
}

func assertUsageJournalCount(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(&models.UsageAggregationJournal{}).Count(&count).Error; err != nil {
		t.Fatalf("count UsageAggregationJournals: %v", err)
	}
	if count != want {
		t.Fatalf("UsageAggregationJournal count = %d, want %d", count, want)
	}
}

func assertBatchWriterRejectsRowsWithoutChanges(
	t *testing.T,
	db *gorm.DB,
	existing *models.UsageStat,
	rows []models.RequestLog,
) {
	t.Helper()
	if existing != nil {
		createCorruptUsageStats(t, db, *existing)
	}
	var before []models.UsageStat
	if err := db.Order("id ASC").Find(&before).Error; err != nil {
		t.Fatalf("query UsageStats before write: %v", err)
	}

	if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err == nil {
		t.Fatal("WriteBatch() error = nil, want checked arithmetic rejection")
	}

	var requestLogCount int64
	if err := db.Model(&models.RequestLog{}).Count(&requestLogCount).Error; err != nil {
		t.Fatalf("count RequestLogs: %v", err)
	}
	if requestLogCount != 0 {
		t.Fatalf("RequestLog count = %d, want rollback to zero", requestLogCount)
	}
	var after []models.UsageStat
	if err := db.Order("id ASC").Find(&after).Error; err != nil {
		t.Fatalf("query UsageStats after write: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("UsageStats changed after rejection: got %+v want %+v", after, before)
	}
}

func waitForPersistedTotal(t *testing.T, service *Service, want uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if service.Stats().PersistedTotal >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("PersistedTotal = %d, want at least %d", service.Stats().PersistedTotal, want)
}
