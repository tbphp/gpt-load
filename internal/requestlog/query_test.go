package requestlog

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestDecodeRequestLogRowsPreservesUsageCostAttribution(t *testing.T) {
	rows := []models.RequestLog{{
		ID:                   "00000000-0000-4000-8000-000000000601",
		CompletedAtMS:        1_785_085_323_000,
		AccessKeyID:          61,
		GroupID:              17,
		Protocol:             string(protocol.OpenAICompletions),
		ClientModel:          "client-model",
		UpstreamModel:        "upstream-model",
		Status:               string(telemetry.RequestStatusSuccess),
		StatusCode:           200,
		DurationMs:           25,
		UncachedInputTokens:  11,
		CacheReadTokens:      12,
		CacheWrite5MTokens:   13,
		CacheWrite1HTokens:   14,
		OutputTokens:         15,
		EstimatedCostNanoUSD: 123_456_789,
		UsageState:           string(usage.StateComplete),
		CostState:            string(pricing.CostStatePriced),
		PricingCompleteness:  string(pricing.CompletenessComplete),
		Attempts:             models.JSON(`[]`),
	}}

	records, err := decodeRequestLogRows(rows)
	if err != nil {
		t.Fatalf("decodeRequestLogRows() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one record", records)
	}
	got := records[0]
	if got.GroupID != 17 || got.UsageState != usage.StateComplete ||
		got.CostState != pricing.CostStatePriced || got.UncachedInputTokens != 11 ||
		got.CacheReadTokens != 12 || got.CacheWrite5MTokens != 13 ||
		got.CacheWrite1HTokens != 14 || got.OutputTokens != 15 ||
		got.EstimatedCostNanoUSD != 123_456_789 ||
		got.CompletedAtMS != 1_785_085_323_000 {
		t.Fatalf("decoded usage/cost record = %#v", got)
	}
}

func TestDecodeRequestLogRowsIgnoresHistoricalKeyMask(t *testing.T) {
	rows := []models.RequestLog{{
		ID:                  "00000000-0000-4000-8000-000000000605",
		CompletedAtMS:       1_785_110_400_000,
		Protocol:            string(protocol.OpenAICompletions),
		Status:              string(telemetry.RequestStatusSuccess),
		UsageState:          string(usage.StateNotApplicable),
		CostState:           string(pricing.CostStateNotApplicable),
		PricingCompleteness: string(pricing.CompletenessNotApplicable),
		Attempts: models.JSON(
			`[{"sequence":1,"group_id":7,"group_name":"Primary","key_id":11,"key_mask":"prov****safe","upstream_model":"model","status_code":200,"duration_ms":10,"failure_category":"ok","action":"terminate","will_retry":false,"error_code":"","error_summary":"","committed":true}]`,
		),
	}}

	records, err := decodeRequestLogRows(rows)
	if err != nil {
		t.Fatalf("decodeRequestLogRows() error = %v", err)
	}
	if len(records) != 1 || len(records[0].Attempts) != 1 {
		t.Fatalf("records = %#v, want one historical attempt", records)
	}
	encoded, err := json.Marshal(records[0].Attempts[0])
	if err != nil {
		t.Fatalf("marshal decoded attempt: %v", err)
	}
	for _, forbidden := range [][]byte{[]byte(`"key_mask"`), []byte("prov"), []byte("safe")} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("decoded attempt retains historical key material %q: %s", forbidden, encoded)
		}
	}
}

