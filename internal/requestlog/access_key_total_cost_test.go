package requestlog

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
)

func TestQueryAccessKeyTotalCostUsesRetainedScopedAggregates(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	reader := service
	old := usageStat(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 7, "model", 10)
	old.AccessKeyID = 1
	recent := usageStat(time.Now().UTC().Truncate(time.Hour), 7, "model", 20)
	recent.AccessKeyID = 1
	other := old
	other.AccessKeyID = 2
	other.EstimatedCostNanoUSD = 50_000_000_000
	createUsageStats(t, db, old, recent, other)
	if err := db.Create(&models.AutoDecisionUsageStat{
		BucketStartMS: old.BucketStartMS, AccessKeyID: 1, GroupID: 7,
		Model: "decision", EstimatedCostNanoUSD: 500_000_000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id   uint
		want int64
	}{{1, 3_500_000_000}, {2, 50_000_000_000}, {3, 0}} {
		got, err := reader.QueryAccessKeyTotalCost(context.Background(), test.id)
		if err != nil || got != test.want {
			t.Fatalf("key %d: got %d, %v; want %d", test.id, got, err, test.want)
		}
	}
	if _, err := reader.QueryAccessKeyTotalCost(context.Background(), 0); err == nil {
		t.Fatal("zero key scope accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.QueryAccessKeyTotalCost(ctx, 1); err == nil {
		t.Fatal("canceled query succeeded")
	}
}
