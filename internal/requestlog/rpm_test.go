package requestlog

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/rpm"
	"gpt-load/internal/storage/models"
)

func TestRPMWorkerPersistsAbsoluteSnapshotsAndMergesLiveData(t *testing.T) {
	db := openRequestLogQueryDB(t)
	if err := db.AutoMigrate(&models.RPMStat{}); err != nil {
		t.Fatal(err)
	}
	s := newRequestLogTestService(db)
	store := rpm.NewStore()
	s.SetRPMStore(store)
	now := time.Date(2026, 9, 22, 1, 0, 10, 0, time.UTC)
	s.now = func() time.Time { return now }
	store.Record(rpm.AccessKey, 1, false, now)
	s.flushRPMCheckpoints(t.Context())
	s.flushRPMCheckpoints(t.Context())
	store.Record(rpm.AccessKey, 1, true, now)
	report, err := s.QueryRPM(t.Context(), rpm.AccessKey, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Requests != 2 || report.Rejected != 1 || report.Peak == nil || *report.Peak != 2 {
		t.Fatalf("persisted/live merge: %+v", report)
	}
	s.flushRPMCheckpoints(t.Context())
	var rows []models.RPMStat
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Requests != 2 {
		t.Fatalf("absolute checkpoint = %+v", rows)
	}
	// 重启后同一分钟的数据累加请求数，但独立计算实际观测到的滑窗峰值。
	store = rpm.NewStore()
	s.SetRPMStore(store)
	store.Record(rpm.AccessKey, 1, false, now)
	s.flushRPMCheckpoints(t.Context())
	report, err = s.QueryRPM(t.Context(), rpm.AccessKey, 1, now)
	if err != nil || report.Requests != 3 || report.Peak == nil || *report.Peak != 2 {
		t.Fatalf("restart merge: %+v, %v", report, err)
	}
}

func TestRPMWriteFailureIsIsolatedAndRetried(t *testing.T) {
	db := openRequestLogQueryDB(t)
	if err := db.AutoMigrate(&models.RPMStat{}); err != nil {
		t.Fatal(err)
	}
	s := newRequestLogTestService(db)
	store := rpm.NewStore()
	s.SetRPMStore(store)
	now := time.Now()
	s.now = func() time.Time { return now }
	store.Record(rpm.AccessKey, 1, false, now)
	if err := db.Callback().Create().Before("gorm:create").Register("test:rpm_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "rpm_stats" {
			tx.AddError(errors.New("RPM write failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.writeBatch(t.Context(), nil); err != nil {
		t.Fatalf("RPM error escaped worker: %v", err)
	}
	if s.Stats().WriteFailureTotal != 0 {
		t.Fatal("RPM failure changed request-log failure counter")
	}
	if len(store.Snapshots(now, true, 10)) == 0 {
		t.Fatal("failed batch was acknowledged")
	}
	if err := db.Callback().Create().Remove("test:rpm_failure"); err != nil {
		t.Fatal(err)
	}
	s.flushRPMCheckpoints(t.Context())
	if len(store.Snapshots(now, true, 10)) != 0 {
		t.Fatal("successful retry was not acknowledged")
	}
}

func TestRPMRetentionFollowsLogPolicy(t *testing.T) {
	db := openRequestLogQueryDB(t)
	if err := db.AutoMigrate(&models.RPMStat{}); err != nil {
		t.Fatal(err)
	}
	s := newRequestLogTestService(db)
	now := time.Now().Truncate(time.Minute)
	cutoff := now.Add(-7 * 24 * time.Hour)
	for _, at := range []time.Time{cutoff.Add(-time.Minute), cutoff} {
		if err := db.Create(&models.RPMStat{Kind: "access_key", KeyID: 1, MinuteMS: at.UnixMilli(), SessionID: "test", Requests: 1, Peak: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	s.Sweep(t.Context(), now)
	var rows []models.RPMStat
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MinuteMS != cutoff.UnixMilli() {
		t.Fatalf("retained rows: %+v", rows)
	}
}
