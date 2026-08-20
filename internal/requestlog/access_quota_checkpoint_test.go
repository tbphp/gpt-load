package requestlog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/testutil/sqlitetest"
)

func TestAccessQuotaCheckpointWriterAppliesAndRetriesAbsoluteSnapshot(t *testing.T) {
	db := sqlitetest.OpenMigrated(t)
	accessKey, rule := createCheckpointRule(t, db)
	writer := &gormAccessQuotaCheckpointWriter{db: db}
	startedAt := int64(1_787_184_000_000)
	endsAt := startedAt + 300_000
	snapshot := accessquota.RestoredState{
		AccessKeyID: accessKey.ID, RuleID: rule.ID, RuleRevision: 1,
		UsedNanoUSD: 75, WindowStartedAtMS: &startedAt, WindowEndsAtMS: &endsAt,
		WindowGeneration: 2, SnapshotVersion: 4,
	}
	if err := writer.WriteSnapshots(t.Context(), []accessquota.RestoredState{snapshot}); err != nil {
		t.Fatalf("first WriteSnapshots() error = %v", err)
	}
	if err := writer.WriteSnapshots(t.Context(), []accessquota.RestoredState{snapshot}); err != nil {
		t.Fatalf("same-version retry error = %v", err)
	}
	var persisted models.AccessKeyCostLimitState
	if err := db.First(&persisted, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.UsedNanoUSD != 75 || persisted.SnapshotVersion != 4 ||
		persisted.WindowGeneration != 2 || persisted.WindowStartedAtMS == nil ||
		*persisted.WindowStartedAtMS != startedAt {
		t.Fatalf("persisted checkpoint = %#v", persisted)
	}
}

func TestAccessQuotaCheckpointWriterClassifiesStaleDeletedAndMissingState(t *testing.T) {
	t.Run("stale revision is discarded", func(t *testing.T) {
		db := sqlitetest.OpenMigrated(t)
		accessKey, rule := createCheckpointRule(t, db)
		if err := db.Model(&models.AccessKeyCostLimitRule{}).Where("id = ?", rule.ID).
			Update("rule_revision", 2).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&models.AccessKeyCostLimitState{}).Where("rule_id = ?", rule.ID).
			Update("rule_revision", 2).Error; err != nil {
			t.Fatal(err)
		}
		writer := &gormAccessQuotaCheckpointWriter{db: db}
		err := writer.WriteSnapshots(t.Context(), []accessquota.RestoredState{{
			AccessKeyID: accessKey.ID, RuleID: rule.ID, RuleRevision: 1,
			UsedNanoUSD: 99, SnapshotVersion: 5,
		}})
		if err != nil {
			t.Fatalf("stale WriteSnapshots() error = %v", err)
		}
		var persisted models.AccessKeyCostLimitState
		if err := db.First(&persisted, rule.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.RuleRevision != 2 || persisted.UsedNanoUSD != 0 {
			t.Fatalf("stale snapshot changed state = %#v", persisted)
		}
	})

	t.Run("deleted rule is discarded", func(t *testing.T) {
		db := sqlitetest.OpenMigrated(t)
		accessKey, rule := createCheckpointRule(t, db)
		if err := db.Delete(&rule).Error; err != nil {
			t.Fatal(err)
		}
		writer := &gormAccessQuotaCheckpointWriter{db: db}
		if err := writer.WriteSnapshots(t.Context(), []accessquota.RestoredState{{
			AccessKeyID: accessKey.ID, RuleID: rule.ID, RuleRevision: 1,
			UsedNanoUSD: 99, SnapshotVersion: 5,
		}}); err != nil {
			t.Fatalf("deleted WriteSnapshots() error = %v", err)
		}
	})

	t.Run("existing rule without state fails", func(t *testing.T) {
		db := sqlitetest.OpenMigrated(t)
		accessKey, rule := createCheckpointRule(t, db)
		if err := db.Delete(&models.AccessKeyCostLimitState{}, rule.ID).Error; err != nil {
			t.Fatal(err)
		}
		writer := &gormAccessQuotaCheckpointWriter{db: db}
		if err := writer.WriteSnapshots(t.Context(), []accessquota.RestoredState{{
			AccessKeyID: accessKey.ID, RuleID: rule.ID, RuleRevision: 1,
			UsedNanoUSD: 99, SnapshotVersion: 5,
		}}); err == nil {
			t.Fatal("WriteSnapshots() error = nil, want missing state failure")
		}
	})
}

func TestRequestLogServiceFlushesAndAcknowledgesQuotaDirtyStateWithoutLogEvent(t *testing.T) {
	db := sqlitetest.OpenMigrated(t)
	accessKey, rule := createCheckpointRule(t, db)
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{accessKey.ID: {
		{
			ID: rule.ID, Revision: 1, Kind: accessquota.KindPeriodic,
			LimitNanoUSD: 100, PeriodSeconds: 300,
		},
	}}); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, redact.New(), staticRetentionPolicy{days: 7}, runtime)
	ticket, decision := runtime.Admit(accessKey.ID, time.Unix(100, 0))
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	runtime.Complete(ticket, 75)
	service.writeBatch(t.Context(), nil)

	var persisted models.AccessKeyCostLimitState
	if err := db.First(&persisted, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.UsedNanoUSD != 75 || persisted.SnapshotVersion <= 1 {
		t.Fatalf("persisted checkpoint = %#v", persisted)
	}
	if dirty := runtime.DirtySnapshots(10); len(dirty) != 0 {
		t.Fatalf("dirty after checkpoint = %#v", dirty)
	}
}

