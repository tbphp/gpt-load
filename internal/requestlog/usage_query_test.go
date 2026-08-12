package requestlog

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

func TestQueryUsageHourAggregatesFiltersAndLeavesSparseBucketsAbsent(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)

	rows := make([]models.UsageStat, 0, 26)
	for hour := 0; hour < 24; hour++ {
		if hour == 7 {
			continue
		}
		rows = append(rows, usageStat(start.Add(time.Duration(hour)*time.Hour), 7, "target", int64(hour+1)))
	}
	rows = append(rows,
		usageStat(start.Add(3*time.Hour), 8, "target", 500),
		usageStat(start.Add(4*time.Hour), 7, "other", 600),
		usageStat(start.Add(7*time.Hour), 8, "target", 700),
	)
	rows[0].PartialCount = 1
	createUsageStats(t, db, rows...)

	groupID := uint(7)
	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:        start.UnixMilli(),
		ToMS:          start.Add(24 * time.Hour).UnixMilli(),
		Granularity:   UsageGranularityHour,
		GroupID:       &groupID,
		UpstreamModel: "target",
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 292 || report.Summary.SuccessCount != 292 ||
		report.Summary.FailureCount != 0 || report.Summary.UncachedInputTokens != 2920 ||
		report.Summary.CacheReadTokens != 292 || report.Summary.CacheWrite5MTokens != 20 ||
		report.Summary.CacheWrite1HTokens != 49 || report.Summary.OutputTokens != 584 ||
		report.Summary.EstimatedCostNanoUSD != 29_200_000_000 ||
		report.Summary.UsageMissingCount != 12 ||
		report.Summary.PartialCount != 1 || report.Summary.UnpricedRequestCount != 12 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Series) != 23 {
		t.Fatalf("series length = %d, want 23 actual hour buckets", len(report.Series))
	}
	for _, point := range report.Series {
		if point.BucketStartMS == start.Add(7*time.Hour).UnixMilli() {
			t.Fatalf("series synthesized missing hour: %#v", point)
		}
		if point.BucketEndMS-point.BucketStartMS != 3_600_000 {
			t.Fatalf(
				"hour point duration = %dms, want 3600000",
				point.BucketEndMS-point.BucketStartMS,
			)
		}
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, UsageDistributionMetricRequests)
	if len(distribution.Items) != 1 || distribution.Items[0].GroupID != 7 ||
		distribution.Items[0].RequestCount != 292 || distribution.Other != nil {
		t.Fatalf("filtered distribution = %#v", distribution)
	}
}

func TestQueryUsageExcludesLegacyZeroAttemptAggregates(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 14, 0, 0, 0, time.UTC)
	attributed := usageStat(start, 7, "model-a", 3)
	attributed.AccessKeyID = 1
	legacyZeroAttempt := models.UsageStat{
		BucketStartMS: start.UnixMilli(),
		AccessKeyID:   1,
		GroupID:       0,
		Model:         "",
		RequestCount:  2,
		FailureCount:  2,
	}
	createUsageStats(t, db, attributed, legacyZeroAttempt)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 3 || report.Summary.SuccessCount != 3 ||
		report.Summary.FailureCount != 0 {
		t.Fatalf("summary = %#v, want only attributed requests", report.Summary)
	}
	if len(report.Series) != 1 || report.Series[0].RequestCount != 3 ||
		report.Series[0].FailureCount != 0 {
		t.Fatalf("series = %#v, want only attributed requests", report.Series)
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, UsageDistributionMetricRequests)
	if len(distribution.Items) != 1 || distribution.Items[0].GroupID != 7 ||
		distribution.Items[0].RequestCount != 3 || distribution.Other != nil {
		t.Fatalf("distribution = %#v", distribution)
	}
}

