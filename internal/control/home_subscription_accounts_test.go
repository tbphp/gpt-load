package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestReadHomeSubscriptionAccountsUsesBoundedHourlyActivityAndDeduplicates(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 31, 12, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }

	sharedOneGroupID, sharedOne := createHomeSubscriptionCredential(
		t, fixture, "shared-one", "shared-account", "shared@example.com",
	)
	sharedTwoGroupID, sharedTwo := createHomeSubscriptionCredential(
		t, fixture, "shared-two", "shared-account", "shared@example.com",
	)
	_, other := createHomeSubscriptionCredential(
		t, fixture, "other", "other-account", "other@example.com",
	)
	_, failureOnly := createHomeSubscriptionCredential(
		t, fixture, "failure-only", "failure-account", "failure@example.com",
	)
	_, outsideWindow := createHomeSubscriptionCredential(
		t, fixture, "outside-window", "old-account", "old@example.com",
	)

	if _, err := fixture.service.UpdateGroupCredential(
		t.Context(), sharedTwoGroupID, sharedTwo.ID,
		CredentialUpdateRequest{Status: optionalField[state.CredentialStatus]{
			Set: true, Value: state.CredentialStatusDisabled,
		}},
	); err != nil {
		t.Fatalf("disable duplicate subscription credential: %v", err)
	}

	createHomeCredentialObservation(t, fixture, sharedOne, now.Add(-10*time.Minute), "Old plan")
	createHomeCredentialObservation(t, fixture, sharedTwo, now.Add(-time.Minute), "Pro 20x")
	createHomeCredentialObservation(t, fixture, other, now.Add(-2*time.Minute), "Other plan")

	recentBucket := now.Truncate(time.Hour).Add(-time.Hour).UnixMilli()
	if err := fixture.db.Create(&[]models.CredentialAttemptStat{
		{CredentialID: sharedOne.ID, BucketStartMS: recentBucket, SuccessCount: 3},
		{CredentialID: sharedTwo.ID, BucketStartMS: recentBucket, SuccessCount: 4, FailureCount: 20},
		{CredentialID: other.ID, BucketStartMS: recentBucket, SuccessCount: 6},
		{CredentialID: failureOnly.ID, BucketStartMS: recentBucket, FailureCount: 100},
		{CredentialID: outsideWindow.ID, BucketStartMS: now.Truncate(time.Hour).Add(-25 * time.Hour).UnixMilli(), SuccessCount: 100},
	}).Error; err != nil {
		t.Fatalf("create credential activity: %v", err)
	}

	apiGroupID := createGroupWithCredentials(t, fixture, "sk-api-key")
	var apiCredential models.Credential
	if err := fixture.db.Where("group_id = ?", apiGroupID).Take(&apiCredential).Error; err != nil {
		t.Fatalf("query API-key credential: %v", err)
	}
	if err := fixture.db.Create(&models.CredentialAttemptStat{
		CredentialID: apiCredential.ID, BucketStartMS: recentBucket, SuccessCount: 200,
	}).Error; err != nil {
		t.Fatalf("create API-key activity: %v", err)
	}

	result, err := fixture.service.ReadHomeSubscriptionAccounts(t.Context())
	if err != nil {
		t.Fatalf("ReadHomeSubscriptionAccounts() error = %v", err)
	}
	if result.ObservedAtMS != now.UnixMilli() || len(result.Items) != 2 {
		t.Fatalf("ReadHomeSubscriptionAccounts() = %#v", result)
	}
	shared := result.Items[0]
	if shared.ChannelID != string(channel.Codex) || shared.GroupCount != 2 ||
		shared.AvailableGroupCount != 1 || shared.Credential.Account.Email != "shared@example.com" ||
		shared.Credential.Observation == nil || shared.Credential.Observation.Snapshot == nil ||
		shared.Credential.Observation.Snapshot.Plan.Name != "Pro 20x" {
		t.Fatalf("deduplicated shared account = %#v", shared)
	}
	if result.Items[1].Credential.Account.Email != "other@example.com" ||
		result.Items[1].GroupCount != 1 || result.Items[1].AvailableGroupCount != 1 {
		t.Fatalf("second account = %#v", result.Items[1])
	}
	if sharedOneGroupID == sharedTwoGroupID {
		t.Fatal("duplicate account fixture unexpectedly reused one group")
	}
}

func TestReadHomeSubscriptionAccountsReflectsLatestRankingOnEveryRead(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 31, 12, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	_, first := createHomeSubscriptionCredential(t, fixture, "cache-first", "cache-first", "first@example.com")
	_, second := createHomeSubscriptionCredential(t, fixture, "cache-second", "cache-second", "second@example.com")
	bucket := now.Truncate(time.Hour).UnixMilli()
	if err := fixture.db.Create(&[]models.CredentialAttemptStat{
		{CredentialID: first.ID, BucketStartMS: bucket, SuccessCount: 5},
		{CredentialID: second.ID, BucketStartMS: bucket, SuccessCount: 1},
	}).Error; err != nil {
		t.Fatal(err)
	}

	firstRead, err := fixture.service.ReadHomeSubscriptionAccounts(t.Context())
	if err != nil || len(firstRead.Items) != 2 || firstRead.Items[0].Credential.Account.Email != "first@example.com" {
		t.Fatalf("first read = %#v, %v", firstRead, err)
	}
	if err := fixture.db.Model(&models.CredentialAttemptStat{}).
		Where("credential_id = ? AND bucket_start_ms = ?", second.ID, bucket).
		Update("success_count", 50).Error; err != nil {
		t.Fatal(err)
	}

	latest, err := fixture.service.ReadHomeSubscriptionAccounts(t.Context())
	if err != nil || latest.Items[0].Credential.Account.Email != "second@example.com" {
		t.Fatalf("latest read = %#v, %v", latest, err)
	}
}

