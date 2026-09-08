package requestlog

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/models"
)

func TestQueryUsageExactWindowBoundaries(t *testing.T) {
	base := time.Date(2026, time.September, 8, 13, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		from, to time.Duration
		width    time.Duration
	}{
		{"within hour", 17 * time.Minute, 38 * time.Minute, 5 * time.Minute},
		{"short across hour", 57 * time.Minute, 63 * time.Minute, 5 * time.Minute},
		{"exact aligned hour", 0, time.Hour, 5 * time.Minute},
		{"exact rolling hour", 17 * time.Minute, 77 * time.Minute, 5 * time.Minute},
		{"aligned hour plus millisecond", 0, time.Hour + time.Millisecond, time.Hour},
		{"rolling hour plus millisecond", 17 * time.Minute, 77*time.Minute + time.Millisecond, time.Hour},
		{"full hours and boundaries", 17 * time.Minute, 190 * time.Minute, time.Hour},
		{"aligned full hours", 0, 3 * time.Hour, time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			from, to := base.Add(test.from), base.Add(test.to)
			rows := []models.RequestLog{
				aggregationRow(aggregationRequestID(800), from.Add(-time.Millisecond), 7, "model"),
				aggregationRow(aggregationRequestID(801), from, 7, "model"),
				aggregationRow(aggregationRequestID(802), from.Add(to.Sub(from)/2), 7, "model"),
				aggregationRow(aggregationRequestID(803), to.Add(-time.Millisecond), 7, "model"),
				aggregationRow(aggregationRequestID(804), to, 7, "model"),
			}
			if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err != nil {
				t.Fatal(err)
			}
			report, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
				FromMS: from.UnixMilli(), ToMS: to.UnixMilli(), Granularity: UsageGranularityHour,
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			if report.Summary.RequestCount != 3 {
				t.Fatalf("request count = %d, want exactly three in-range requests", report.Summary.RequestCount)
			}
			for _, point := range report.Series {
				bucketStart := point.BucketStartMS - point.BucketStartMS%test.width.Milliseconds()
				if point.BucketStartMS != max(bucketStart, from.UnixMilli()) ||
					point.BucketEndMS != min(bucketStart+test.width.Milliseconds(), to.UnixMilli()) {
					t.Fatalf("bucket = %#v, want clipped %s natural bucket", point, test.width)
				}
			}
			assertMinuteUsageReportTotals(t, report)
		})
	}
}

func TestResolveUsageTimeBucket(t *testing.T) {
	hour, day := epochms.MillisecondsPerHour, epochms.MillisecondsPerDay
	for _, test := range []struct {
		span        int64
		granularity UsageGranularity
		width       int64
	}{
		{1, UsageGranularityMinute, UsageFiveMinuteBucketMS},
		{hour, UsageGranularityMinute, UsageFiveMinuteBucketMS},
		{hour + 1, UsageGranularityHour, hour},
		{day, UsageGranularityHour, hour},
		{day + 1, UsageGranularityHour, 3 * hour},
		{3 * day, UsageGranularityHour, 3 * hour},
		{3*day + 1, UsageGranularityHour, 6 * hour},
		{7 * day, UsageGranularityHour, 6 * hour},
		{7*day + 1, UsageGranularityHour, 12 * hour},
		{15 * day, UsageGranularityHour, 12 * hour},
		{15*day + 1, UsageGranularityDay, day},
		{30 * day, UsageGranularityDay, day},
		{30*day + 1, UsageGranularityDay, 2 * day},
		{60 * day, UsageGranularityDay, 2 * day},
		{60*day + 1, UsageGranularityDay, 3 * day},
		{365 * day, UsageGranularityDay, 13 * day},
	} {
		t.Run(fmt.Sprint(test.span), func(t *testing.T) {
			granularity, width, err := ResolveUsageTimeBucket(123, 123+test.span)
			if err != nil || granularity != test.granularity || width != test.width {
				t.Fatalf("bucket = %s/%d/%v, want %s/%d", granularity, width, err, test.granularity, test.width)
			}
		})
	}
	const maximum int64 = 1<<53 - 1
	for _, bounds := range [][2]int64{{-1, hour}, {0, 0}, {1, 0}, {maximum, maximum + 1}, {0, math.MaxInt64}} {
		if _, _, err := ResolveUsageTimeBucket(bounds[0], bounds[1]); err == nil {
			t.Fatalf("bounds %v accepted, want invalid or unsafe interval rejected", bounds)
		}
	}
	if granularity, width, err := ResolveUsageTimeBucket(0, maximum); err != nil ||
		granularity != UsageGranularityDay || width <= 0 || (maximum-1)/width+1 > 30 {
		t.Fatalf("maximum safe span = %s/%d/%v", granularity, width, err)
	}
}

