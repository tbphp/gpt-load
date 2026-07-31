package requestlog

import (
	"context"
	"math"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestQueryHomeStatisticsReturnsDenseEpochWindowsForEmptyData(t *testing.T) {
	const observedAtMS int64 = 1_784_896_496_789
	tests := []struct {
		name        string
		rangeValue  HomeStatisticsRange
		fromMS      int64
		toMS        int64
		bucketWidth int64
		buckets     int
	}{
		{
			name:        "24 hours",
			rangeValue:  HomeStatistics24H,
			fromMS:      1_784_811_600_000,
			toMS:        1_784_898_000_000,
			bucketWidth: 3_600_000,
			buckets:     24,
		},
		{
			name:        "30 days",
			rangeValue:  HomeStatistics30D,
			fromMS:      1_782_345_600_000,
			toMS:        1_784_937_600_000,
			bucketWidth: 86_400_000,
			buckets:     30,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := newRequestLogTestService(openRequestLogQueryDB(t)).
				QueryHomeStatistics(context.Background(), HomeStatisticsQuery{
					Range:        test.rangeValue,
					ObservedAtMS: observedAtMS,
				})
			if err != nil {
				t.Fatalf("QueryHomeStatistics() error = %v", err)
			}
			if report.Range != test.rangeValue || report.ObservedAtMS != observedAtMS ||
				report.FromMS != test.fromMS || report.ToMS != test.toMS ||
				len(report.Series) != test.buckets {
				t.Fatalf("window/report = %#v", report)
			}
			if report.Summary != (UsageAggregate{}) ||
				len(report.TopModels) != 0 ||
				len(report.TopGroups) != 0 ||
				len(report.TopAccessKeys) != 0 {
				t.Fatalf("empty aggregates/rankings = %#v", report)
			}
			for index, point := range report.Series {
				wantStart := test.fromMS + int64(index)*test.bucketWidth
				if point.BucketStartMS != wantStart ||
					point.BucketEndMS != wantStart+test.bucketWidth ||
					point.UsageAggregate != (UsageAggregate{}) {
					t.Fatalf("series[%d] = %#v", index, point)
				}
			}
		})
	}
}