func TestDecodeRequestLogRowsRejectsInvalidUsageCostValues(t *testing.T) {
	base := models.RequestLog{
		ID:                  "00000000-0000-4000-8000-000000000602",
		CompletedAtMS:       1_785_110_400_000,
		Protocol:            string(protocol.OpenAICompletions),
		Status:              string(telemetry.RequestStatusSuccess),
		UsageState:          string(usage.StateComplete),
		CostState:           string(pricing.CostStatePriced),
		PricingCompleteness: string(pricing.CompletenessComplete),
		Attempts:            models.JSON(`[]`),
	}
	tests := []struct {
		name   string
		mutate func(*models.RequestLog)
	}{
		{name: "usage state", mutate: func(row *models.RequestLog) { row.UsageState = "invalid" }},
		{name: "cost state", mutate: func(row *models.RequestLog) { row.CostState = "invalid" }},
		{name: "negative token", mutate: func(row *models.RequestLog) { row.UncachedInputTokens = -1 }},
		{name: "negative cost", mutate: func(row *models.RequestLog) { row.EstimatedCostNanoUSD = -1 }},
		{
			name: "missing usage cannot be priced",
			mutate: func(row *models.RequestLog) {
				row.UsageState = string(usage.StateMissing)
				row.CostState = string(pricing.CostStatePriced)
			},
		},
		{
			name: "not applicable usage cannot be unpriced",
			mutate: func(row *models.RequestLog) {
				row.UsageState = string(usage.StateNotApplicable)
				row.CostState = string(pricing.CostStateUnpriced)
			},
		},
		{
			name: "complete usage cannot have not applicable cost",
			mutate: func(row *models.RequestLog) {
				row.CostState = string(pricing.CostStateNotApplicable)
			},
		},
		{
			name: "unpriced cost must be zero",
			mutate: func(row *models.RequestLog) {
				row.CostState = string(pricing.CostStateUnpriced)
				row.EstimatedCostNanoUSD = 1
			},
		},
		{
			name: "not applicable cost must be zero",
			mutate: func(row *models.RequestLog) {
				row.UsageState = string(usage.StateNotApplicable)
				row.CostState = string(pricing.CostStateNotApplicable)
				row.EstimatedCostNanoUSD = 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := base
			test.mutate(&row)
			if _, err := decodeRequestLogRows([]models.RequestLog{row}); err == nil {
				t.Fatal("decodeRequestLogRows() error = nil, want invalid row rejection")
			}
		})
	}
}

func TestDecodeRequestLogRowsAcceptsApprovedUsageCostMatrix(t *testing.T) {
	tests := []struct {
		name         string
		usageState   usage.State
		costState    pricing.CostState
		completeness pricing.Completeness
		cost         int64
	}{
		{name: "complete priced", usageState: usage.StateComplete, costState: pricing.CostStatePriced, completeness: pricing.CompletenessComplete, cost: 10_000_000},
		{name: "complete unpriced", usageState: usage.StateComplete, costState: pricing.CostStateUnpriced, completeness: pricing.CompletenessUnavailable},
		{name: "partial priced", usageState: usage.StatePartial, costState: pricing.CostStatePriced, completeness: pricing.CompletenessPartial, cost: 10_000_000},
		{name: "partial unpriced", usageState: usage.StatePartial, costState: pricing.CostStateUnpriced, completeness: pricing.CompletenessUnavailable},
		{name: "missing unpriced", usageState: usage.StateMissing, costState: pricing.CostStateUnpriced, completeness: pricing.CompletenessUnavailable},
		{
			name:         "not applicable",
			usageState:   usage.StateNotApplicable,
			costState:    pricing.CostStateNotApplicable,
			completeness: pricing.CompletenessNotApplicable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := models.RequestLog{
				ID:                   "00000000-0000-4000-8000-000000000604",
				CompletedAtMS:        1_785_110_400_000,
				Protocol:             string(protocol.OpenAICompletions),
				Status:               string(telemetry.RequestStatusSuccess),
				UsageState:           string(test.usageState),
				CostState:            string(test.costState),
				PricingCompleteness:  string(test.completeness),
				EstimatedCostNanoUSD: test.cost,
				Attempts:             models.JSON(`[]`),
			}
			records, err := decodeRequestLogRows([]models.RequestLog{row})
			if err != nil {
				t.Fatalf("decodeRequestLogRows() error = %v", err)
			}
			if len(records) != 1 || records[0].UsageState != test.usageState ||
				records[0].CostState != test.costState ||
				records[0].EstimatedCostNanoUSD != test.cost {
				t.Fatalf("records = %#v", records)
			}
		})
	}
}