func TestQueryUsageMergesSourcesBeforeRankingEveryDimension(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 8, 14, 0, 0, 0, time.UTC)
	for groupID := uint(1); groupID <= 11; groupID++ {
		count := int64(6)
		if groupID > 5 {
			count = 0
		}
		if groupID == 11 {
			count = 5
		}
		stat := usageStat(start, groupID, fmt.Sprintf("model-%02d", groupID), count)
		stat.AccessKeyID = groupID
		stat.UncachedInputTokens, stat.OutputTokens = count, 2*count
		stat.CacheReadTokens, stat.CacheWrite5MTokens, stat.CacheWrite1HTokens = 0, 0, 0
		stat.EstimatedCostNanoUSD = count * 250_000_000
		createUsageStats(t, db, stat)
		if err := db.Create(&models.AccessKey{
			ID: groupID, Name: fmt.Sprintf("key-%d", groupID), KeyValue: fmt.Sprintf("cipher-%d", groupID),
			KeyHash: fmt.Sprintf("hash-%d", groupID), KeySuffix: "0000", Status: "active", Filters: models.JSON(`{}`),
		}).Error; err != nil {
			t.Fatal(err)
		}
		if groupID <= 5 {
			continue
		}
		logCount := 6
		if groupID == 11 {
			logCount = 5
		}
		for index := 0; index < logCount; index++ {
			row := aggregationRow(fmt.Sprintf("rank-%d-%d", groupID, index), start.Add(-time.Minute), groupID, stat.Model)
			row.AccessKeyID = groupID
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
		FromMS: start.Add(-30 * time.Minute).UnixMilli(), ToMS: start.Add(90 * time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 70 {
		t.Fatalf("summary count = %d, want 70", report.Summary.RequestCount)
	}
	for _, distributions := range []map[UsageDistributionMetric]UsageDistribution{report.Distributions.Group, report.Distributions.Model, report.Distributions.AccessKey} {
		for _, distribution := range distributions {
			if len(distribution.Items) != 5 || distribution.Items[0].RequestCount != 10 {
				t.Fatalf("%s/%s = %#v, want the source-local sixth item ranked first after merging", distribution.Dimension, distribution.Metric, distribution)
			}
			top := distribution.Items[0]
			if top.GroupID != 11 && top.AccessKeyID != 11 && top.Model != "model-11" {
				t.Fatalf("unexpected top item: %#v", top)
			}
		}
	}
	assertMinuteUsageReportTotals(t, report)
}

func TestQueryUsageMixedSourcesApplyAllFilters(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 8, 14, 0, 0, 0, time.UTC)
	for index := 0; index < 6; index++ {
		stat := usageStat(start, 7, "model", 1)
		stat.AccessKeyID, stat.CredentialID, stat.ChannelID = 41, 11, "openai"
		switch index {
		case 1:
			stat.GroupID = 8
		case 2:
			stat.AccessKeyID = 42
		case 3:
			stat.CredentialID = 12
		case 4:
			stat.ChannelID = "anthropic"
		case 5:
			stat.Model = "other"
		}
		createUsageStats(t, db, stat)
		for _, offset := range []time.Duration{-time.Minute, 61 * time.Minute} {
			row := aggregationRow(fmt.Sprintf("filter-%d-%d", index, offset), start.Add(offset), stat.GroupID, stat.Model)
			row.AccessKeyID, row.CredentialID, row.ChannelID = stat.AccessKeyID, stat.CredentialID, stat.ChannelID
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	groupID, credentialID, accessKeyID := uint(7), uint(11), uint(41)
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
		FromMS: start.Add(-30 * time.Minute).UnixMilli(), ToMS: start.Add(90 * time.Minute).UnixMilli(),
		GroupID: &groupID, CredentialID: &credentialID, AccessKeyID: &accessKeyID, ChannelID: channel.OpenAI, UpstreamModel: "model",
	})
	if err != nil || report.Summary.RequestCount != 3 {
		t.Fatalf("filtered count/error = %d/%v, want three requests across all sources", report.Summary.RequestCount, err)
	}
	assertMinuteUsageReportTotals(t, report)
}

func TestQueryUsageExactAccessKeyFilterKeepsAdministratorDistributions(t *testing.T) {
	for _, span := range []time.Duration{30 * time.Minute, 2*time.Hour + 2*time.Minute} {
		for _, persisted := range []bool{true, false} {
			for _, selfScoped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/persisted=%t/self=%t", span, persisted, selfScoped), func(t *testing.T) {
					db := openRequestLogQueryDB(t)
					from := time.Date(2026, time.September, 8, 13, 17, 0, 0, time.UTC)
					to := from.Add(span)
					keyID := uint(41)
					if persisted {
						if err := db.Create(&models.AccessKey{
							ID: keyID, Name: "filtered", KeyValue: "cipher-filtered", KeyHash: "hash-filtered",
							KeySuffix: "0041", Status: "active", Filters: models.JSON(`{}`),
						}).Error; err != nil {
							t.Fatal(err)
						}
					}
					if err := db.Create(&models.Group{
						ID: 7, Name: "filtered-group", ChannelID: "openai", Params: models.JSON(`{}`),
						Models: models.JSON(`[]`), Enabled: true,
					}).Error; err != nil {
						t.Fatal(err)
					}
					var rows []models.RequestLog
					for index, at := range []time.Time{from.Add(time.Minute), from.Add(span / 2), to.Add(-time.Minute)} {
						row := aggregationRow(fmt.Sprintf("filtered-%d", index), at, 7, "filtered-model")
						row.AccessKeyID = keyID
						rows = append(rows, row)
						other := row
						other.ID, other.AccessKeyID = fmt.Sprintf("other-%d", index), 42
						rows = append(rows, other)
					}
					if err := (&gormBatchWriter{db: db}).WriteBatch(t.Context(), rows); err != nil {
						t.Fatal(err)
					}
					report, err := newRequestLogTestService(db).QueryUsage(t.Context(), UsageQuery{
						FromMS: from.UnixMilli(), ToMS: to.UnixMilli(), AccessKeyID: &keyID, SelfScoped: selfScoped,
					})
					if err != nil || report.Summary.RequestCount != 3 {
						t.Fatalf("filtered request count/error = %d/%v, want three requests", report.Summary.RequestCount, err)
					}
					if selfScoped {
						if len(report.Distributions.Group) != 0 || len(report.Distributions.AccessKey) != 0 {
							t.Fatalf("self-scoped view exposes management dimensions: %#v", report.Distributions)
						}
					} else {
						group := usageDistribution(t, report, UsageDistributionDimensionGroup, UsageDistributionMetricRequests)
						if len(group.Items) != 1 || group.Items[0].GroupID != 7 || group.Items[0].RequestCount != 3 {
							t.Fatalf("administrator key filter lost group distribution: %#v", group)
						}
						key := usageDistribution(t, report, UsageDistributionDimensionAccessKey, UsageDistributionMetricRequests)
						if persisted {
							if len(key.Items) != 1 || key.Items[0].AccessKeyID != keyID || key.Items[0].RequestCount != 3 {
								t.Fatalf("administrator key filter lost access-key distribution: %#v", key)
							}
						} else if len(key.Items) != 0 || key.Other == nil || key.Other.RequestCount != 3 {
							t.Fatalf("historical key must remain in Other: %#v", key)
						}
					}
					assertMinuteUsageReportTotals(t, report)
				})
			}
		}
	}
}