func TestReadHomeSubscriptionAccountsLimitsTheHomepageToFourCards(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 31, 12, 30, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	bucket := now.Truncate(time.Hour).UnixMilli()
	for index := 0; index < 5; index++ {
		_, credential := createHomeSubscriptionCredential(
			t,
			fixture,
			fmt.Sprintf("limit-%d", index),
			fmt.Sprintf("limit-account-%d", index),
			fmt.Sprintf("limit-%d@example.com", index),
		)
		if err := fixture.db.Create(&models.CredentialAttemptStat{
			CredentialID: credential.ID, BucketStartMS: bucket, SuccessCount: int64(10 - index),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	result, err := fixture.service.ReadHomeSubscriptionAccounts(t.Context())
	if err != nil || len(result.Items) != 4 {
		t.Fatalf("ReadHomeSubscriptionAccounts() = %#v, %v", result, err)
	}
	for index, item := range result.Items {
		want := fmt.Sprintf("limit-%d@example.com", index)
		if item.Credential.Account.Email != want {
			t.Fatalf("item %d email = %q, want %q", index, item.Credential.Account.Email, want)
		}
		if item.ChannelName != "Codex" {
			t.Fatalf("item %d channel name = %q, want Codex", index, item.ChannelName)
		}
	}
}

func TestHomeSubscriptionAccountsRouteIsAdminOnly(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	accessKey, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "home readonly"})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	admin := performHomeRequest(engine, "/api/home/subscription-accounts", authTestKey)
	if admin.Code != http.StatusOK {
		t.Fatalf("admin response = %d %s", admin.Code, admin.Body.String())
	}
	unknownQuery := performHomeRequest(
		engine,
		"/api/home/subscription-accounts?unknown=1",
		authTestKey,
	)
	if unknownQuery.Code != http.StatusBadRequest {
		t.Fatalf("unknown query response = %d %s", unknownQuery.Code, unknownQuery.Body.String())
	}
	access := performHomeRequest(engine, "/api/home/subscription-accounts", accessKey.Key)
	if access.Code != http.StatusForbidden {
		t.Fatalf("AccessKey response = %d %s, want 403", access.Code, access.Body.String())
	}
}

func TestHomeSubscriptionActivityScopeUsesOnlyHourlyAccountAggregatesAndQuotesGroups(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN:                       "user:password@tcp(127.0.0.1:3306)/gpt_load",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}

	result := homeSubscriptionActivityScope(db, 1, 2).Find(&[]homeSubscriptionActivityRow{})
	if result.Error != nil {
		t.Fatalf("home subscription activity query error = %v", result.Error)
	}
	sql := result.Statement.SQL.String()
	if strings.Contains(sql, "JOIN groups") || !strings.Contains(sql, "FROM `groups`") {
		t.Fatalf("generated SQL = %q, want a quoted groups subquery", sql)
	}
	if !strings.Contains(sql, "credential_attempt_stats") || strings.Contains(sql, "request_log") {
		t.Fatalf("generated SQL = %q, want hourly account aggregates only", sql)
	}
}

func createHomeSubscriptionCredential(
	t *testing.T,
	fixture serviceFixture,
	groupSuffix string,
	accountID string,
	email string,
) (uint, models.Credential) {
	t.Helper()
	stage := mustImportSubscriptionStage(t, fixture, accountID, email)
	name := fmt.Sprintf("home-subscription-%s", groupSuffix)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer(name), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.3-codex"}}},
		StagedCredentialIDs: []string{stage.StageID},
		ConfirmSameTarget:   true,
	})
	if err != nil {
		t.Fatalf("create subscription group %q: %v", name, err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatalf("query subscription credential: %v", err)
	}
	return created.GroupID, credential
}

func createHomeCredentialObservation(
	t *testing.T,
	fixture serviceFixture,
	credential models.Credential,
	observedAt time.Time,
	plan string,
) {
	t.Helper()
	remaining := 0.75
	resetAtMS := observedAt.Add(5 * time.Hour).UnixMilli()
	snapshot, err := json.Marshal(CredentialObservationSnapshot{
		Plan: ObservationPlanSummary{Name: plan, Level: "premium"},
		QuotaWindows: []ObservationQuotaWindow{{
			ID: "five-hour", Label: "5h", Scope: "account", Unit: "ratio",
			Remaining: &remaining, ResetAtMS: &resetAtMS, State: "available",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observedAtMS := observedAt.UnixMilli()
	row := models.CredentialObservation{
		CredentialID: credential.ID, IdentityFingerprint: credential.IdentityFingerprint,
		SchemaVersion: 1, ObservationVersion: 1, SnapshotJSON: snapshot,
		State: models.CredentialObservationFresh, ObservedAtMS: &observedAtMS,
		LastAttemptAtMS: &observedAtMS, UpdatedAtMS: observedAtMS,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create credential observation: %v", err)
	}
}