func TestServiceListUsesStableKeysetCursor(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	completedAt := time.Date(2026, time.July, 24, 12, 0, 0, 123, time.UTC)
	older := completedAt.Add(-time.Millisecond)
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
		first.NextCursor.CompletedAtMS != 1_784_894_400_000 ||
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
	if second.Items[0].CompletedAtMS == second.Items[1].CompletedAtMS {
		t.Fatalf("second page did not advance from tied timestamp: %+v", second.Items)
	}
	if second.NextCursor != nil {
		t.Fatalf("second NextCursor = %#v, want nil", second.NextCursor)
	}
}

func TestServiceListAppliesAllFiltersAndGroupJSON(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
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
		from.Add(-time.Millisecond),
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
	fromMS := from.UnixMilli()
	toMS := to.UnixMilli()
	page, err := service.List(context.Background(), ListQuery{
		FromMS:        &fromMS,
		ToMS:          &toMS,
		GroupID:       &groupID,
		ClientModel:   "client-model",
		UpstreamModel: "different-upstream-model",
		AccessKeyID:   &accessKeyID,
		Status:        telemetry.RequestStatusError,
		Limit:         50,
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

func TestServiceListGroupFilterUsesAnyAttemptWhileAttributionUsesFinalGroup(t *testing.T) {
	db := openRequestLogQueryDB(t)
	event := testEvent("00000000-0000-4000-8000-000000000210")
	event.Attempts = []telemetry.Attempt{
		{Sequence: 1, GroupID: 12, KeyID: 1, UpstreamModel: "retry-model", WillRetry: true},
		{Sequence: 2, GroupID: 13, KeyID: 2, UpstreamModel: "final-model"},
	}
	event.UpstreamModel = "final-model"
	event.Usage = telemetry.UsageObservation{
		GroupID: 13, KeyID: 2, AttemptSequence: 2,
		Result: usage.Result{
			State: usage.StateNotApplicable,
		},
		Pricing: telemetry.PricingObservation{
			PriceScopeKey: "group:13", UpstreamModel: "final-model",
			CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable),
		},
	}
	row := mustMapEvent(t, redact.New(), event, (*pricing.Table)(nil))
	if row.GroupID != 13 {
		t.Fatalf("top-level GroupID = %d, want final attribution 13", row.GroupID)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create attributed RequestLog: %v", err)
	}

	service := newRequestLogTestService(db)
	for _, groupID := range []uint{12, 13} {
		page, err := service.List(context.Background(), ListQuery{
			GroupID: &groupID,
			Limit:   50,
		})
		if err != nil {
			t.Fatalf("List(GroupID=%d) error = %v", groupID, err)
		}
		if got, want := requestIDs(page.Items), []string{event.RequestID}; !reflect.DeepEqual(got, want) {
			t.Fatalf("List(GroupID=%d) IDs = %v, want %v", groupID, got, want)
		}
	}
}

func TestServiceListBatchLoadsCurrentAccessKeyNames(t *testing.T) {
	db := openRequestLogQueryDB(t)
	current := models.AccessKey{
		Name: "before-rename", KeyValue: "cipher-current", KeyHash: "hash-current",
		KeySuffix: "0001", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create current AccessKey: %v", err)
	}
	deleted := models.AccessKey{
		Name: "deleted", KeyValue: "cipher-deleted", KeyHash: "hash-deleted",
		KeySuffix: "0002", Status: "active", Filters: models.JSON(`{}`),
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

	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50})
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

	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50})
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
		CompletedAtMS: completedAt.UTC().UnixMilli(),
		AccessKeyID:   accessKeyID,
		Protocol:      string(protocol.OpenAICompletions),
		ClientModel:   clientModel,
		UpstreamModel: "upstream-" + clientModel,
		Status:        string(telemetry.RequestStatusSuccess),
		StatusCode:    200,
		DurationMs:    25,
		UsageState:    string(usage.StateNotApplicable),
		CostState:     string(pricing.CostStateNotApplicable),
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