func TestQueryHomeStatisticsBuildsTopFiveRankingsAndDeletedRefs(t *testing.T) {
	db := openRequestLogQueryDB(t)
	groupA := createHomeStatisticsGroup(t, db, "Group A")
	groupB := createHomeStatisticsGroup(t, db, "Group B")
	accessA := createHomeStatisticsAccessKey(t, db, "Access A", "0001")
	accessB := createHomeStatisticsAccessKey(t, db, "Access B", "0002")
	const bucketStartMS int64 = 1_784_894_400_000

	rows := []models.UsageStat{
		homeStatisticsStat(bucketStartMS, accessA.ID, groupA.ID, "z-model", 5, 500),
		homeStatisticsStat(bucketStartMS, accessA.ID, groupA.ID, "a-model", 5, 500),
		homeStatisticsStat(bucketStartMS, accessB.ID, groupB.ID, "a-model", 5, 500),
		homeStatisticsStat(bucketStartMS, 999, 999, "deleted-model", 100, 400),
		homeStatisticsStat(bucketStartMS, 0, 0, "", 20, 300),
		homeStatisticsStat(bucketStartMS, accessB.ID, groupB.ID, "low-model", 1, 200),
		homeStatisticsStat(bucketStartMS, accessB.ID, groupB.ID, "excluded-model", 1, 100),
	}
	createUsageStats(t, db, rows...)

	report, err := newRequestLogTestService(db).QueryHomeStatistics(
		context.Background(),
		HomeStatisticsQuery{
			Range:        HomeStatistics24H,
			ObservedAtMS: 1_784_896_496_789,
		},
	)
	if err != nil {
		t.Fatalf("QueryHomeStatistics() error = %v", err)
	}
	if report.Summary.RequestCount != 137 ||
		report.Summary.SuccessCount != 137 ||
		report.Summary.UncachedInputTokens != 1_370 ||
		report.Summary.EstimatedCostNanoUSD != 2_500 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.TopModels) != 5 ||
		len(report.TopGroups) != 4 ||
		len(report.TopAccessKeys) != 4 {
		t.Fatalf(
			"ranking lengths = %d/%d/%d",
			len(report.TopModels),
			len(report.TopGroups),
			len(report.TopAccessKeys),
		)
	}
	wantModels := []struct {
		groupID uint
		model   string
	}{
		{groupA.ID, "a-model"},
		{groupA.ID, "z-model"},
		{groupB.ID, "a-model"},
		{999, "deleted-model"},
		{0, ""},
	}
	for index, want := range wantModels {
		got := report.TopModels[index]
		if got.Group.ID != want.groupID || got.Model != want.model {
			t.Fatalf("TopModels[%d] = %#v, want group/model %d/%q", index, got, want.groupID, want.model)
		}
	}
	if report.TopModels[0].Group.Name == nil ||
		*report.TopModels[0].Group.Name != "Group A" ||
		report.TopModels[0].Group.Deleted {
		t.Fatalf("current group ref = %#v", report.TopModels[0].Group)
	}
	for _, index := range []int{3, 4} {
		if !report.TopModels[index].Group.Deleted ||
			report.TopModels[index].Group.Name != nil {
			t.Fatalf("deleted/zero group ref = %#v", report.TopModels[index].Group)
		}
	}
	if report.TopGroups[0].Group.ID != groupA.ID ||
		report.TopGroups[0].EstimatedCostNanoUSD != 1_000 ||
		report.TopGroups[1].Group.ID != groupB.ID ||
		report.TopGroups[1].EstimatedCostNanoUSD != 800 {
		t.Fatalf("TopGroups = %#v", report.TopGroups)
	}
	if report.TopAccessKeys[0].AccessKey.ID != accessA.ID ||
		report.TopAccessKeys[0].AccessKey.Name == nil ||
		*report.TopAccessKeys[0].AccessKey.Name != "Access A" ||
		report.TopAccessKeys[0].AccessKey.Deleted ||
		report.TopAccessKeys[1].AccessKey.ID != accessB.ID {
		t.Fatalf(
			"TopAccessKeys = %#v; first name = %q",
			report.TopAccessKeys,
			*report.TopAccessKeys[0].AccessKey.Name,
		)
	}
	for _, index := range []int{2, 3} {
		if !report.TopAccessKeys[index].AccessKey.Deleted ||
			report.TopAccessKeys[index].AccessKey.Name != nil {
			t.Fatalf("deleted/zero access ref = %#v", report.TopAccessKeys[index].AccessKey)
		}
	}
}

