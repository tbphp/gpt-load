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
	"gpt-load/internal/telemetry"
)

func TestServicePersistsRecordedRequestBodies(t *testing.T) {
	db, _ := openRequestLogFileDB(t)
	events := persistRecordedRequestBodies(t, db, "00000000-0000-4000-8000-0000000006", time.Time{})

	newRequestLogTestService(db).Sweep(t.Context(), events[0].CompletedAt.Add(8*24*time.Hour))
	var remaining int64
	if err := db.Model(&models.RequestLogBody{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("request bodies after retention = %d, error = %v", remaining, err)
	}
}

// TestExternalDatabaseRequestBodyRecording 在真实 MySQL 与 PostgreSQL 上覆盖正文写入、
// 大正文截断后的落库和随请求日志的级联删除。
func TestExternalDatabaseRequestBodyRecording(t *testing.T) {
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
	unique := uint64(time.Now().UnixNano()) & 0xffffffffff
	// 外部库被所有合同测试共享，用独占的历史小时写入，避免用量聚合计入其他测试的查询窗口。
	completedAt := time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(unique&0xffff) * time.Hour)
	events := persistRecordedRequestBodies(t, db, fmt.Sprintf("00000000-0000-4000-8000-%010x", unique), completedAt)

	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.RequestID)
	}
	if err := db.Where("id IN ?", ids).Delete(&models.RequestLog{}).Error; err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := db.Model(&models.RequestLogBody{}).Where("request_id IN ?", ids).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("request bodies after deleting logs = %d, error = %v", remaining, err)
	}
}

// persistRecordedRequestBodies 写入原样、截断、非 JSON 和未开启记录四类请求，并逐条核对读取结果；
// completedAt 为零值时沿用 testEvent 的完成时间。
func persistRecordedRequestBodies(t *testing.T, db *gorm.DB, idPrefix string, completedAt time.Time) []telemetry.RequestEvent {
	t.Helper()
	recorded := testEvent(idPrefix + "49")
	recorded.RequestBody = []byte(`{"model":"client-model","messages":[{"role":"user","content":"hi"}]}`)
	large := testEvent(idPrefix + "50")
	large.RequestBody = []byte(`{"model":"client-model","input":"` + strings.Repeat("界", 120<<10) + `"}`)
	multipart := testEvent(idPrefix + "51")
	multipart.RequestBody = []byte("--boundary\r\nContent-Disposition: form-data; name=\"image\"\r\n\r\nabc")
	plain := testEvent(idPrefix + "52")
	events := []telemetry.RequestEvent{recorded, large, multipart, plain}
	if !completedAt.IsZero() {
		for index := range events {
			events[index].CompletedAt = completedAt
		}
		recorded, large, multipart, plain = events[0], events[1], events[2], events[3]
	}

	service := newRequestLogTestService(db)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		service.Emit(event)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := service.pendingRequestBodyBytes.Load(); pending != 0 {
		t.Fatalf("pending request body bytes = %d after shutdown", pending)
	}

	body, err := service.GetRequestBody(t.Context(), recorded.RequestID)
	if err != nil || body == nil || body.State != RequestBodyOriginal || body.Body == nil ||
		*body.Body != string(recorded.RequestBody) || body.OriginalBytes != int64(len(recorded.RequestBody)) {
		t.Fatalf("recorded body = %#v, error = %v", body, err)
	}
	body, err = service.GetRequestBody(t.Context(), large.RequestID)
	if err != nil || body == nil || body.State != RequestBodyTruncated || body.Body == nil ||
		len(*body.Body) > maxRecordedRequestBodyBytes || body.OriginalBytes != int64(len(large.RequestBody)) {
		t.Fatalf("large body state = %#v, error = %v", body, err)
	}
	assertSameStructure(t, large.RequestBody, []byte(*body.Body))
	body, err = service.GetRequestBody(t.Context(), multipart.RequestID)
	if err != nil || body == nil || body.State != RequestBodyNonJSON || body.Body != nil ||
		body.OriginalBytes != int64(len(multipart.RequestBody)) {
		t.Fatalf("multipart body = %#v, error = %v", body, err)
	}
	body, err = service.GetRequestBody(t.Context(), plain.RequestID)
	if err != nil || body != nil {
		t.Fatalf("request without recording = %#v, error = %v", body, err)
	}
	return events
}

func TestServiceSkipsRequestBodiesBeyondPendingBudget(t *testing.T) {
	written := make(chan []models.RequestLog, 1)
	timers := newManualTimerFactory()
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			written <- rows
			return nil
		}),
		redact.New(),
		timers.New,
	)
	service.requestBodyBudget = 10
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	first := testEvent("00000000-0000-4000-8000-000000000652")
	first.RequestBody = []byte(`{"a":1}`)
	second := testEvent("00000000-0000-4000-8000-000000000653")
	second.RequestBody = []byte(`{"b":2}`)
	service.Emit(first)
	service.Emit(second)
	if pending := service.pendingRequestBodyBytes.Load(); pending != int64(len(first.RequestBody)) {
		t.Fatalf("pending request body bytes = %d", pending)
	}
	receiveValue(t, timers.created).Fire()
	rows := receiveValue(t, written)
	if len(rows) != 2 {
		t.Fatalf("written rows = %d", len(rows))
	}
	if body := rows[0].RequestBodyRow; body == nil || body.State != string(RequestBodyOriginal) || body.Body == nil {
		t.Fatalf("first body = %#v", body)
	}
	if body := rows[1].RequestBodyRow; body == nil || body.State != string(RequestBodyQueueFull) || body.Body != nil ||
		body.OriginalBytes != int64(len(second.RequestBody)) {
		t.Fatalf("second body = %#v", body)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending := service.pendingRequestBodyBytes.Load(); pending != 0 {
		t.Fatalf("pending request body bytes = %d after write", pending)
	}
}
