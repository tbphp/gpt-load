package requestlog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/execution"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

func TestQueryCredentialActivityCombinesHourlyStatsAndOldestPartialHour(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 15, 13, 45, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	from := now.Add(-24 * time.Hour)

	createCredentialAttemptStats(t, db,
		models.CredentialAttemptStat{
			CredentialID: 11, BucketStartMS: from.Truncate(time.Hour).UnixMilli(),
			SuccessCount: 100, FailureCount: 100,
		},
		models.CredentialAttemptStat{
			CredentialID: 11, BucketStartMS: from.Truncate(time.Hour).Add(time.Hour).UnixMilli(),
			SuccessCount: 2, FailureCount: 1,
		},
		models.CredentialAttemptStat{
			CredentialID: 11, BucketStartMS: now.Truncate(time.Hour).UnixMilli(),
			SuccessCount: 3, FailureCount: 4,
		},
		models.CredentialAttemptStat{
			CredentialID: 12, BucketStartMS: now.Truncate(time.Hour).UnixMilli(),
			SuccessCount: 7, FailureCount: 8,
		},
		models.CredentialAttemptStat{
			CredentialID: 13, BucketStartMS: now.Truncate(time.Hour).UnixMilli(),
			SuccessCount: 200, FailureCount: 300,
		},
	)

	createCredentialActivityAttempt(t, db, 1, 11, from.Add(5*time.Minute), telemetry.FailureCategoryOK)
	createCredentialActivityAttempt(t, db, 2, 11, from.Add(6*time.Minute), telemetry.FailureCategoryRateLimited)
	createCredentialActivityAttempt(t, db, 3, 11, from.Add(7*time.Minute), telemetry.FailureCategoryDownstreamCancel)
	createCredentialActivityAttempt(t, db, 4, 11, from.Add(-time.Minute), telemetry.FailureCategoryAmbiguous)
	latest11 := now.Add(-time.Minute)
	createCredentialActivityAttempt(t, db, 5, 11, latest11, telemetry.FailureCategoryOK)
	latest12 := now.Add(-2 * time.Minute)
	createCredentialActivityAttempt(t, db, 6, 12, latest12, telemetry.FailureCategoryOK)
	createCredentialActivityAttempt(t, db, 7, 13, now.Add(-time.Second), telemetry.FailureCategoryOK)
	createCredentialActivityAttemptWithDispatch(
		t, db, 8, 11, from.Add(8*time.Minute), telemetry.FailureCategoryOK, execution.DispatchLocal,
	)
	createCredentialActivityAttemptWithDispatch(
		t, db, 9, 11, now.Add(-time.Second), telemetry.FailureCategoryOK, execution.DispatchLocal,
	)

	queryCount := 0
	const callbackName = "test:credential_activity_query_count"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatalf("register query counter: %v", err)
	}
	if err := db.Callback().Row().After("gorm:row").Register(callbackName, func(*gorm.DB) {
		queryCount++
	}); err != nil {
		t.Fatalf("register row query counter: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Query().Remove(callbackName)
		_ = db.Callback().Row().Remove(callbackName)
	})

	result, err := service.QueryCredentialActivity(context.Background(), CredentialActivityQuery{
		CredentialIDs: []uint{11, 12},
		FromMS:        from.UnixMilli(),
		ToMS:          now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("QueryCredentialActivity() error = %v", err)
	}
	if queryCount != 3 {
		t.Fatalf("query count = %d, want one latest, one hourly, and one boundary query", queryCount)
	}
	if len(result) != 2 {
		t.Fatalf("result = %#v, want only requested credentials", result)
	}
	first := result[11]
	if first.CredentialID != 11 || first.SuccessCount != 6 || first.FailureCount != 6 ||
		!first.DataComplete || first.LastUsedAtMS == nil || *first.LastUsedAtMS != latest11.UnixMilli() {
		t.Fatalf("credential 11 activity = %#v", first)
	}
	second := result[12]
	if second.CredentialID != 12 || second.SuccessCount != 7 || second.FailureCount != 8 ||
		!second.DataComplete || second.LastUsedAtMS == nil || *second.LastUsedAtMS != latest12.UnixMilli() {
		t.Fatalf("credential 12 activity = %#v", second)
	}
}

