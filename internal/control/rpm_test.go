package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/rpm"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestModernSubscriptionRPMPeakDistinguishesEmptyFromFailure(t *testing.T) {
	zero, peak := int64(0), int64(9)
	for _, test := range []struct {
		name     string
		age      time.Duration
		fail     bool
		wantPeak *int64
	}{
		{name: "no records", wantPeak: &zero},
		{name: "recent records", age: time.Minute, wantPeak: &peak},
		{name: "expired records", age: 2 * time.Hour, wantPeak: &zero},
		{name: "query failure", fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initControlI18n(t)
			fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
			now := time.Now().Truncate(time.Minute)
			fixture.service.now = func() time.Time { return now }
			if test.age != 0 {
				if err := fixture.db.Create(&models.RPMStat{
					Kind: "credential", KeyID: credentialID, MinuteMS: now.Add(-test.age).UnixMilli(),
					SessionID: "test", Requests: 7, Peak: peak,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			reader := requestlog.NewService(fixture.db, redact.New(), rpmTestRetention{})
			fixture.service.usageStats = reader
			if test.fail {
				fixture.service.usageStats = failingRPMPeaksReader{reader}
			}
			engine := gin.New()
			NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
			result := performGroupCollectionRequest(engine,
				fmt.Sprintf("/api/modern/groups/%d/credentials", groupID), "Bearer "+authTestKey)
			if result.Code != http.StatusOK {
				t.Fatalf("collection status=%d body=%s", result.Code, result.Body.String())
			}
			var data struct {
				Items []ModernCredentialItem `json:"items"`
			}
			if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
				t.Fatal(err)
			}
			if len(data.Items) != 1 || data.Items[0].CredentialID != credentialID {
				t.Fatalf("unexpected credential collection: %+v", data.Items)
			}
			got := data.Items[0].RPMPeakHour
			if test.wantPeak == nil {
				if got != nil {
					t.Fatalf("query failure reported RPM %d", *got)
				}
			} else if got == nil || *got != *test.wantPeak {
				t.Fatalf("expected RPM %d, response=%s", *test.wantPeak, result.Body.String())
			}
		})
	}
}

type failingRPMPeaksReader struct{ *requestlog.Service }

func (failingRPMPeaksReader) RPMPeaks(context.Context, rpm.Kind, []uint, time.Time) (map[uint]int64, error) {
	return nil, fmt.Errorf("test RPM query failed")
}

func TestRPMCollectionsSortBeforePagination(t *testing.T) {
	one, high := int64(1), int64(80)
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "first", "0001", state.AccessKeyStatusActive, 300),
		accessKeyCollectionQueryRecord(2, "second", "0002", state.AccessKeyStatusActive, 200),
		accessKeyCollectionQueryRecord(3, "third", "0003", state.AccessKeyStatusActive, 100),
	}
	records[0].RPMPeakHour = &one
	records[2].RPMPeakHour = &high
	result := queryAccessKeyCollectionRecords(records, AccessKeyCollectionQuery{Sort: "rpm_peak_desc", Page: 1, PageSize: 1})
	if len(result.Items) != 1 || result.Items[0].ID != 3 {
		t.Fatalf("RPM sort paged too early: %+v", result.Items)
	}
	if !modernCredentialLess(credentialCollectionRecord{item: CredentialItemResponse{CredentialID: 3, RPMPeakHour: &high}}, credentialCollectionRecord{item: CredentialItemResponse{CredentialID: 1, RPMPeakHour: &one}}, "rpm_peak_desc") {
		t.Fatal("upstream RPM sort was ignored")
	}
	if _, err := parseAccessKeyCollectionQuery("sort=rpm_peak_desc", false); err != nil {
		t.Fatal(err)
	}
	if _, err := parseModernCredentialQuery("sort=rpm_peak_desc"); err != nil {
		t.Fatal(err)
	}
}

func TestRPMPeakSortIsModernOnly(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	result := performGroupCollectionRequest(engine, "/api/access-keys?sort=rpm_peak_desc", "Bearer "+authTestKey)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("classic RPM sort status = %d, want 400: %s", result.Code, result.Body.String())
	}
	result = performGroupCollectionRequest(engine, "/api/modern/access-keys?sort=rpm_peak_desc", "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("modern RPM sort status = %d, want 200: %s", result.Code, result.Body.String())
	}
}

func TestRPMCredentialQueryUsesOwnershipAndAdminBoundary(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := models.Group{Name: "rpm-group", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`)}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{GroupID: group.ID, Data: "encrypted-test", Fingerprint: "rpm-test", Status: models.CredentialStatusDisabled}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Minute)
	fixture.service.now = func() time.Time { return now }
	if err := fixture.db.Create(&models.RPMStat{Kind: "credential", KeyID: credential.ID, MinuteMS: now.Add(-time.Minute).UnixMilli(), SessionID: "test", Requests: 7, Peak: 9}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.usageStats = requestlog.NewService(fixture.db, redact.New(), rpmTestRetention{})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/modern/groups/%d/credentials/%d/rpm", group.ID, credential.ID)
	result := performGroupCollectionRequest(engine, path, "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("RPM query: %d %s", result.Code, result.Body.String())
	}
	var report requestlog.RPMReport
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &report); err != nil {
		t.Fatal(err)
	}
	if report.Peak == nil || *report.Peak != 9 || report.Requests != 7 {
		t.Fatalf("report: %+v", report)
	}
	if got := performGroupCollectionRequest(engine, path, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", got.Code)
	}
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "rpm-reader"})
	if err != nil {
		t.Fatal(err)
	}
	if got := performGroupCollectionRequest(engine, path, "Bearer "+accessKey.Key); got.Code != http.StatusForbidden {
		t.Fatalf("access key could read upstream statistics: %d", got.Code)
	}
	accessPath := fmt.Sprintf("/api/access-keys/%d/rpm", accessKey.ID)
	if got := performGroupCollectionRequest(engine, accessPath, "Bearer "+accessKey.Key); got.Code != http.StatusForbidden {
		t.Fatalf("new endpoint bypassed access-key whitelist: %d", got.Code)
	}
	if got := performGroupCollectionRequest(engine, accessPath, "Bearer "+authTestKey); got.Code != http.StatusOK {
		t.Fatalf("admin could not read access-key statistics: %d %s", got.Code, got.Body.String())
	}
	wrong := fmt.Sprintf("/api/modern/groups/%d/credentials/%d/rpm", group.ID+1, credential.ID)
	if got := performGroupCollectionRequest(engine, wrong, "Bearer "+authTestKey); got.Code != http.StatusNotFound {
		t.Fatalf("cross-group request: %d %s", got.Code, got.Body.String())
	}
	if got := performGroupCollectionRequest(engine, path+"?range=7d", "Bearer "+authTestKey); got.Code != http.StatusBadRequest {
		t.Fatalf("unsupported range: %d", got.Code)
	}
}

type rpmTestRetention struct{}

func (rpmTestRetention) RequestLogRetentionDays() int { return 7 }
