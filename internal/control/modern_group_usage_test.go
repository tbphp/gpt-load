package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestModernGroupUsageBatchesExactRollingWindow(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.September, 11, 10, 20, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = requestlog.NewService(fixture.db, nil, nil)
	for groupID := uint(1); groupID <= 8; groupID++ {
		for index, at := range []time.Time{from.Add(-time.Millisecond), from, from.Add(time.Hour), now.Add(-time.Millisecond), now} {
			row := models.RequestLog{
				ID: fmt.Sprintf("batch-%d-%d", groupID, index), CompletedAtMS: at.UnixMilli(),
				GroupID: groupID, ChannelID: "openai", CredentialID: groupID, AccessKeyID: 1,
				Protocol: "openai-completions", ClientModel: "test", UpstreamModel: "test",
				Status: "success", StatusCode: 200, AttemptCount: 3,
				UsageState: "complete", CostState: "priced", PricingCompleteness: "complete",
				UncachedInputTokens: 3, OutputTokens: 5, EstimatedCostNanoUSD: 700,
			}
			if err := fixture.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		for index, at := range []time.Time{from.Truncate(time.Hour), from.Truncate(time.Hour).Add(time.Hour), now.Truncate(time.Hour)} {
			count := int64(99)
			if index == 1 {
				count = 25
			}
			if err := fixture.db.Create(&models.UsageStat{
				BucketStartMS: at.UnixMilli(), GroupID: groupID, ChannelID: "openai", CredentialID: groupID,
				AccessKeyID: 1, Model: "test", RequestCount: count, SuccessCount: count,
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	result := performGroupCollectionRequest(engine, "/api/modern/groups/usage?group_ids=1,2,3,4,5,6,7,9", "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("batch usage status = %d: %s", result.Code, result.Body.String())
	}
	var data struct {
		FromMS int64 `json:"from_ms"`
		ToMS   int64 `json:"to_ms"`
		Items  []struct {
			GroupID uint `json:"group_id"`
			usageAggregateResponse
		} `json:"items"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if data.FromMS != from.UnixMilli() || data.ToMS != now.UnixMilli() || len(data.Items) != 8 {
		t.Fatalf("window or batch changed: %+v", data)
	}
	for _, row := range data.Items {
		if row.GroupID == 8 {
			t.Fatal("unrequested group included")
		}
		if row.GroupID == 9 {
			if row.RequestCount != 0 || row.EstimatedCostNanoUSD != "0" {
				t.Fatalf("empty group has usage: %+v", row)
			}
			continue
		}
		if row.RequestCount != 27 || row.TotalTokens != 16 || row.EstimatedCostNanoUSD != "1400" {
			t.Fatalf("group %d: %+v; want 25 hourly + 2 boundary requests counted once", row.GroupID, row)
		}
	}
}

func TestModernGroupUsageRejectsInvalidBatchesAndReadOnlyKeys(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.usageStats = requestlog.NewService(fixture.db, nil, nil)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "read only"})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	for _, query := range []string{"", "group_ids=", "group_ids=0", "group_ids=1,1", "group_ids=01", "group_ids=-1", "group_ids=1&group_ids=2", "group_ids=1&from_ms=0", "group_ids=" + strings.Repeat("1,", 100) + "2"} {
		result := performGroupCollectionRequest(engine, "/api/modern/groups/usage?"+query, "Bearer "+authTestKey)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d", query, result.Code)
		}
	}
	for _, item := range []struct {
		auth   string
		status int
	}{{"", http.StatusUnauthorized}, {"Bearer " + key.Key, http.StatusForbidden}} {
		result := performGroupCollectionRequest(engine, "/api/modern/groups/usage?group_ids=1", item.auth)
		if result.Code != item.status {
			t.Fatalf("scope status = %d, want %d", result.Code, item.status)
		}
	}
}
