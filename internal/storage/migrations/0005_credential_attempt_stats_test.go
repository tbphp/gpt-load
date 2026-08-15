package migrations_test

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestCredentialAttemptStatsMigrationCreatesSchemaAndBackfillsRecentAttempts(t *testing.T) {
	db := openInitialTestDatabase(t)
	applyCredentialAttemptStatsPrerequisites(t, db)

	now := time.Now().UTC().Truncate(time.Hour)
	firstHour := now.Add(-2 * time.Hour)
	secondHour := now.Add(-time.Hour)
	createAttemptMigrationRequest(t, db, "00000000-0000-4000-8000-000000000501", firstHour, []attemptMigrationInput{
		{credentialID: 11, failureCategory: telemetry.FailureCategoryOK},
		{credentialID: 11, failureCategory: telemetry.FailureCategoryRateLimited},
		{credentialID: 12, failureCategory: telemetry.FailureCategoryInvalidKey},
		{credentialID: 13, failureCategory: telemetry.FailureCategoryDownstreamCancel},
	})
	createAttemptMigrationRequest(t, db, "00000000-0000-4000-8000-000000000502", secondHour, []attemptMigrationInput{
		{credentialID: 11, failureCategory: telemetry.FailureCategoryOK},
	})
	createAttemptMigrationRequest(t, db, "00000000-0000-4000-8000-000000000503", now.Add(-26*time.Hour), []attemptMigrationInput{
		{credentialID: 14, failureCategory: telemetry.FailureCategoryOK},
	})

	if err := migrations.Up0005(db); err != nil {
		t.Fatalf("Up0005() error = %v", err)
	}
	if err := migrations.Validate0005(db); err != nil {
		t.Fatalf("Validate0005() error = %v", err)
	}

	assertColumns(t, db, "credential_attempt_stats", []string{
		"id", "credential_id", "bucket_start_ms", "success_count", "failure_count",
	})
	assertSQLiteIndex(t, db, "credential_attempt_stats", "idx_credential_attempt_stats_identity", true, []string{
		"credential_id", "bucket_start_ms",
	})
	assertSQLiteIndex(t, db, "credential_attempt_stats", "idx_credential_attempt_stats_bucket_id", false, []string{
		"bucket_start_ms", "id",
	})
	assertSQLiteIndex(t, db, "usage_stats", "idx_usage_stats_credential_bucket", false, []string{
		"credential_id", "bucket_start_ms",
	})

	type aggregate struct {
		CredentialID  uint
		BucketStartMS int64
		SuccessCount  int64
		FailureCount  int64
	}
	var got []aggregate
	if err := db.Table("credential_attempt_stats").
		Order("credential_id ASC").
		Order("bucket_start_ms ASC").
		Find(&got).Error; err != nil {
		t.Fatalf("query credential attempt stats: %v", err)
	}
	want := []aggregate{
		{CredentialID: 11, BucketStartMS: firstHour.UnixMilli(), SuccessCount: 1, FailureCount: 1},
		{CredentialID: 11, BucketStartMS: secondHour.UnixMilli(), SuccessCount: 1, FailureCount: 0},
		{CredentialID: 12, BucketStartMS: firstHour.UnixMilli(), SuccessCount: 0, FailureCount: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credential attempt stats = %#v, want %#v", got, want)
	}

	if err := migrations.Up0005(db); err != nil {
		t.Fatalf("repeat Up0005() error = %v", err)
	}
	var repeated []aggregate
	if err := db.Table("credential_attempt_stats").
		Order("credential_id ASC").
		Order("bucket_start_ms ASC").
		Find(&repeated).Error; err != nil {
		t.Fatalf("query repeated credential attempt stats: %v", err)
	}
	if !reflect.DeepEqual(repeated, want) {
		t.Fatalf("repeated credential attempt stats = %#v, want %#v", repeated, want)
	}
}

type attemptMigrationInput struct {
	credentialID    uint
	failureCategory telemetry.FailureCategory
}

func applyCredentialAttemptStatsPrerequisites(t *testing.T, db *gorm.DB) {
	t.Helper()
	for id, up := range []struct {
		id string
		up func(*gorm.DB) error
	}{
		{id: migrations.ID0001, up: migrations.Up0001},
		{id: migrations.ID0002, up: migrations.Up0002},
		{id: migrations.ID0003, up: migrations.Up0003},
		{id: migrations.ID0004, up: migrations.Up0004},
	} {
		if err := up.up(db); err != nil {
			t.Fatalf("apply prerequisite %d (%s): %v", id, up.id, err)
		}
	}
}

func createAttemptMigrationRequest(
	t *testing.T,
	db *gorm.DB,
	requestID string,
	completedAt time.Time,
	inputs []attemptMigrationInput,
) {
	t.Helper()
	request := models.RequestLog{
		ID: requestID, CompletedAtMS: completedAt.UnixMilli(), AccessKeyID: 1,
		GroupID: 1, ChannelID: "openai", CredentialID: inputs[len(inputs)-1].credentialID,
		Protocol: "openai-completions", ClientModel: "migration-model", UpstreamModel: "migration-model",
		ModelConsistency: string(telemetry.ModelConsistencyMatch), Status: string(telemetry.RequestStatusSuccess),
		StatusCode: 200, DurationMs: 1, AttemptCount: len(inputs),
		UsageState: string(usage.StateNotApplicable), CostState: string(pricing.CostStateNotApplicable),
		PricingCompleteness: string(pricing.CompletenessNotApplicable),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatalf("create request log %s: %v", requestID, err)
	}
	for index, input := range inputs {
		attempt := models.RequestLogAttempt{
			RequestID: requestID, Sequence: index + 1, CompletedAtMS: completedAt.UnixMilli(),
			GroupID: 1, GroupName: "migration-group", ChannelID: "openai", CredentialID: input.credentialID,
			StatusCode: 200, DurationMs: 1, FailureCategory: string(input.failureCategory),
			Action: string(telemetry.ActionTerminate), ErrorSummary: "",
		}
		if err := db.Create(&attempt).Error; err != nil {
			t.Fatalf("create request attempt %s/%d: %v", requestID, index+1, err)
		}
	}
}

func assertSQLiteIndex(
	t *testing.T,
	db *gorm.DB,
	table string,
	indexName string,
	wantUnique bool,
	wantColumns []string,
) {
	t.Helper()
	var indexes []struct {
		Name   string
		Unique int
	}
	if err := db.Raw("PRAGMA index_list('" + table + "')").Scan(&indexes).Error; err != nil {
		t.Fatalf("inspect %s indexes: %v", table, err)
	}
	for _, index := range indexes {
		if index.Name != indexName {
			continue
		}
		if (index.Unique == 1) != wantUnique {
			t.Fatalf("%s unique = %d, want %t", indexName, index.Unique, wantUnique)
		}
		var columns []struct {
			Name string
			Key  int
		}
		if err := db.Raw("PRAGMA index_xinfo('" + indexName + "')").Scan(&columns).Error; err != nil {
			t.Fatalf("inspect %s columns: %v", indexName, err)
		}
		gotColumns := make([]string, 0, len(wantColumns))
		for _, column := range columns {
			if column.Key == 1 {
				gotColumns = append(gotColumns, column.Name)
			}
		}
		if !reflect.DeepEqual(gotColumns, wantColumns) {
			t.Fatalf("%s columns = %v, want %v", indexName, gotColumns, wantColumns)
		}
		return
	}
	t.Fatalf("%s is missing", indexName)
}
