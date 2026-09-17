package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/storage/models"
)

func TestQuotaHistoryRetentionDoesNotFollowRequestLogRetention(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
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