func TestRequestLogWorkerWakesForQuotaCheckpointAndDrainsOnStop(t *testing.T) {
	db := sqlitetest.OpenMigrated(t)
	accessKey, rule := createCheckpointRule(t, db)
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{accessKey.ID: {
		{ID: rule.ID, Revision: 1, Kind: accessquota.KindPeriodic, LimitNanoUSD: 100, PeriodSeconds: 300},
	}}); err != nil {
		t.Fatal(err)
	}
	timers := newManualTimerFactory()
	service := NewService(db, redact.New(), staticRetentionPolicy{days: 7}, runtime)
	service.timerFactory = timers.New
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	ticket, decision := runtime.Admit(accessKey.ID, time.Unix(100, 0))
	if !decision.Allowed {
		t.Fatalf("Admit() = %#v", decision)
	}
	runtime.Complete(ticket, 75)
	receiveValue(t, timers.created).Fire()
	waitForCheckpointCost(t, db, rule.ID, 75)

	runtime.Complete(ticket, 5)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitForCheckpointCost(t, db, rule.ID, 80)
}

func TestRequestLogWorkerDrainsEveryQuotaCheckpointBatchOnStop(t *testing.T) {
	runtime := accessquota.NewRuntime()
	definitions := make(map[uint][]accessquota.Rule, batchSize+1)
	for index := 1; index <= batchSize+1; index++ {
		definitions[uint(index)] = []accessquota.Rule{{
			ID: uint(index), Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
		}}
	}
	if err := runtime.Reconcile(definitions); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= batchSize+1; index++ {
		ticket, decision := runtime.Admit(uint(index), time.Unix(100, 0))
		if !decision.Allowed {
			t.Fatalf("Admit(%d) = %#v", index, decision)
		}
		runtime.Complete(ticket, 1)
	}

	timers := newManualTimerFactory()
	batchSizes := make([]int, 0, 2)
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		timers.New,
	)
	service.accessQuota = runtime
	service.quotaWake = make(chan struct{}, 1)
	service.quotaWriter = accessQuotaCheckpointWriterFunc(func(
		_ context.Context,
		snapshots []accessquota.RestoredState,
	) error {
		batchSizes = append(batchSizes, len(snapshots))
		return nil
	})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(batchSizes) != 2 || batchSizes[0] != batchSize || batchSizes[1] != 1 {
		t.Fatalf("checkpoint batch sizes = %v, want [%d 1]", batchSizes, batchSize)
	}
	if dirty := runtime.DirtySnapshots(1); len(dirty) != 0 {
		t.Fatalf("dirty checkpoints after stop = %#v", dirty)
	}
}

func TestRequestLogWorkerReportsFinalQuotaCheckpointFailureOnStop(t *testing.T) {
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {{
		ID: 302, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	ticket, _ := runtime.Admit(1, time.Unix(100, 0))
	runtime.Complete(ticket, 75)

	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		newManualTimerFactory().New,
	)
	service.accessQuota = runtime
	service.quotaWake = make(chan struct{}, 1)
	service.quotaWriter = accessQuotaCheckpointWriterFunc(func(
		context.Context,
		[]accessquota.RestoredState,
	) error {
		return errors.New("checkpoint unavailable")
	})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	err := service.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "final access key cost limit checkpoint") {
		t.Fatalf("Stop() error = %v, want final checkpoint failure", err)
	}
	if dirty := runtime.DirtySnapshots(1); len(dirty) != 1 {
		t.Fatalf("dirty checkpoints after failed stop = %#v", dirty)
	}
}

