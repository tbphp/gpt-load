package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

func TestAccessKeyDistributionCollectionMatchesUsageAndSortsBeforePagination(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := time.Date(2026, 9, 7, 13, 20, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	fixture.service.usageStats = requestlog.NewService(fixture.db, nil, nil)
	key1, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour).UnixMilli()
	key2, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "second", ExpiresAtMS: &expires})
	if err != nil {
		t.Fatal(err)
	}
	window, apiErr := parseUsageQuery("range=7d", now.UnixMilli())
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for index, row := range []models.UsageStat{
		{AccessKeyID: key1.ID, BucketStartMS: window.FromMS, RequestCount: 1, SuccessCount: 1, UncachedInputTokens: 2, OutputTokens: 3, EstimatedCostNanoUSD: 100},
		{AccessKeyID: key2.ID, BucketStartMS: window.FromMS, RequestCount: 2, SuccessCount: 2, CacheReadTokens: 7, CacheWriteUnknownTokens: 11, EstimatedCostNanoUSD: 200},
		{AccessKeyID: key1.ID, BucketStartMS: window.FromMS - 3600000, RequestCount: 10, SuccessCount: 10, EstimatedCostNanoUSD: 1000},
		{AccessKeyID: key1.ID, BucketStartMS: window.ToMS, RequestCount: 20, SuccessCount: 20, EstimatedCostNanoUSD: 2000},
	} {
		row.GroupID = 7
		row.ChannelID = "openai"
		row.Model = fmt.Sprintf("model-%d", index)
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	for _, tc := range []struct {
		query  string
		want   uint
		count  int64
		cost   string
		tokens int64
		total  int64
	}{
		{"sort=cost_desc&page_size=1", key2.ID, 2, "200", 18, 2},
		{"sort=cost_desc&page_size=1&page=2", key1.ID, 1, "100", 5, 2},
		{"sort=expires_asc&page_size=1", key2.ID, 2, "200", 18, 2},
	} {
		t.Run(tc.query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/access-keys?"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+authTestKey)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("collection = %d %s", w.Code, w.Body.String())
			}
			var result struct {
				Data struct {
					Items []struct {
						ID    uint                               `json:"id"`
						Usage usageDistributionAggregateResponse `json:"usage"`
					}
					Pagination  AccessKeyCollectionPagination `json:"pagination"`
					UsageWindow AccessKeyUsageWindow          `json:"usage_window"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.UsageWindow.Range != "7d" || result.Data.UsageWindow.FromMS != window.FromMS || result.Data.UsageWindow.ToMS != window.ToMS {
				t.Fatalf("collection window = %+v, want fixed 7-day window", result.Data.UsageWindow)
			}
			if len(result.Data.Items) != 1 || result.Data.Items[0].ID != tc.want || result.Data.Items[0].Usage.RequestCount != tc.count || result.Data.Items[0].Usage.EstimatedCostNanoUSD != tc.cost || result.Data.Items[0].Usage.TotalTokens != tc.tokens || result.Data.Pagination.TotalItems != tc.total {
				t.Fatalf("collection = %+v", result)
			}
		})
	}
	recorder := performUsageRequest(engine, authTestKey, fmt.Sprintf("range=7d&access_key_id=%d", key2.ID))
	if recorder.Code != 200 {
		t.Fatalf("usage = %d %s", recorder.Code, recorder.Body.String())
	}
	var report struct{ Data usageResponse }
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Data.Summary.RequestCount != 2 || report.Data.Summary.EstimatedCostNanoUSD != "200" || report.Data.Summary.TotalTokens != 18 {
		t.Fatalf("usage = %+v", report.Data)
	}
}

func TestRemovedContactSettingIsNotExposed(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	key, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	home, err := fixture.service.ReadAccessKeyHomeBase(t.Context(), fixture.service.now().UnixMilli(), key.ID)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(serialized, &values); err != nil {
		t.Fatal(err)
	}
	if _, exists := values["contact_info"]; exists {
		t.Fatal("home still exposes removed contact information")
	}
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{"contact_info": json.RawMessage(`"removed"`)}}); err == nil {
		t.Fatal("removed contact setting can still be updated")
	}
}
