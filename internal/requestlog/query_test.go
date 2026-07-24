package requestlog

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

func TestServiceListUsesStableKeysetCursor(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(db, redact.New())
	completedAt := time.Date(2026, time.July, 24, 12, 0, 0, 123, time.UTC)
	older := completedAt.Add(-time.Nanosecond)
	for _, row := range []models.RequestLog{
		requestLogQueryRow("00000000-0000-4000-8000-000000000100", older, 41, "older", nil),
		requestLogQueryRow("00000000-0000-4000-8000-000000000101", completedAt, 41, "same-time", nil),
		requestLogQueryRow("00000000-0000-4000-8000-000000000102", completedAt, 41, "same-time", nil),
		requestLogQueryRow("00000000-0000-4000-8000-000000000103", completedAt, 41, "same-time", nil),
	} {
		createRequestLogQueryRow(t, db, row)
	}

	first, err := service.List(context.Background(), ListQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	if got, want := requestIDs(first.Items), []string{
		"00000000-0000-4000-8000-000000000103",
		"00000000-0000-4000-8000-000000000102",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page IDs = %v, want %v", got, want)
	}
	if first.NextCursor == nil ||
		!first.NextCursor.CompletedAt.Equal(completedAt) ||
		first.NextCursor.RequestID != "00000000-0000-4000-8000-000000000102" {
		t.Fatalf("first NextCursor = %#v", first.NextCursor)
	}

	second, err := service.List(context.Background(), ListQuery{
		Limit:  2,
		Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if got, want := requestIDs(second.Items), []string{
		"00000000-0000-4000-8000-000000000101",
		"00000000-0000-4000-8000-000000000100",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page IDs = %v, want %v", got, want)
	}
	if second.Items[0].CompletedAt.Equal(second.Items[1].CompletedAt) {
		t.Fatalf("second page did not advance from tied timestamp: %+v", second.Items)
	}
	if second.NextCursor != nil {
		t.Fatalf("second NextCursor = %#v, want nil", second.NextCursor)
	}
}

func TestServiceListAppliesAllFiltersAndGroupJSON(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(db, redact.New())
	from := time.Date(2026, time.July, 24, 11, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	targetID := "00000000-0000-4000-8000-000000000201"
	integerAttempts := []Attempt{
		{Sequence: 1, GroupID: 12, GroupName: "retry-first", WillRetry: true},
		{Sequence: 2, GroupID: 13, GroupName: "retry-second"},
	}
	target := requestLogQueryRow(targetID, from, 71, "client-model", integerAttempts)
	target.UpstreamModel = "different-upstream-model"
	target.Status = string(telemetry.RequestStatusError)
	createRequestLogQueryRow(t, db, target)

	atTo := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000202",
		to,
		71,
		"client-model",
		integerAttempts,
	)
	atTo.Status = string(telemetry.RequestStatusError)
	createRequestLogQueryRow(t, db, atTo)

	beforeFrom := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000203",
		from.Add(-time.Nanosecond),
		71,
		"client-model",
		integerAttempts,
	)
	beforeFrom.Status = string(telemetry.RequestStatusError)
	createRequestLogQueryRow(t, db, beforeFrom)

	stringGroup := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000204",
		from.Add(time.Minute),
		71,
		"client-model",
		nil,
	)
	stringGroup.Status = string(telemetry.RequestStatusError)
	stringGroup.Attempts = models.JSON(`[{"group_id":"12"}]`)
	createRequestLogQueryRow(t, db, stringGroup)

	realGroup := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000205",
		from.Add(2*time.Minute),
		71,
		"client-model",
		nil,
	)
	realGroup.Status = string(telemetry.RequestStatusError)
	realGroup.Attempts = models.JSON(`[{"group_id":12.0}]`)
	createRequestLogQueryRow(t, db, realGroup)

	upstreamOnly := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000206",
		from.Add(3*time.Minute),
		71,
		"wrong-client-model",
		integerAttempts,
	)
	upstreamOnly.UpstreamModel = "client-model"
	upstreamOnly.Status = string(telemetry.RequestStatusError)
	createRequestLogQueryRow(t, db, upstreamOnly)

	wrongAccessKey := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000208",
		from.Add(5*time.Minute),
		72,
		"client-model",
		integerAttempts,
	)
	wrongAccessKey.Status = string(telemetry.RequestStatusError)
	createRequestLogQueryRow(t, db, wrongAccessKey)

	wrongStatus := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000209",
		from.Add(6*time.Minute),
		71,
		"client-model",
		integerAttempts,
	)
	createRequestLogQueryRow(t, db, wrongStatus)

	zeroAttempts := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000207",
		from.Add(4*time.Minute),
		71,
		"client-model",
		nil,
	)
	zeroAttempts.Status = string(telemetry.RequestStatusError)
	zeroAttempts.Attempts = nil
	createRequestLogQueryRow(t, db, zeroAttempts)

	groupID := uint(12)
	accessKeyID := uint(71)
	page, err := service.List(context.Background(), ListQuery{
		From:        &from,
		To:          &to,
		GroupID:     &groupID,
		ClientModel: "client-model",
		AccessKeyID: &accessKeyID,
		Status:      telemetry.RequestStatusError,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("filtered List() error = %v", err)
	}
	if got, want := requestIDs(page.Items), []string{targetID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered IDs = %v, want %v", got, want)
	}

	requestPage, err := service.List(context.Background(), ListQuery{
		RequestID: targetID,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("request ID List() error = %v", err)
	}
	if got, want := requestIDs(requestPage.Items), []string{targetID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request ID filter = %v, want %v", got, want)
	}

	conflictingAccessKeyID := uint(72)
	conflictingPage, err := service.List(context.Background(), ListQuery{
		RequestID:   targetID,
		AccessKeyID: &conflictingAccessKeyID,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("conflicting request ID List() error = %v", err)
	}
	if len(conflictingPage.Items) != 0 {
		t.Fatalf("request ID with conflicting AccessKey filter = %#v, want empty", conflictingPage.Items)
	}
}

func TestServiceListBatchLoadsCurrentAccessKeyNames(t *testing.T) {
	db := openRequestLogQueryDB(t)
	current := models.AccessKey{
		Name: "before-rename", KeyValue: "cipher-current", KeyHash: "hash-current",
		Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create current AccessKey: %v", err)
	}
	deleted := models.AccessKey{
		Name: "deleted", KeyValue: "cipher-deleted", KeyHash: "hash-deleted",
		Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("create deleted AccessKey: %v", err)
	}

	base := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	createRequestLogQueryRow(t, db, requestLogQueryRow(
		"00000000-0000-4000-8000-000000000301", base, current.ID, "one", nil,
	))
	createRequestLogQueryRow(t, db, requestLogQueryRow(
		"00000000-0000-4000-8000-000000000302", base.Add(time.Second), current.ID, "two", nil,
	))
	createRequestLogQueryRow(t, db, requestLogQueryRow(
		"00000000-0000-4000-8000-000000000303", base.Add(2*time.Second), deleted.ID, "three", nil,
	))
	if err := db.Model(&current).Update("name", "after-rename").Error; err != nil {
		t.Fatalf("rename AccessKey: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("delete AccessKey: %v", err)
	}

	accessKeyQueries := 0
	const callbackName = "test:request_log_access_key_query_count"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "access_keys" {
			accessKeyQueries++
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	page, err := NewService(db, redact.New()).List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if accessKeyQueries != 1 {
		t.Fatalf("AccessKey query count = %d, want one batch query", accessKeyQueries)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %#v, want three", page.Items)
	}
	byID := make(map[string]Record, len(page.Items))
	for _, item := range page.Items {
		byID[item.RequestID] = item
	}
	for _, id := range []string{
		"00000000-0000-4000-8000-000000000301",
		"00000000-0000-4000-8000-000000000302",
	} {
		item := byID[id]
		if item.AccessKey.ID != current.ID || item.AccessKey.Name == nil ||
			*item.AccessKey.Name != "after-rename" || item.AccessKey.Deleted {
			t.Fatalf("current AccessKey ref for %s = %#v", id, item.AccessKey)
		}
	}
	deletedRef := byID["00000000-0000-4000-8000-000000000303"].AccessKey
	if deletedRef.ID != deleted.ID || deletedRef.Name != nil || !deletedRef.Deleted {
		t.Fatalf("deleted AccessKey ref = %#v", deletedRef)
	}
}

func TestServiceListNormalizesNullAttemptsToEmptyArray(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000401",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		91,
		"zero-attempt",
		nil,
	)
	row.Attempts = nil
	createRequestLogQueryRow(t, db, row)

	page, err := NewService(db, redact.New()).List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Attempts == nil ||
		len(page.Items[0].Attempts) != 0 {
		t.Fatalf("Attempts = %#v, want non-nil empty slice", page.Items)
	}
	encoded, err := json.Marshal(page.Items[0])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(encoded) == "" || !containsJSONFragment(encoded, `"Attempts":[]`) {
		t.Fatalf("encoded Record = %s, want empty attempts array", encoded)
	}
}

func openRequestLogQueryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close request log query database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("storage.AutoMigrate() error = %v", err)
	}
	return db
}

func requestLogQueryRow(
	id string,
	completedAt time.Time,
	accessKeyID uint,
	clientModel string,
	attempts []Attempt,
) models.RequestLog {
	encodedAttempts, err := json.Marshal(attempts)
	if err != nil {
		panic(err)
	}
	return models.RequestLog{
		ID:            id,
		CreatedAt:     completedAt,
		AccessKeyID:   accessKeyID,
		Protocol:      string(protocol.OpenAI),
		ClientModel:   clientModel,
		UpstreamModel: "upstream-" + clientModel,
		Status:        string(telemetry.RequestStatusSuccess),
		StatusCode:    200,
		DurationMs:    25,
		Attempts:      models.JSON(encodedAttempts),
	}
}

func createRequestLogQueryRow(t *testing.T, db *gorm.DB, row models.RequestLog) {
	t.Helper()
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create RequestLog %s: %v", row.ID, err)
	}
}

func requestIDs(records []Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.RequestID)
	}
	return ids
}

func containsJSONFragment(encoded []byte, fragment string) bool {
	for index := 0; index+len(fragment) <= len(encoded); index++ {
		if string(encoded[index:index+len(fragment)]) == fragment {
			return true
		}
	}
	return false
}