func TestQueryUsageMixedSourcesRejectCorruptRowsBeforeCombining(t *testing.T) {
	for _, kind := range []string{"misaligned hour", "negative hourly count", "negative log tokens"} {
		t.Run(kind, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			start := time.Date(2026, time.September, 8, 14, 0, 0, 0, time.UTC)
			if err := db.Exec(`PRAGMA ignore_check_constraints = ON`).Error; err != nil {
				t.Fatal(err)
			}
			first, second := usageStat(start, 7, "model", 100), usageStat(start, 8, "model", 1)
			switch kind {
			case "misaligned hour":
				second.BucketStartMS += UsageFiveMinuteBucketMS
			case "negative hourly count":
				second.RequestCount, second.SuccessCount = -1, -1
			}
			createUsageStats(t, db, first, second)
			row := aggregationRow("corrupt-boundary", start.Add(-time.Minute), 7, "model")
			if kind == "negative log tokens" {
				row.UncachedInputTokens = -1
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
				FromMS: start.Add(-30 * time.Minute).UnixMilli(), ToMS: start.Add(90 * time.Minute).UnixMilli(),
			}); err == nil {
				t.Fatal("corrupt source accepted after other rows compensated its totals")
			}
		})
	}
}

func TestQueryUsageLongWindowAndMissingBoundaryLogs(t *testing.T) {
	db := openRequestLogQueryDB(t)
	from := time.Date(2025, time.September, 8, 13, 17, 0, 0, time.UTC)
	to := from.Add(365 * 24 * time.Hour)
	createUsageStats(t, db,
		usageStat(from.Truncate(time.Hour), 7, "boundary-without-logs", 100),
		usageStat(from.Truncate(time.Hour).Add(time.Hour), 7, "retained", 2),
		usageStat(to.Truncate(time.Hour).Add(-time.Hour), 7, "retained", 3),
		usageStat(to.Truncate(time.Hour), 7, "boundary-without-logs", 200),
	)
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
		FromMS: from.UnixMilli(), ToMS: to.UnixMilli(), Granularity: UsageGranularityDay,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 5 || len(report.Series) != 2 {
		t.Fatalf("report = %#v, want two retained buckets and five requests", report)
	}
	width := (13 * 24 * time.Hour).Milliseconds()
	for _, point := range report.Series {
		start := point.BucketStartMS - point.BucketStartMS%width
		if point.BucketStartMS != max(start, from.UnixMilli()) || point.BucketEndMS != min(start+width, to.UnixMilli()) {
			t.Fatalf("long-window bucket = %#v", point)
		}
	}
	assertMinuteUsageReportTotals(t, report)
}
