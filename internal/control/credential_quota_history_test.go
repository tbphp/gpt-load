package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
)

func TestCredentialQuotaHistoryScopesCurrentAccountAndTime(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := models.Group{Name: "quota history", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`)}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{GroupID: group.ID, Data: "encrypted-test", Fingerprint: "history-test", Status: models.CredentialStatusDisabled}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	identity := subscription.QuotaHistoryTargetIdentity(stateloader.CredentialIdentityGeneration(credential.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params)))
	seconds := int64(604_800)
	for _, row := range []models.CredentialQuotaHistory{
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 60_000, UsedBasisPoints: 1200},
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 120_000, UsedBasisPoints: 2000},
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: "old-account", WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 60_000, UsedBasisPoints: 9900},
	} {
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	url := fmt.Sprintf("/api/groups/%d/credentials/%d/quota-history?from_ms=0&to_ms=120000", group.ID, credential.ID)
	result := performGroupCollectionRequest(engine, url, "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", result.Code, result.Body.String())
	}
	var data struct {
		Windows []struct {
			Points []struct {
				UsedBasisPoints int64 `json:"used_basis_points"`
			} `json:"points"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 || len(data.Windows[0].Points) != 1 || data.Windows[0].Points[0].UsedBasisPoints != 1200 {
		t.Fatalf("history leaked another identity or boundary: %+v", data)
	}
	for _, auth := range []string{""} {
		if got := performGroupCollectionRequest(engine, url, auth); got.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status=%d", got.Code)
		}
	}
	for _, query := range []string{"from_ms=0&to_ms=1&credential_id=1", "from_ms=0&to_ms=0", "from_ms=0&to_ms=4000000000"} {
		got := performGroupCollectionRequest(engine, fmt.Sprintf("/api/groups/%d/credentials/%d/quota-history?%s", group.ID, credential.ID, query), "Bearer "+authTestKey)
		if got.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %s status=%d", query, got.Code)
		}
	}
	if err := fixture.db.Model(&group).Update("connection_type", models.ConnectionTypeAPIKey).Error; err != nil {
		t.Fatal(err)
	}
	if got := performGroupCollectionRequest(engine, url, "Bearer "+authTestKey); got.Code != http.StatusBadRequest {
		t.Fatalf("API key history status=%d", got.Code)
	}
}