func TestQueryUsageScopesAccessKeyAndDistributesByModel(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 15, 0, 0, 0, time.UTC)

	firstGroup := usageStat(start, 7, "shared-model", 2)
	firstGroup.AccessKeyID = 41
	secondGroup := usageStat(start, 8, "shared-model", 3)
	secondGroup.AccessKeyID = 41
	otherModel := usageStat(start, 8, "other-model", 1)
	otherModel.AccessKeyID = 41
	otherAccessKey := usageStat(start, 7, "shared-model", 100)
	otherAccessKey.AccessKeyID = 42
	createUsageStats(t, db, firstGroup, secondGroup, otherModel, otherAccessKey)

	accessKeyID := uint(41)
	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
		AccessKeyID: &accessKeyID,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionModel, UsageDistributionMetricRequests)
	if report.Summary.RequestCount != 6 || len(distribution.Items) != 2 {
		t.Fatalf("scoped report = %#v", report)
	}
	if distribution.Items[0].GroupID != 0 ||
		distribution.Items[0].Model != "shared-model" ||
		distribution.Items[0].RequestCount != 5 {
		t.Fatalf("model distribution[0] = %#v", distribution.Items[0])
	}
	if distribution.Items[1].GroupID != 0 ||
		distribution.Items[1].Model != "other-model" ||
		distribution.Items[1].RequestCount != 1 {
		t.Fatalf("model distribution[1] = %#v", distribution.Items[1])
	}
}

func TestQueryUsageRejectsZeroAccessKeyScope(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 16, 0, 0, 0, time.UTC)
	zero := uint(0)

	_, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
		AccessKeyID: &zero,
	})
	if err == nil {
		t.Fatal("QueryUsage() error = nil, want zero AccessKey scope rejection")
	}
}

func TestQueryUsageDistributionAggregatesCredentialsAndFoldsRemainder(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC)

	rows := make([]models.UsageStat, 0, 8)
	for credentialID := uint(1); credentialID <= 3; credentialID++ {
		row := usageStat(start, 1, "shared-model", int64(11-credentialID))
		row.CredentialID = credentialID
		rows = append(rows, row)
	}
	for groupID := uint(2); groupID <= 7; groupID++ {
		row := usageStat(start, groupID, fmt.Sprintf("model-%d", groupID), int64(10-groupID))
		row.CredentialID = groupID + 20
		rows = append(rows, row)
	}
	createUsageStats(t, db, rows...)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, UsageDistributionMetricRequests)
	if len(distribution.Items) != 5 || distribution.Items[0].GroupID != 1 ||
		distribution.Items[0].RequestCount != 27 {
		t.Fatalf("group distribution = %#v, want credentials aggregated into top five", distribution)
	}
	if distribution.Other == nil || distribution.Other.RequestCount != 7 {
		t.Fatalf("other distribution = %#v, want remaining two groups aggregated", distribution.Other)
	}
	if report.Summary.RequestCount != 60 {
		t.Fatalf("summary request count = %d, want 60", report.Summary.RequestCount)
	}
}

func TestQueryUsageGroupDistributionKeepsOnlyPersistedGroupsInTopFive(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 0, 30, 0, 0, time.UTC).Truncate(time.Hour)

	for _, groupID := range []uint{1, 2} {
		if err := db.Create(&models.Group{
			ID:        groupID,
			Name:      fmt.Sprintf("known-group-%d", groupID),
			ChannelID: "openai",
			Params:    models.JSON(`{}`),
			Models:    models.JSON(`[]`),
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("create known group %d: %v", groupID, err)
		}
	}

	knownFirst := usageStat(start, 1, "known-first", 30)
	knownFirst.EstimatedCostNanoUSD = 3_000_000_000
	knownSecond := usageStat(start, 2, "known-second", 20)
	knownSecond.EstimatedCostNanoUSD = 2_000_000_000
	deleted := usageStat(start, 90, "deleted", 1_000)
	deleted.EstimatedCostNanoUSD = 100_000_000_000
	unknown := usageStat(start, 91, "unknown", 900)
	unknown.EstimatedCostNanoUSD = 90_000_000_000
	createUsageStatsWithoutGroups(t, db, knownFirst, knownSecond, deleted, unknown)

	for _, metric := range []UsageDistributionMetric{
		UsageDistributionMetricRequests,
		UsageDistributionMetricCost,
	} {
		report, err := service.QueryUsage(context.Background(), UsageQuery{
			FromMS:      start.UnixMilli(),
			ToMS:        start.Add(time.Hour).UnixMilli(),
			Granularity: UsageGranularityHour,
		})
		if err != nil {
			t.Fatalf("QueryUsage(%s) error = %v", metric, err)
		}
		distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, metric)
		if len(distribution.Items) != 2 ||
			distribution.Items[0].GroupID != 1 ||
			distribution.Items[1].GroupID != 2 {
			t.Fatalf("%s distribution items = %#v, want only persisted groups", metric, distribution.Items)
		}
		if distribution.Other == nil ||
			distribution.Other.RequestCount != 1_900 ||
			distribution.Other.EstimatedCostNanoUSD != 190_000_000_000 {
			t.Fatalf("%s distribution other = %#v, want deleted and unknown totals", metric, distribution.Other)
		}
	}
}