func TestQueryHomeStatisticsUsesOneReadSnapshot(t *testing.T) {
	db, dsn := openRequestLogFileDB(t)
	const bucketStartMS int64 = 1_784_894_400_000
	createUsageStats(
		t,
		db,
		homeStatisticsStat(bucketStartMS, 1, 1, "before", 1, 10),
	)

	writerDB, closeWriter := openUsageQueryWriterDB(t, dsn)
	defer closeWriter()
	inserted := false
	const callbackName = "test:home_statistics_snapshot_insert"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if inserted || tx.Statement.Table != "usage_stats" {
			return
		}
		inserted = true
		if err := writerDB.Create(&models.UsageStat{
			BucketStartMS:        bucketStartMS,
			AccessKeyID:          2,
			GroupID:              2,
			Model:                "after-first-read",
			RequestCount:         100,
			SuccessCount:         100,
			EstimatedCostNanoUSD: 1_000,
		}).Error; err != nil {
			t.Errorf("insert concurrent UsageStat: %v", err)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	report, err := newRequestLogTestService(db).QueryHomeStatistics(
		context.Background(),
		HomeStatisticsQuery{
			Range:        HomeStatistics24H,
			ObservedAtMS: 1_784_896_496_789,
		},
	)
	if err != nil {
		t.Fatalf("QueryHomeStatistics() error = %v", err)
	}
	if !inserted || report.Summary.RequestCount != 1 ||
		len(report.TopModels) != 1 || report.TopModels[0].Model != "before" ||
		len(report.TopGroups) != 1 || len(report.TopAccessKeys) != 1 {
		t.Fatalf("report did not retain one snapshot: inserted=%t report=%#v", inserted, report)
	}
}

func TestQueryHomeStatisticsRejectsUnsafeOrCorruptAggregates(t *testing.T) {
	const (
		bucketStartMS int64 = 1_784_894_400_000
		observedAtMS  int64 = 1_784_896_496_789
		maxSafeInt    int64 = 9_007_199_254_740_991
	)
	tests := []struct {
		name string
		rows []models.UsageStat
	}{
		{
			name: "unsafe request count",
			rows: []models.UsageStat{{
				BucketStartMS: bucketStartMS,
				AccessKeyID:   1,
				GroupID:       1,
				Model:         "unsafe-count",
				RequestCount:  maxSafeInt + 1,
				SuccessCount:  maxSafeInt + 1,
			}},
		},
		{
			name: "unsafe token count",
			rows: []models.UsageStat{{
				BucketStartMS:       bucketStartMS,
				AccessKeyID:         1,
				GroupID:             1,
				Model:               "unsafe-token",
				RequestCount:        1,
				SuccessCount:        1,
				UncachedInputTokens: maxSafeInt + 1,
			}},
		},
		{
			name: "request invariant mismatch",
			rows: []models.UsageStat{{
				BucketStartMS: bucketStartMS,
				AccessKeyID:   1,
				GroupID:       1,
				Model:         "bad-counts",
				RequestCount:  3,
				SuccessCount:  1,
				FailureCount:  1,
			}},
		},
		{
			name: "cost overflow",
			rows: []models.UsageStat{
				homeStatisticsStat(bucketStartMS, 1, 1, "cost-a", 1, math.MaxInt64),
				homeStatisticsStat(bucketStartMS, 2, 2, "cost-b", 1, math.MaxInt64),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			createCorruptUsageStats(t, db, test.rows...)
			if _, err := newRequestLogTestService(db).QueryHomeStatistics(
				context.Background(),
				HomeStatisticsQuery{
					Range:        HomeStatistics24H,
					ObservedAtMS: observedAtMS,
				},
			); err == nil {
				t.Fatal("QueryHomeStatistics() error = nil, want fail-closed rejection")
			}
		})
	}
}

func TestQueryHomeStatisticsRejectsInvalidQuery(t *testing.T) {
	service := newRequestLogTestService(openRequestLogQueryDB(t))
	for _, query := range []HomeStatisticsQuery{
		{Range: HomeStatisticsRange("1h"), ObservedAtMS: 1},
		{Range: HomeStatistics24H, ObservedAtMS: -1},
	} {
		if _, err := service.QueryHomeStatistics(context.Background(), query); err == nil {
			t.Fatalf("QueryHomeStatistics(%#v) error = nil", query)
		}
	}
}

func homeStatisticsStat(
	bucketStartMS int64,
	accessKeyID uint,
	groupID uint,
	model string,
	requestCount int64,
	cost int64,
) models.UsageStat {
	return models.UsageStat{
		BucketStartMS:        bucketStartMS,
		AccessKeyID:          accessKeyID,
		GroupID:              groupID,
		Model:                model,
		RequestCount:         requestCount,
		SuccessCount:         requestCount,
		UncachedInputTokens:  requestCount * 10,
		EstimatedCostNanoUSD: cost,
	}
}

func createHomeStatisticsGroup(t *testing.T, db *gorm.DB, name string) models.Group {
	t.Helper()
	row := models.Group{
		Name:        name,
		UpstreamURL: "https://example.invalid",
		Protocols:   models.JSON(`[]`),
		Models:      models.JSON(`[]`),
		Enabled:     true,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create Group %q: %v", name, err)
	}
	return row
}

func createHomeStatisticsAccessKey(
	t *testing.T,
	db *gorm.DB,
	name string,
	suffix string,
) models.AccessKey {
	t.Helper()
	row := models.AccessKey{
		Name:      name,
		KeyValue:  "encrypted-" + suffix,
		KeyHash:   "hash-" + suffix,
		KeySuffix: suffix,
		Status:    "active",
		Filters:   models.JSON(`{}`),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create AccessKey %q: %v", name, err)
	}
	return row
}