func TestRequestLogWorkerRetriesQuotaCheckpointWithoutClearingDirtyVersion(t *testing.T) {
	runtime := accessquota.NewRuntime()
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: {{
		ID: 301, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	timers := newManualTimerFactory()
	writes := make(chan []accessquota.RestoredState, 2)
	writeCount := 0
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		timers.New,
	)
	service.accessQuota = runtime
	service.quotaWake = make(chan struct{}, 1)
	service.quotaWriter = accessQuotaCheckpointWriterFunc(func(
		_ context.Context,
		snapshots []accessquota.RestoredState,
	) error {
		writes <- append([]accessquota.RestoredState(nil), snapshots...)
		writeCount++
		if writeCount == 1 {
			return errors.New("checkpoint unavailable")
		}
		return nil
	})
	runtime.SetDirtyNotifier(service.wakeAccessQuotaCheckpoint)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	ticket, _ := runtime.Admit(1, time.Unix(100, 0))
	runtime.Complete(ticket, 75)
	receiveValue(t, timers.created).Fire()
	first := receiveValue(t, writes)
	failureDeadline := time.Now().Add(time.Second)
	for {
		stats := service.Stats()
		if stats.AccessQuotaCheckpointDegraded && stats.AccessQuotaCheckpointWriteFailureTotal == 1 {
			break
		}
		if !time.Now().Before(failureDeadline) {
			t.Fatalf("checkpoint state after failed write = %#v", stats)
		}
		time.Sleep(time.Millisecond)
	}
	receiveValue(t, timers.created).Fire()
	second := receiveValue(t, writes)
	if len(first) != 1 || len(second) != 1 ||
		first[0].SnapshotVersion != second[0].SnapshotVersion ||
		first[0].UsedNanoUSD != second[0].UsedNanoUSD {
		t.Fatalf("checkpoint retries = %#v / %#v", first, second)
	}
	deadline := time.Now().Add(time.Second)
	for len(runtime.DirtySnapshots(1)) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if dirty := runtime.DirtySnapshots(1); len(dirty) != 0 {
		t.Fatalf("dirty after successful retry = %#v", dirty)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 0 || stats.AccessQuotaCheckpointWriteFailureTotal != 1 ||
		stats.AccessQuotaCheckpointDegraded ||
		stats.LastAccessQuotaCheckpointWriteFailureAt.IsZero() ||
		stats.DroppedPersistFailedTotal != 0 {
		t.Fatalf("checkpoint failure stats = %#v", stats)
	}
}

type accessQuotaCheckpointWriterFunc func(context.Context, []accessquota.RestoredState) error

func (fn accessQuotaCheckpointWriterFunc) WriteSnapshots(
	ctx context.Context,
	snapshots []accessquota.RestoredState,
) error {
	return fn(ctx, snapshots)
}

func createCheckpointRule(t *testing.T, db *gorm.DB) (models.AccessKey, models.AccessKeyCostLimitRule) {
	t.Helper()
	accessKey := models.AccessKey{
		Name: "checkpoint", KeyValue: "cipher", KeyHash: "checkpoint-hash",
		KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&accessKey).Error; err != nil {
		t.Fatal(err)
	}
	rule := models.AccessKeyCostLimitRule{
		AccessKeyID: accessKey.ID, Kind: models.AccessKeyCostLimitKindPeriodic,
		LimitNanoUSD: 100, PeriodSeconds: 300, RuleRevision: 1,
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.AccessKeyCostLimitState{
		RuleID: rule.ID, RuleRevision: 1, SnapshotVersion: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return accessKey, rule
}

func waitForCheckpointCost(t *testing.T, db *gorm.DB, ruleID uint, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var state models.AccessKeyCostLimitState
		if err := db.First(&state, ruleID).Error; err != nil {
			t.Fatal(err)
		}
		if state.UsedNanoUSD == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("checkpoint cost = %d, want %d", state.UsedNanoUSD, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