func TestQueryUsageAccessKeyTokenDistributionKeepsOnlyPersistedKeys(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)

	knownKeys := []models.AccessKey{
		{
			Name: "request-heavy", KeyValue: "cipher-0001", KeyHash: "hash-0001",
			KeySuffix: "0001", Status: "active", Filters: models.JSON(`{}`),
		},
		{
			Name: "token-heavy", KeyValue: "cipher-0002", KeyHash: "hash-0002",
			KeySuffix: "0002", Status: "active", Filters: models.JSON(`{}`),
		},
	}
	if err := db.Create(&knownKeys).Error; err != nil {
		t.Fatalf("create persisted AccessKeys: %v", err)
	}

	requestHeavy := usageStat(start, 1, "request-heavy", 20)
	requestHeavy.AccessKeyID = knownKeys[0].ID
	requestHeavy.UncachedInputTokens = 100
	requestHeavy.CacheReadTokens = 0
	requestHeavy.CacheWrite5MTokens = 0
	requestHeavy.CacheWrite1HTokens = 0
	requestHeavy.OutputTokens = 0

	tokenHeavy := usageStat(start, 1, "token-heavy", 5)
	tokenHeavy.AccessKeyID = knownKeys[1].ID
	tokenHeavy.UncachedInputTokens = 500
	tokenHeavy.CacheReadTokens = 0
	tokenHeavy.CacheWrite5MTokens = 0
	tokenHeavy.CacheWrite1HTokens = 0
	tokenHeavy.OutputTokens = 0

	deletedOrUnknown := usageStat(start, 1, "deleted-key", 100)
	deletedOrUnknown.AccessKeyID = 99_999
	deletedOrUnknown.UncachedInputTokens = 1_000
	deletedOrUnknown.CacheReadTokens = 0
	deletedOrUnknown.CacheWrite5MTokens = 0
	deletedOrUnknown.CacheWrite1HTokens = 0
	deletedOrUnknown.OutputTokens = 0
	createUsageStats(t, db, requestHeavy, tokenHeavy, deletedOrUnknown)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	distribution := usageDistribution(
		t,
		report,
		UsageDistributionDimensionAccessKey,
		UsageDistributionMetricTokens,
	)
	if len(distribution.Items) != 2 ||
		distribution.Items[0].AccessKeyID != knownKeys[1].ID ||
		distribution.Items[0].TotalTokens != 500 ||
		distribution.Items[1].AccessKeyID != knownKeys[0].ID ||
		distribution.Items[1].TotalTokens != 100 {
		t.Fatalf("token distribution items = %#v", distribution.Items)
	}
	if distribution.Other == nil ||
		distribution.Other.RequestCount != 100 ||
		distribution.Other.TotalTokens != 1_000 ||
		distribution.Other.EstimatedCostNanoUSD != 10_000_000_000 {
		t.Fatalf("token distribution other = %#v", distribution.Other)
	}
}

func TestQueryUsageModelDistributionAggregatesGroupsAndCredentials(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC)
	rows := []models.UsageStat{
		usageStat(start, 1, "shared-model", 3),
		usageStat(start, 1, "shared-model", 4),
		usageStat(start, 2, "shared-model", 5),
		usageStat(start, 2, "other-model", 2),
	}
	for index := range rows {
		rows[index].CredentialID = uint(index + 1)
	}
	createUsageStats(t, db, rows...)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionModel, UsageDistributionMetricRequests)
	if len(distribution.Items) != 2 || distribution.Items[0].Model != "shared-model" ||
		distribution.Items[0].RequestCount != 12 || distribution.Other != nil {
		t.Fatalf("model distribution = %#v", distribution)
	}
}

