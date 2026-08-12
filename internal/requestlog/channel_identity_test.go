package requestlog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventFreezesChannelCredentialAndExecutionAttempt(t *testing.T) {
	event := channelScopedEvent(t, "00000000-0000-4000-8000-000000007001")

	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatalf("mapEvent() error = %v", err)
	}
	if row.ChannelID != string(channel.OpenAI) || row.CredentialID != 8 {
		t.Fatalf("request attribution = %q/%d", row.ChannelID, row.CredentialID)
	}
	if len(row.AttemptRows) != 1 {
		t.Fatalf("attempt rows = %#v", row.AttemptRows)
	}
	attempt := row.AttemptRows[0]
	if attempt.ChannelID != string(channel.OpenAI) || attempt.CredentialID != 8 ||
		attempt.Operation != string(execution.OperationChatCompletion) ||
		attempt.RouteMode != string(channel.RouteNative) ||
		attempt.DispatchState != string(execution.DispatchMaybeSent) ||
		!attempt.ResponseStarted || !attempt.Committed ||
		attempt.UpstreamRequestID != "upstream-request-1" {
		t.Fatalf("frozen attempt = %#v", attempt)
	}
}

func TestMapEventV3ReceiptMustMatchChannelAndModel(t *testing.T) {
	event := channelScopedEvent(t, "00000000-0000-4000-8000-000000007002")
	receipt := decodeReceiptJSON(t, event.Usage.Pricing.ReceiptJSON)
	receipt.Rule.ChannelID = string(channel.Anthropic)
	event.Usage.Pricing.ReceiptJSON = encodeReceiptJSON(t, receipt)
	if _, err := mapEvent(redact.New(), event); err == nil ||
		!strings.Contains(err.Error(), "frozen pricing observation") {
		t.Fatalf("mapEvent() error = %v, want v3 channel/model mismatch", err)
	}

	event = channelScopedEvent(t, "00000000-0000-4000-8000-000000007003")
	receipt = decodeReceiptJSON(t, event.Usage.Pricing.ReceiptJSON)
	receipt.Rule.ModelID = "other-model"
	event.Usage.Pricing.ReceiptJSON = encodeReceiptJSON(t, receipt)
	if _, err := mapEvent(redact.New(), event); err == nil ||
		!strings.Contains(err.Error(), "frozen pricing observation") {
		t.Fatalf("mapEvent() error = %v, want v3 channel/model mismatch", err)
	}
}

func TestMapEventDoesNotAcceptHistoricalReceiptSchemasForNewWrites(t *testing.T) {
	event := channelScopedEvent(t, "00000000-0000-4000-8000-000000007004")
	event.Usage.Pricing.ReceiptJSON = encodeReceiptJSON(t, emptyReceipt(2, pricing.ReceiptRule{
		ModelID: event.UpstreamModel,
	}))
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("mapEvent() accepted a historical v2 receipt for a new write")
	}
}

func TestMapEventSanitizesUpstreamRequestID(t *testing.T) {
	event := channelScopedEvent(t, "00000000-0000-4000-8000-000000007005")
	event.Attempts[0].UpstreamRequestID = "sk-secret-upstream-request"
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatalf("mapEvent() error = %v", err)
	}
	if len(row.AttemptRows) != 1 || strings.Contains(row.AttemptRows[0].UpstreamRequestID, "secret") ||
		row.AttemptRows[0].UpstreamRequestID != redact.Placeholder {
		t.Fatalf("sanitized upstream request ID = %#v", row.AttemptRows)
	}
}

func TestDecodeAttemptRowsAcceptsHistoricalReceiptsButChecksV3Identity(t *testing.T) {
	for _, schemaVersion := range []int{1, 2} {
		rule := pricing.ReceiptRule{ModelID: "model-a"}
		if schemaVersion == 1 {
			rule.ScopeKey = "provider:openai"
		}
		rows := []models.RequestLogAttempt{{
			ChannelID:      string(channel.OpenAI),
			CredentialID:   9,
			UpstreamModel:  "model-a",
			PricingReceipt: models.JSON(encodeReceiptJSON(t, emptyReceipt(schemaVersion, rule))),
		}}
		if _, err := decodeAttemptRows(rows); err != nil {
			t.Fatalf("decodeAttemptRows(v%d) error = %v", schemaVersion, err)
		}
	}

	v3 := emptyReceipt(3, pricing.ReceiptRule{
		ChannelID: string(channel.Anthropic),
		ModelID:   "model-a",
	})
	_, err := decodeAttemptRows([]models.RequestLogAttempt{{
		ChannelID:      string(channel.OpenAI),
		CredentialID:   9,
		UpstreamModel:  "model-a",
		PricingReceipt: models.JSON(encodeReceiptJSON(t, v3)),
	}})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("decodeAttemptRows(v3 mismatch) error = %v", err)
	}
}