func TestQueryCredentialActivityReturnsZeroActivityAndIncompleteRetention(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(db, nil, staticRetentionPolicy{days: 0})
	now := time.Date(2026, time.August, 15, 13, 45, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	result, err := service.QueryCredentialActivity(t.Context(), CredentialActivityQuery{
		CredentialIDs: []uint{21},
		FromMS:        now.Add(-24 * time.Hour).UnixMilli(),
		ToMS:          now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("QueryCredentialActivity() error = %v", err)
	}
	activity, exists := result[21]
	if !exists || activity.CredentialID != 21 || activity.SuccessCount != 0 ||
		activity.FailureCount != 0 || activity.LastUsedAtMS != nil || activity.DataComplete {
		t.Fatalf("empty activity = %#v, exists = %v", activity, exists)
	}
}

func TestQueryCredentialActivityRetainsLatestFailureAfterSuccess(t *testing.T) {
	assertCredentialLatestFailureSummary(t, openRequestLogQueryDB(t))
}

func assertCredentialLatestFailureSummary(t *testing.T, db *gorm.DB) {
	t.Helper()
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC)
	const message = "Selected model is at capacity. Please try a different model."
	for index, test := range []struct {
		credentialID uint
		age          time.Duration
		category     telemetry.FailureCategory
		dispatch     execution.DispatchState
		summary      string
	}{
		{91, 26 * time.Hour, telemetry.FailureCategoryUpstreamHost, execution.DispatchMaybeSent, "older failure"},
		{91, 25 * time.Hour, telemetry.FailureCategoryUpstreamHost, execution.DispatchMaybeSent, message},
		{91, time.Minute, telemetry.FailureCategoryOK, execution.DispatchMaybeSent, ""},
		{91, 50 * time.Second, telemetry.FailureCategoryDownstreamCancel, execution.DispatchMaybeSent, "client canceled"},
		{91, 40 * time.Second, telemetry.FailureCategoryClientError, execution.DispatchLocal, "local rejection"},
		{92, time.Minute, telemetry.FailureCategoryUpstreamHost, execution.DispatchMaybeSent, "other credential"},
		{91, -time.Minute, telemetry.FailureCategoryUpstreamHost, execution.DispatchMaybeSent, "future failure"},
		{93, time.Minute, telemetry.FailureCategoryOK, execution.DispatchMaybeSent, ""},
	} {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", 9000+index)
		at := now.Add(-test.age)
		row := requestLogQueryRow(id, at, 1, "latest-failure", []Attempt{{
			CredentialID: test.credentialID, FailureCategory: test.category,
			DispatchState: test.dispatch, ErrorSummary: test.summary,
		}})
		row.AttemptRows[0].CompletedAtMS = at.UnixMilli()
		createRequestLogQueryRow(t, db, row)
		t.Cleanup(func() {
			if err := db.Where("id = ?", id).Delete(&models.RequestLog{}).Error; err != nil {
				t.Errorf("delete diagnostic request: %v", err)
			}
		})
	}
	result, err := service.QueryCredentialActivity(t.Context(), CredentialActivityQuery{
		CredentialIDs: []uint{91, 92, 93, 94},
		FromMS:        now.Add(-24 * time.Hour).UnixMilli(), ToMS: now.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uint]string{91: message, 92: "other credential", 93: "", 94: ""} {
		if got := result[id].LastFailureSummary; got != want {
			t.Errorf("credential %d latest failure = %q, want %q", id, got, want)
		}
	}
}

func TestQueryCredentialActivityValidatesScope(t *testing.T) {
	service := newRequestLogTestService(openRequestLogQueryDB(t))
	valid := CredentialActivityQuery{CredentialIDs: []uint{1}, FromMS: 1, ToMS: 2}
	tooMany := make([]uint, 101)
	for index := range tooMany {
		tooMany[index] = uint(index + 1)
	}

	tests := map[string]CredentialActivityQuery{
		"empty IDs":        {FromMS: 1, ToMS: 2},
		"zero ID":          {CredentialIDs: []uint{0}, FromMS: 1, ToMS: 2},
		"too many IDs":     {CredentialIDs: tooMany, FromMS: 1, ToMS: 2},
		"negative start":   {CredentialIDs: []uint{1}, FromMS: -1, ToMS: 2},
		"empty time range": {CredentialIDs: []uint{1}, FromMS: 2, ToMS: 2},
		"excessive range":  {CredentialIDs: []uint{1}, FromMS: 1, ToMS: 1 + credentialActivityMaxWindowMS + 1},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.QueryCredentialActivity(t.Context(), input); err == nil {
				t.Fatalf("QueryCredentialActivity(%#v) error = nil", input)
			}
		})
	}
	if _, err := (*Service)(nil).QueryCredentialActivity(t.Context(), valid); err == nil {
		t.Fatal("nil service error = nil")
	}
}

func createCredentialAttemptStats(t *testing.T, db *gorm.DB, rows ...models.CredentialAttemptStat) {
	t.Helper()
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create CredentialAttemptStats: %v", err)
	}
}

func createCredentialActivityAttempt(
	t *testing.T,
	db *gorm.DB,
	sequence int,
	credentialID uint,
	completedAt time.Time,
	category telemetry.FailureCategory,
) {
	createCredentialActivityAttemptWithDispatch(t, db, sequence, credentialID, completedAt, category, "")
}

func createCredentialActivityAttemptWithDispatch(
	t *testing.T,
	db *gorm.DB,
	sequence int,
	credentialID uint,
	completedAt time.Time,
	category telemetry.FailureCategory,
	dispatch execution.DispatchState,
) {
	t.Helper()
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence)
	row := requestLogQueryRow(id, completedAt, 1, "activity", []Attempt{{
		CredentialID: credentialID, FailureCategory: category, DispatchState: dispatch,
	}})
	row.AttemptRows[0].CompletedAtMS = completedAt.UnixMilli()
	createRequestLogQueryRow(t, db, row)
}