func TestQueryUsageMergesHourlyRowsIntoThirtyUTCDays(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]models.UsageStat, 0, 60)
	for day := 0; day < 30; day++ {
		bucket := start.AddDate(0, 0, day)
		rows = append(rows,
			usageStat(bucket.Add(2*time.Hour), 9, "merge", 1),
			usageStat(bucket.Add(18*time.Hour), 9, "merge", 2),
		)
	}
	createUsageStats(t, db, rows...)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.AddDate(0, 0, 30).UnixMilli(),
		Granularity: UsageGranularityDay,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(report.Series) != 30 || report.Summary.RequestCount != 90 {
		t.Fatalf("day series/summary = %d/%#v, want 30/90", len(report.Series), report.Summary)
	}
	for day, point := range report.Series {
		wantStart := start.AddDate(0, 0, day)
		if point.BucketStartMS != wantStart.UnixMilli() ||
			point.BucketEndMS != wantStart.AddDate(0, 0, 1).UnixMilli() ||
			point.RequestCount != 3 || point.SuccessCount != 3 || point.FailureCount != 0 ||
			point.UncachedInputTokens != 30 || point.CacheReadTokens != 3 || point.OutputTokens != 6 ||
			point.EstimatedCostNanoUSD != 300_000_000 ||
			point.UsageMissingCount != 1 || point.UnpricedRequestCount != 1 {
			t.Fatalf("day %d point = %#v", day, point)
		}
	}
}

func TestQueryUsageMergesHourlyRowsIntoAdaptiveBuckets(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	for _, width := range []time.Duration{3 * time.Hour, 6 * time.Hour, 12 * time.Hour} {
		t.Run(width.String(), func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := newRequestLogTestService(db)
			createUsageStats(
				t,
				db,
				usageStat(start, 9, "merge", 1),
				usageStat(start.Add(width-time.Hour), 9, "merge", 2),
				usageStat(start.Add(width), 9, "merge", 3),
				usageStat(start.Add(2*width-time.Hour), 9, "merge", 4),
			)

			report, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:        start.UnixMilli(),
				ToMS:          start.Add(2 * width).UnixMilli(),
				Granularity:   UsageGranularityHour,
				BucketWidthMS: width.Milliseconds(),
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			if len(report.Series) != 2 || report.Summary.RequestCount != 10 {
				t.Fatalf("adaptive series/summary = %#v/%#v, want two buckets/10 requests", report.Series, report.Summary)
			}
			for index, wantRequests := range []int64{3, 7} {
				point := report.Series[index]
				wantStart := start.Add(time.Duration(index) * width)
				if point.BucketStartMS != wantStart.UnixMilli() ||
					point.BucketEndMS != wantStart.Add(width).UnixMilli() ||
					point.RequestCount != wantRequests || point.SuccessCount != wantRequests ||
					point.UncachedInputTokens != wantRequests*10 ||
					point.CacheReadTokens != wantRequests ||
					point.OutputTokens != wantRequests*2 ||
					point.EstimatedCostNanoUSD != wantRequests*100_000_000 {
					t.Fatalf("adaptive point %d = %#v", index, point)
				}
			}
		})
	}
}

func TestQueryUsageRejectsInvalidBucketWidths(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		from        time.Time
		to          time.Time
		granularity UsageGranularity
		width       time.Duration
	}{
		{name: "not whole hours", from: start, to: start.Add(5 * time.Hour), granularity: UsageGranularityHour, width: 150 * time.Minute},
		{name: "hour bucket is not aligned", from: start.Add(time.Hour), to: start.Add(7 * time.Hour), granularity: UsageGranularityHour, width: 3 * time.Hour},
		{name: "hour range is not divisible", from: start, to: start.Add(5 * time.Hour), granularity: UsageGranularityHour, width: 3 * time.Hour},
		{name: "daily granularity requires a day", from: start, to: start.Add(24 * time.Hour), granularity: UsageGranularityDay, width: 12 * time.Hour},
		{name: "hour granularity cannot use a day", from: start, to: start.Add(24 * time.Hour), granularity: UsageGranularityHour, width: 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := newRequestLogTestService(db)
			_, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:        test.from.UnixMilli(),
				ToMS:          test.to.UnixMilli(),
				Granularity:   test.granularity,
				BucketWidthMS: test.width.Milliseconds(),
			})
			if err == nil {
				t.Fatal("QueryUsage() error = nil, want invalid bucket width rejection")
			}
		})
	}
}

