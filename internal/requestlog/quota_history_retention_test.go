package requestlog

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestQuotaHistoryRetentionDoesNotFollowRequestLogRetention(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, 9, 17, 12, 34, 0, 0, time.UTC)
	cutoff := now.Add(-35 * 24 * time.Hour).UnixMilli()
	for _, at := range []int64{cutoff - 1, cutoff, now.Add(-10 * 24 * time.Hour).UnixMilli()} {
		row := models.CredentialQuotaHistory{GroupID: 1, CredentialID: 1, TargetIdentity: "identity", WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", ObservedAtMS: at, UsedBasisPoints: 1200}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	service.Sweep(t.Context(), now)
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ObservedAtMS != cutoff {
		t.Fatalf("quota history retention: %+v", rows)
	}
}

func TestQuotaHistoryRetentionReportsFailuresAndIgnoresCancellation(t *testing.T) {
	for _, operation := range []string{"query", "delete"} {
		for _, canceled := range []bool{false, true} {
			name := operation + "-failure"
			if canceled {
				name = operation + "-cancellation"
			}
			t.Run(name, func(t *testing.T) {
				db := openRequestLogQueryDB(t)
				service := newRequestLogTestService(db)
				var logs bytes.Buffer
				service.logger = newRequestLogJSONLogger(&logs)
				now := time.Date(2026, 9, 17, 12, 34, 0, 0, time.UTC)
				row := models.CredentialQuotaHistory{GroupID: 1, CredentialID: 1, TargetIdentity: "identity", WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", ObservedAtMS: now.Add(-36 * 24 * time.Hour).UnixMilli(), UsedBasisPoints: 1200}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				callback := func(tx *gorm.DB) {
					if tx.Statement.Table != "credential_quota_histories" {
						return
					}
					if canceled {
						cancel()
						tx.AddError(ctx.Err())
					} else {
						tx.AddError(errors.New("forced quota history retention failure"))
					}
				}
				var err error
				if operation == "query" {
					err = db.Callback().Query().Before("gorm:query").Register("test:quota_history_retention_failure", callback)
				} else {
					err = db.Callback().Delete().Before("gorm:delete").Register("test:quota_history_retention_failure", callback)
				}
				if err != nil {
					t.Fatal(err)
				}
				service.Sweep(ctx, now)
				stats := service.Stats()
				if canceled {
					if stats.RetentionDeleteFailureTotal != 0 || !stats.LastRetentionFailureAt.IsZero() || logs.Len() != 0 {
						t.Fatalf("cancellation reported as retention failure: stats=%+v logs=%s", stats, logs.String())
					}
				} else if stats.RetentionDeleteFailureTotal != 1 || !stats.LastRetentionFailureAt.Equal(now) {
					t.Fatalf("retention failure missing from monitoring: %+v", stats)
				}
			})
		}
	}
}