func TestCredentialQuotaHistoryReturnsAllStoredPointsWithoutResampling(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := models.Group{Name: "bounded quota history", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`)}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{GroupID: group.ID, Data: "encrypted-test", Fingerprint: "bounded-history", Status: models.CredentialStatusDisabled}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	identity := subscription.QuotaHistoryTargetIdentity(stateloader.CredentialIdentityGeneration(credential.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params)))
	from := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	seconds := int64(604_800)
	rows := make([]models.CredentialQuotaHistory, 0, 120)
	for index := 0; index < 120; index++ {
		at := from + int64(index)*60_000
		reset := from + 18_000_000 + int64(index)*1000
		used := int64(4000)
		if index%60 == 20 {
			used = 9900
		}
		if index%60 == 40 {
			used = 100
		}
		rows = append(rows, models.CredentialQuotaHistory{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "session", WindowID: "primary", Label: "Session", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: at, UsedBasisPoints: used, ResetAtMS: &reset})
	}
	for _, minute := range []int64{59, 60, 240, 277, 314, 351} {
		rows = append(rows, models.CredentialQuotaHistory{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "sparse", WindowID: "secondary", Label: "Sparse", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: from + minute*60_000, UsedBasisPoints: minute})
	}
	if err := fixture.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	result := performGroupCollectionRequest(engine, fmt.Sprintf("/api/groups/%d/credentials/%d/quota-history?from_ms=%d&to_ms=%d", group.ID, credential.ID, from, from+7*24*3_600_000), "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var data struct {
		Windows []struct {
			Key    string `json:"key"`
			Points []struct {
				ObservedAtMS    int64 `json:"observed_at_ms"`
				UsedBasisPoints int64 `json:"used_basis_points"`
			} `json:"points"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 2 {
		t.Fatalf("unexpected history windows: %+v", data)
	}
	for _, window := range data.Windows {
		want := []int64{59, 60, 240, 277, 314, 351}
		if window.Key == "session" {
			want = make([]int64, 120)
			for index := range want {
				want[index] = int64(index)
			}
		}
		if len(window.Points) != len(want) {
			t.Fatalf("window %s: got %d points, want %d", window.Key, len(window.Points), len(want))
		}
		for index, point := range window.Points {
			if point.ObservedAtMS != from+want[index]*60_000 {
				t.Fatalf("window %s: point %d is not the expected real observation: %+v", window.Key, index, point)
			}
			if window.Key == "sparse" && point.UsedBasisPoints != want[index] {
				t.Fatalf("sparse observation value changed: %+v", point)
			}
		}
	}
}

func TestCredentialQuotaHistoryReturnsOnlyPointsWithinRange(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := models.Group{Name: "quota history range", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`)}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{GroupID: group.ID, Data: "encrypted-test", Fingerprint: "history-range", Status: models.CredentialStatusDisabled}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	identity := subscription.QuotaHistoryTargetIdentity(stateloader.CredentialIdentityGeneration(credential.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params)))
	seconds := int64(604_800)
	rows := []models.CredentialQuotaHistory{
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "primary", WindowID: "primary", Label: "Primary", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 1_000, UsedBasisPoints: 1000},
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "primary", WindowID: "primary", Label: "Primary", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 2_000, UsedBasisPoints: 2000},
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "secondary", WindowID: "secondary", Label: "Secondary", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 1_000, UsedBasisPoints: 3000},
		{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: "primary", WindowID: "primary", Label: "Primary", Scope: "account", WindowSeconds: &seconds, ObservedAtMS: 4_000, UsedBasisPoints: 4000},
	}
	if err := fixture.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	result := performGroupCollectionRequest(engine, fmt.Sprintf("/api/groups/%d/credentials/%d/quota-history?from_ms=3000&to_ms=5000", group.ID, credential.ID), "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var data struct {
		Windows []struct {
			Key    string `json:"key"`
			Points []struct {
				ObservedAtMS int64 `json:"observed_at_ms"`
			} `json:"points"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 || data.Windows[0].Key != "primary" ||
		len(data.Windows[0].Points) != 1 || data.Windows[0].Points[0].ObservedAtMS != 4_000 {
		t.Fatalf("unexpected out-of-range history: %+v", data)
	}
}

func TestCredentialQuotaHistoryFiltersStoredShortAndUnknownPeriods(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	group := models.Group{Name: "long quota history", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`)}
	if err := fixture.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential := models.Credential{GroupID: group.ID, Data: "encrypted-test", Fingerprint: "long-history", Status: models.CredentialStatusDisabled}
	if err := fixture.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	identity := subscription.QuotaHistoryTargetIdentity(stateloader.CredentialIdentityGeneration(credential.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params)))
	for _, seconds := range []int64{0, 18_000, 86_399, 86_400, 604_800} {
		row := models.CredentialQuotaHistory{GroupID: group.ID, CredentialID: credential.ID, TargetIdentity: identity, WindowKey: fmt.Sprint(seconds), WindowID: "window", Label: "Window", Scope: "account", ObservedAtMS: 60_000, UsedBasisPoints: 1200}
		if seconds != 0 {
			row.WindowSeconds = &seconds
		}
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	url := fmt.Sprintf("/api/groups/%d/credentials/%d/quota-history?from_ms=0&to_ms=120000", group.ID, credential.ID)
	result := performGroupCollectionRequest(engine, url, "Bearer "+authTestKey)
	if result.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
	}
	var data struct {
		Windows []struct {
			WindowSeconds int64 `json:"window_seconds"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(decodeGroupCollectionSuccessData(t, result), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 2 || data.Windows[0].WindowSeconds != 86_400 || data.Windows[1].WindowSeconds != 604_800 {
		t.Fatalf("old short or unknown periods are visible: %+v", data)
	}
}
