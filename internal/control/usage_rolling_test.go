package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestUsageAPIRollingHourReadsLogsAndScopesAccessKey(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "rolling usage viewer"})
	if err != nil {
		t.Fatalf("create usage access key: %v", err)
	}
	now := time.Date(2026, time.September, 7, 14, 5, 23, 123_000_000, time.UTC)
	from := now.Add(-time.Hour)
	for index, item := range []struct {
		completedAt time.Time
		accessKeyID uint
		attempts    int
	}{
		{from.Add(-time.Millisecond), key.ID, 1},
		{from, key.ID, 1},
		{now.Add(-time.Millisecond), key.ID, 1},
		{now, key.ID, 1},
		{now.Add(-time.Minute), key.ID, 0},
		{now.Add(-time.Second), key.ID + 1, 1},
	} {
		row := models.RequestLog{
			ID:            fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1),
			CompletedAtMS: item.completedAt.UnixMilli(), AccessKeyID: item.accessKeyID,
			GroupID: 7, ChannelID: "openai", CredentialID: 11,
			Protocol: "openai-completions", ClientModel: "usage-model", UpstreamModel: "usage-model",
			Status: "success", StatusCode: 200, AttemptCount: item.attempts,
			UsageState: "complete", CostState: "priced", PricingCompleteness: "complete",
			UncachedInputTokens: 3, OutputTokens: 5, EstimatedCostNanoUSD: 700,
		}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatalf("create usage request log: %v", err)
		}
	}
	if err := fixture.db.Create(&models.UsageStat{
		BucketStartMS: from.Truncate(time.Hour).UnixMilli(), AccessKeyID: key.ID,
		GroupID: 7, ChannelID: "openai", CredentialID: 11, Model: "usage-model",
		RequestCount: 99, SuccessCount: 99,
	}).Error; err != nil {
		t.Fatalf("create independent hourly usage stat: %v", err)
	}
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = requestlog.NewService(fixture.db, nil, nil)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	for _, test := range []struct {
		name      string
		auth      string
		query     string
		wantCount int64
		wantCost  string
		wantRange string
		wantGrain requestlog.UsageGranularity
	}{
		{"management rolling hour", "test-auth-key", "range=1h", 3, "2100", "1h", requestlog.UsageGranularityMinute},
		{"access key rolling hour", key.Key, "range=1h", 2, "1400", "1h", requestlog.UsageGranularityMinute},
		{"daily range retains hourly source", "test-auth-key", "range=24h", 99, "0", "24h", requestlog.UsageGranularityHour},
		{"custom hour retains hourly source and metadata", "test-auth-key",
			fmt.Sprintf("from_ms=%d&to_ms=%d", from.Truncate(time.Hour).UnixMilli(), from.Truncate(time.Hour).Add(time.Hour).UnixMilli()),
			99, "0", "1h", requestlog.UsageGranularityHour},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := performUsageRequest(engine, test.auth, test.query)
			if recorder.Code != http.StatusOK {
				t.Fatalf("usage response = %d %s", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Data usageResponse `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode usage response: %v", err)
			}
			data := envelope.Data
			if data.Range != test.wantRange || data.Granularity != test.wantGrain {
				t.Fatalf("usage range/granularity = %s/%s, want %s/%s", data.Range, data.Granularity, test.wantRange, test.wantGrain)
			}
			if data.Summary.RequestCount != test.wantCount || data.Summary.EstimatedCostNanoUSD != test.wantCost {
				t.Fatalf("usage summary = %+v", data.Summary)
			}
			if test.query == "range=1h" {
				if len(data.Series) != 2 || data.Series[0].BucketStartMS != from.UnixMilli() ||
					data.Series[1].BucketEndMS != now.UnixMilli() || data.Granularity != "minute" {
					t.Fatalf("rolling usage series = %+v", data.Series)
				}
				if data.Series[0].RequestCount+data.Series[1].RequestCount != test.wantCount {
					t.Fatalf("series differs from summary: %+v", data.Series)
				}
				for _, distribution := range data.Distributions.Model {
					if len(distribution.Items) != 1 || distribution.Items[0].RequestCount != test.wantCount ||
						distribution.Items[0].EstimatedCostNanoUSD != test.wantCost {
						t.Fatalf("model distribution differs from summary: %+v", distribution)
					}
				}
			}
			if test.auth == key.Key && (data.Distributions.Group != nil || data.Distributions.AccessKey != nil ||
				data.CollectionHealth.Scope != "access_key") {
				t.Fatalf("access key response exposes management scope: %+v", data)
			}
		})
	}
}