func TestQueryUsageOrdersDistributionByRequestsAndCost(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 1, 0, 0, 0, time.UTC)
	requestHeavy := usageStat(start, 1, "request-heavy", 20)
	requestHeavy.EstimatedCostNanoUSD = 1_000_000_000
	expensiveLowRequests := usageStat(start, 2, "expensive-low", 5)
	expensiveLowRequests.EstimatedCostNanoUSD = 10_000_000_000
	expensiveHighRequests := usageStat(start, 3, "expensive-high", 7)
	expensiveHighRequests.EstimatedCostNanoUSD = 10_000_000_000
	createUsageStats(t, db, requestHeavy, expensiveLowRequests, expensiveHighRequests)

	tests := []struct {
		name   string
		metric UsageDistributionMetric
		want   []uint
	}{
		{
			name:   "requests use cost then identity tie breakers",
			metric: UsageDistributionMetricRequests,
			want:   []uint{1, 3, 2},
		},
		{
			name:   "cost uses requests then identity tie breakers",
			metric: UsageDistributionMetricCost,
			want:   []uint{3, 2, 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:      start.UnixMilli(),
				ToMS:        start.Add(time.Hour).UnixMilli(),
				Granularity: UsageGranularityHour,
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, test.metric)
			if distribution.Dimension != UsageDistributionDimensionGroup ||
				distribution.Metric != test.metric {
				t.Fatalf("distribution metadata = %#v", distribution)
			}
			if len(distribution.Items) != len(test.want) {
				t.Fatalf("distribution = %#v, want %d rows", distribution, len(test.want))
			}
			for index, wantGroupID := range test.want {
				if distribution.Items[index].GroupID != wantGroupID {
					t.Fatalf(
						"distribution[%d].GroupID = %d, want %d; report=%#v",
						index,
						distribution.Items[index].GroupID,
						wantGroupID,
						distribution,
					)
				}
			}
		})
	}
}