func TestUsageAggregationKeepsChannelAndCredentialIdentitiesSeparate(t *testing.T) {
	completedAt := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	first := aggregationRow("channel-a", completedAt, 7, "shared-model")
	first.ChannelID = string(channel.OpenAI)
	first.CredentialID = 11
	second := aggregationRow("channel-b", completedAt, 7, "shared-model")
	second.ChannelID = string(channel.Anthropic)
	second.CredentialID = 11
	third := aggregationRow("credential-b", completedAt, 7, "shared-model")
	third.ChannelID = string(channel.OpenAI)
	third.CredentialID = 12

	deltas, err := buildUsageStatDeltas([]models.RequestLog{first, second, third})
	if err != nil {
		t.Fatalf("buildUsageStatDeltas() error = %v", err)
	}
	if len(deltas) != 3 {
		t.Fatalf("usage identities = %#v, want three channel/credential identities", deltas)
	}

	journals, err := buildUsageAggregationJournals([]models.RequestLog{first})
	if err != nil {
		t.Fatalf("buildUsageAggregationJournals() error = %v", err)
	}
	if len(journals) != 1 || journals[0].ChannelID != first.ChannelID ||
		journals[0].CredentialID != first.CredentialID {
		t.Fatalf("journal attribution = %#v", journals)
	}
}

