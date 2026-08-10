package requestlog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

// TestExternalDatabaseRequestLogLifecycle covers the request-log write,
// aggregate upsert, duplicate replay, and retention chain on real MySQL and
// PostgreSQL servers. Unit tests cover each branch; this keeps driver SQL and
// transaction differences inside the release contract.
func TestExternalDatabaseRequestLogLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	unique := uint64(time.Now().UnixNano()) & 0xffffffffffff
	requestID := fmt.Sprintf("00000000-0000-4000-8000-%012x", unique)
	groupID := uint(unique % 1_000_000)
	credentialID := groupID + 1
	modelID := fmt.Sprintf("external-request-log-%012x", unique)
	row := aggregationRow(requestID, now.Add(-40*24*time.Hour), groupID, modelID)
	row.ChannelID = "openai"
	row.CredentialID = credentialID
	writer := &gormBatchWriter{db: db}
	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err != nil {
		t.Fatalf("first WriteBatch() error = %v", err)
	}
	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err != nil {
		t.Fatalf("duplicate WriteBatch() error = %v", err)
	}
	assertExternalRequestLogCount(t, db, "id = ?", []any{requestID}, 1)
	// aggregationRow is deliberately placed on an exact hour, so its completed
	// timestamp is the usage aggregate bucket as well.
	assertExternalUsageStatCount(
		t,
		db,
		row.CompletedAtMS,
		row.AccessKeyID,
		row.ChannelID,
		groupID,
		credentialID,
		modelID,
		1,
	)

	service := NewService(db, redact.New(), staticRetentionPolicy{days: 1})
	service.Sweep(context.Background(), now)
	assertExternalRequestLogCount(t, db, "id = ?", []any{requestID}, 0)
	assertExternalUsageStatCount(
		t,
		db,
		row.CompletedAtMS,
		row.AccessKeyID,
		row.ChannelID,
		groupID,
		credentialID,
		modelID,
		0,
	)
	var journals int64
	if err := db.Model(&models.UsageAggregationJournal{}).
		Where("request_id = ?", requestID).
		Count(&journals).Error; err != nil {
		t.Fatalf("count usage aggregation journals: %v", err)
	}
	if journals != 0 {
		t.Fatalf("usage aggregation journals for %q = %d, want 0", requestID, journals)
	}
}

func assertExternalRequestLogCount(t *testing.T, db *gorm.DB, query string, args []any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(&models.RequestLog{}).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	if count != want {
		t.Fatalf("request log count = %d, want %d", count, want)
	}
}

func assertExternalUsageStatCount(
	t *testing.T,
	db *gorm.DB,
	bucketStartMS int64,
	accessKeyID uint,
	channelID string,
	groupID uint,
	credentialID uint,
	modelID string,
	want int64,
) {
	t.Helper()
	var count int64
	if err := db.Model(&models.UsageStat{}).
		Where(
			"bucket_start_ms = ? AND access_key_id = ? AND channel_id = ? AND group_id = ? AND credential_id = ? AND model = ?",
			bucketStartMS,
			accessKeyID,
			channelID,
			groupID,
			credentialID,
			modelID,
		).
		Count(&count).Error; err != nil {
		t.Fatalf("count usage stats: %v", err)
	}
	if count != want {
		t.Fatalf("usage stat count = %d, want %d", count, want)
	}
}