func TestQueryUsageUsesOneReadSnapshot(t *testing.T) {
	db, dsn := openRequestLogFileDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 3, 0, 0, 0, 0, time.UTC)
	createUsageStats(t, db, usageStat(start, 1, "before", 1))

	writerDB, closeWriter := openUsageQueryWriterDB(t, dsn)
	defer closeWriter()
	inserted := false
	const callbackName = "test:usage_query_snapshot_insert"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if inserted || tx.Statement.Table != "usage_stats" {
			return
		}
		inserted = true
		if err := writerDB.Create(&models.UsageStat{
			BucketStartMS: start.UnixMilli(),
			GroupID:       2,
			Model:         "after-summary",
			RequestCount:  1,
			SuccessCount:  1,
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

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	distribution := usageDistribution(t, report, UsageDistributionDimensionGroup, UsageDistributionMetricRequests)
	if !inserted || report.Summary.RequestCount != 1 || len(report.Series) != 1 ||
		len(distribution.Items) != 1 || distribution.Items[0].GroupID != 1 {
		t.Fatalf("report did not retain one snapshot: inserted=%t report=%#v", inserted, report)
	}
}

func TestQueryUsageCancelsAfterBeginWithoutPoisoningDatabaseConnection(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 4, 0, 0, 0, 0, time.UTC)
	createUsageStats(t, db, usageStat(start, 1, "cancelled", 1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	const callbackName = "test:usage_query_cancel_after_begin"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if cancelled || tx.Statement.Table != "usage_stats" {
			return
		}
		cancelled = true
		cancel()
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	defer func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	}()

	_, err := service.QueryUsage(ctx, UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	})
	if err == nil || !cancelled {
		t.Fatalf("QueryUsage() error/cancelled = %v/%t, want cancelled query", err, cancelled)
	}

	var count int64
	if err := db.Model(&models.UsageStat{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("query after cancellation = %d/%v, want 1/nil", count, err)
	}
	if err := db.Create(&models.UsageStat{
		BucketStartMS: start.UnixMilli(),
		GroupID:       2,
		Model:         "after-cancellation",
		RequestCount:  1,
		SuccessCount:  1,
	}).Error; err != nil {
		t.Fatalf("write after cancellation: %v", err)
	}
}

func TestQueryUsageRejectsCorruptRowsOutsideTopDistribution(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 5, 0, 0, 0, 0, time.UTC)
	rows := make([]models.UsageStat, 0, 102)
	for groupID := uint(1); groupID <= 100; groupID++ {
		rows = append(rows, usageStat(start, groupID, "top", 3))
	}
	compensating := usageStat(start, 101, "compensating", 2)
	compensating.SuccessCount = 2
	compensating.FailureCount = 0
	compensating.UncachedInputTokens = 1
	compensating.EstimatedCostNanoUSD = 1_000_000_000
	corrupt := usageStat(start, 102, "corrupt", 1)
	corrupt.SuccessCount = 1
	corrupt.FailureCount = 0
	corrupt.UncachedInputTokens = -1
	rows = append(rows, compensating, corrupt)
	for groupID := uint(1); groupID <= 102; groupID++ {
		if err := db.Create(&models.Group{
			ID:        groupID,
			Name:      fmt.Sprintf("integrity-group-%d", groupID),
			ChannelID: "openai",
			Params:    models.JSON(`{}`),
			Models:    models.JSON(`[]`),
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("create integrity group %d: %v", groupID, err)
		}
	}
	createCorruptUsageStats(t, db, rows...)

	input := UsageQuery{
		FromMS:      start.UnixMilli(),
		ToMS:        start.Add(time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	}
	summary, err := queryUsageSummary(usageStatScope(db, input))
	if err != nil || summary.RequestCount != 303 {
		t.Fatalf("pre-integrity summary = %#v/%v, want valid 303-request aggregate", summary, err)
	}
	if series, err := queryUsageSeries(usageStatScope(db, input), time.Hour.Milliseconds()); err != nil || len(series) != 1 {
		t.Fatalf("pre-integrity series = %#v/%v, want one valid hour aggregate", series, err)
	}
	if distribution, err := queryUsageDistribution(
		usageStatScope(db, input),
		summary,
		UsageDistributionDimensionGroup,
		UsageDistributionMetricRequests,
	); err != nil || len(distribution.Items) != 5 || distribution.Items[4].GroupID != 5 {
		t.Fatalf("pre-integrity distribution = %#v/%v, want valid top five", distribution, err)
	}

	_, err = service.QueryUsage(context.Background(), input)
	if err == nil {
		t.Fatal("QueryUsage() error = nil, want corrupt row outside top distribution rejection")
	}
}

func usageDistribution(
	t *testing.T,
	report UsageReport,
	dimension UsageDistributionDimension,
	metric UsageDistributionMetric,
) UsageDistribution {
	t.Helper()
	distribution, ok := report.Distributions.Get(dimension, metric)
	if !ok {
		t.Fatalf("distribution %s/%s unavailable: %#v", dimension, metric, report.Distributions)
	}
	return distribution
}

func TestQueryUsageRejectsCorruptAggregates(t *testing.T) {
	start := time.Date(2026, time.July, 4, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows []models.UsageStat
	}{
		{
			name: "negative count",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative", 1)
				row.FailureCount = -1
				return row
			}()},
		},
		{
			name: "negative input tokens",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative-input", 1)
				row.UncachedInputTokens = -1
				return row
			}()},
		},
		{
			name: "negative cached tokens",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative-cache", 1)
				row.CacheWrite5MTokens = -1
				return row
			}()},
		},
		{
			name: "request count mismatch",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "mismatch", 3)
				row.SuccessCount = 1
				row.FailureCount = 1
				return row
			}()},
		},
		{
			name: "checked total token overflow",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "token-overflow", 1)
				row.UncachedInputTokens = math.MaxInt64
				row.OutputTokens = 1
				return row
			}()},
		},
		{
			name: "checked count overflow",
			rows: []models.UsageStat{
				{
					BucketStartMS: start.UnixMilli(),
					GroupID:       1,
					Model:         "overflow-a",
					RequestCount:  math.MaxInt64,
					SuccessCount:  math.MaxInt64,
				},
				{
					BucketStartMS: start.UnixMilli(),
					GroupID:       2,
					Model:         "overflow-b",
					RequestCount:  1,
					SuccessCount:  1,
				},
			},
		},
		{
			name: "checked cost overflow",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "huge-cost-a", 1)
				row.EstimatedCostNanoUSD = math.MaxInt64
				return row
			}(), func() models.UsageStat {
				row := usageStat(start, 2, "huge-cost-b", 1)
				row.EstimatedCostNanoUSD = math.MaxInt64
				return row
			}()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			createCorruptUsageStats(t, db, test.rows...)
			_, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
				FromMS:      start.UnixMilli(),
				ToMS:        start.Add(time.Hour).UnixMilli(),
				Granularity: UsageGranularityHour,
			})
			if err == nil {
				t.Fatal("QueryUsage() error = nil, want corrupt aggregate rejection")
			}
		})
	}
}