func TestUsageWriterPersistsSeparateChannelCredentialStats(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completedAt := time.Date(2026, time.August, 9, 1, 30, 0, 0, time.UTC)
	first := aggregationRow("channel-write-a", completedAt, 7, "shared-model")
	first.ChannelID = string(channel.OpenAI)
	first.CredentialID = 11
	second := aggregationRow("channel-write-b", completedAt, 7, "shared-model")
	second.ChannelID = string(channel.Anthropic)
	second.CredentialID = 11
	third := aggregationRow("credential-write-b", completedAt, 7, "shared-model")
	third.ChannelID = string(channel.OpenAI)
	third.CredentialID = 12

	if err := (&gormBatchWriter{db: db}).WriteBatch(t.Context(), []models.RequestLog{
		first,
		second,
		third,
	}); err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	var stats []models.UsageStat
	if err := db.Order("channel_id ASC").Order("credential_id ASC").Find(&stats).Error; err != nil {
		t.Fatalf("query usage stats: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("usage stats = %#v, want three exact identities", stats)
	}
}

func TestQueryUsageFiltersAndProjectsChannelCredential(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.August, 9, 2, 0, 0, 0, time.UTC)
	first := usageStat(start, 7, "shared-model", 2)
	first.ChannelID = string(channel.OpenAI)
	first.CredentialID = 11
	second := usageStat(start, 7, "shared-model", 5)
	second.ChannelID = string(channel.Anthropic)
	second.CredentialID = 12
	createUsageStats(t, db, first, second)

	credentialID := uint(11)
	report, err := newRequestLogTestService(db).QueryUsage(t.Context(), UsageQuery{
		FromMS:             start.UnixMilli(),
		ToMS:               start.Add(time.Hour).UnixMilli(),
		Granularity:        UsageGranularityHour,
		ChannelID:          channel.OpenAI,
		CredentialID:       &credentialID,
		Distribution:       UsageDistributionDimensionGroup,
		DistributionMetric: UsageDistributionMetricRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 2 || len(report.Distribution.Items) != 1 ||
		report.Distribution.Items[0].GroupID != 7 ||
		report.Distribution.Items[0].RequestCount != 2 {
		t.Fatalf("scoped usage report = %#v", report)
	}
}

func TestAccessScopedUsageDistributionCollapsesHiddenRoutesByModel(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.August, 9, 2, 30, 0, 0, time.UTC).Truncate(time.Hour)
	rows := []models.UsageStat{
		usageStat(start, 7, "shared-model", 2),
		usageStat(start, 8, "shared-model", 3),
		usageStat(start, 9, "shared-model", 5),
	}
	for index := range rows {
		rows[index].AccessKeyID = 41
		rows[index].ChannelID = string(channel.OpenAI)
		rows[index].CredentialID = uint(11 + index)
	}
	rows[2].ChannelID = string(channel.Anthropic)
	createUsageStats(t, db, rows...)

	accessKeyID := uint(41)
	report, err := newRequestLogTestService(db).QueryUsage(t.Context(), UsageQuery{
		FromMS:             start.UnixMilli(),
		ToMS:               start.Add(time.Hour).UnixMilli(),
		Granularity:        UsageGranularityHour,
		AccessKeyID:        &accessKeyID,
		Distribution:       UsageDistributionDimensionModel,
		DistributionMetric: UsageDistributionMetricRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(report.Distribution.Items) != 1 {
		t.Fatalf("access-scoped distribution = %#v", report)
	}
	distribution := report.Distribution.Items[0]
	if distribution.GroupID != 0 || distribution.Model != "shared-model" ||
		distribution.RequestCount != 10 {
		t.Fatalf("access-scoped distribution exposes or fragments routes: %#v", distribution)
	}
}

func TestAccessScopedUsageForcesModelDistribution(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.August, 9, 2, 45, 0, 0, time.UTC).Truncate(time.Hour)
	first := usageStat(start, 7, "shared-model", 2)
	first.AccessKeyID = 41
	second := usageStat(start, 8, "shared-model", 3)
	second.AccessKeyID = 41
	createUsageStats(t, db, first, second)

	accessKeyID := uint(41)
	report, err := newRequestLogTestService(db).QueryUsage(t.Context(), UsageQuery{
		FromMS:             start.UnixMilli(),
		ToMS:               start.Add(time.Hour).UnixMilli(),
		Granularity:        UsageGranularityHour,
		AccessKeyID:        &accessKeyID,
		Distribution:       UsageDistributionDimensionGroup,
		DistributionMetric: UsageDistributionMetricRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Distribution.Dimension != UsageDistributionDimensionModel ||
		len(report.Distribution.Items) != 1 ||
		report.Distribution.Items[0].Model != "shared-model" ||
		report.Distribution.Items[0].RequestCount != 5 {
		t.Fatalf("access-scoped distribution = %#v", report.Distribution)
	}
}

func TestListFiltersAttemptsByChannelAndCredential(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completedAt := time.Date(2026, time.August, 9, 3, 0, 0, 0, time.UTC)
	first := requestLogQueryRow("channel-list-a", completedAt, 1, "model-a", []Attempt{{
		GroupID: 7, ChannelID: channel.OpenAI, CredentialID: 11,
	}})
	first.GroupID = 7
	first.ChannelID = string(channel.OpenAI)
	first.CredentialID = 11
	second := requestLogQueryRow("channel-list-b", completedAt.Add(-time.Second), 1, "model-a", []Attempt{{
		GroupID: 7, ChannelID: channel.Anthropic, CredentialID: 12,
	}})
	createRequestLogQueryRow(t, db, first)
	createRequestLogQueryRow(t, db, second)

	credentialID := uint(11)
	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{
		ChannelID: channel.OpenAI, CredentialID: &credentialID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].RequestID != first.ID ||
		page.Items[0].ChannelID != channel.OpenAI || page.Items[0].CredentialID != 11 {
		t.Fatalf("filtered page = %#v", page)
	}
}

func channelScopedEvent(t *testing.T, requestID string) telemetry.RequestEvent {
	t.Helper()
	event := testEvent(requestID)
	event.Attempts[0].ChannelID = channel.OpenAI
	event.Attempts[0].CredentialID = 8
	event.Attempts[0].Operation = execution.OperationChatCompletion
	event.Attempts[0].RouteMode = channel.RouteNative
	event.Attempts[0].UpstreamRequestID = "upstream-request-1"
	event.Attempts[0].DispatchState = execution.DispatchMaybeSent
	event.Attempts[0].ResponseStarted = true
	event.Attempts[0].Committed = true
	event.Usage.ChannelID = channel.OpenAI
	event.Usage.CredentialID = 8
	event.Usage.Result = usage.Result{State: usage.StateComplete}
	event.Usage.Pricing = telemetry.PricingObservation{
		UpstreamModel:        event.UpstreamModel,
		CostState:            string(pricing.CostStatePriced),
		PricingCompleteness:  string(pricing.CompletenessComplete),
		EstimatedCostNanoUSD: 0,
		ReceiptJSON: encodeReceiptJSON(t, emptyReceipt(3, pricing.ReceiptRule{
			ChannelID: string(channel.OpenAI),
			ModelID:   event.UpstreamModel,
		})),
	}
	return event
}

func emptyReceipt(schemaVersion int, rule pricing.ReceiptRule) pricing.Receipt {
	return pricing.Receipt{
		SchemaVersion: schemaVersion,
		Method:        pricing.ReceiptMethodUnitRateSum,
		MethodVersion: 1,
		Currency:      "USD",
		Rule:          rule,
		LineItems:     []pricing.ReceiptLine{},
	}
}

func encodeReceiptJSON(t *testing.T, receipt pricing.Receipt) string {
	t.Helper()
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("json.Marshal(receipt) error = %v", err)
	}
	return string(encoded)
}

func decodeReceiptJSON(t *testing.T, encoded string) pricing.Receipt {
	t.Helper()
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(encoded), &receipt); err != nil {
		t.Fatalf("json.Unmarshal(receipt) error = %v", err)
	}
	return receipt
}