func usageStat(hour time.Time, groupID uint, model string, requestCount int64) models.UsageStat {
	return models.UsageStat{
		BucketStartMS:        hour.UTC().UnixMilli(),
		GroupID:              groupID,
		Model:                model,
		RequestCount:         requestCount,
		SuccessCount:         requestCount,
		UncachedInputTokens:  requestCount * 10,
		OutputTokens:         requestCount * 2,
		CacheReadTokens:      requestCount,
		CacheWrite5MTokens:   requestCount / 10,
		CacheWrite1HTokens:   requestCount / 5,
		EstimatedCostNanoUSD: requestCount * 100_000_000,
		UsageMissingCount:    requestCount % 2,
		PartialCount:         0,
		UnpricedRequestCount: requestCount % 2,
	}
}

func createUsageStats(t *testing.T, db *gorm.DB, rows ...models.UsageStat) {
	t.Helper()
	groupIDs := make(map[uint]struct{})
	for _, row := range rows {
		if row.GroupID == 0 {
			continue
		}
		groupIDs[row.GroupID] = struct{}{}
	}
	for groupID := range groupIDs {
		var count int64
		if err := db.Model(&models.Group{}).Where("id = ?", groupID).Count(&count).Error; err != nil {
			t.Fatalf("count group %d: %v", groupID, err)
		}
		if count > 0 {
			continue
		}
		if err := db.Create(&models.Group{
			ID:        groupID,
			Name:      fmt.Sprintf("usage-group-%d", groupID),
			ChannelID: "openai",
			Params:    models.JSON(`{}`),
			Models:    models.JSON(`[]`),
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("create usage group %d: %v", groupID, err)
		}
	}
	createUsageStatsWithoutGroups(t, db, rows...)
}

func createUsageStatsWithoutGroups(t *testing.T, db *gorm.DB, rows ...models.UsageStat) {
	t.Helper()
	for _, row := range rows {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create UsageStat %#v: %v", row, err)
		}
	}
}

func createCorruptUsageStats(t *testing.T, db *gorm.DB, rows ...models.UsageStat) {
	t.Helper()
	if err := db.Exec(`PRAGMA ignore_check_constraints = ON`).Error; err != nil {
		t.Fatalf("disable SQLite CHECK constraints: %v", err)
	}
	for _, row := range rows {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create corrupt UsageStat %#v: %v", row, err)
		}
	}
	if err := db.Exec(`PRAGMA ignore_check_constraints = OFF`).Error; err != nil {
		t.Fatalf("restore SQLite CHECK constraints: %v", err)
	}
}

func openUsageQueryWriterDB(t *testing.T, dsn string) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(
		gormsqlite.Open(dsn+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open concurrent UsageStat writer: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get concurrent UsageStat writer DB: %v", err)
	}
	return db, func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close concurrent UsageStat writer: %v", err)
		}
	}
}
