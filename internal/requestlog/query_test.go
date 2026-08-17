package requestlog

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/testutil/sqlitetest"
	"gpt-load/internal/usage"
)

func TestDecodeRequestLogRowsPreservesUsageCostAttribution(t *testing.T) {
	reasoningBudget := int64(8192)
	rows := []models.RequestLog{{
		ID:                    "00000000-0000-4000-8000-000000000601",
		CompletedAtMS:         1_785_085_323_000,
		AccessKeyID:           61,
		GroupID:               17,
		Protocol:              string(protocol.OpenAICompletions),
		Operation:             string(execution.OperationChatCompletion),
		ClientModel:           "client-model",
		UpstreamModel:         "upstream-model",
		UpstreamReportedModel: "upstream-model",
		ModelConsistency:      string(telemetry.ModelConsistencyMatch),
		Status:                string(telemetry.RequestStatusSuccess),
		StatusCode:            200,
		DurationMs:            25,
		ReasoningMode:         "adaptive",
		ReasoningEffort:       "high",
		ReasoningBudgetTokens: &reasoningBudget,
		UncachedInputTokens:   11,
		CacheReadTokens:       12,
		CacheWrite5MTokens:    13,
		CacheWrite1HTokens:    14,
		OutputTokens:          15,
		EstimatedCostNanoUSD:  123_456_789,
		UsageState:            string(usage.StateComplete),
		CostState:             string(pricing.CostStatePriced),
		PricingCompleteness:   string(pricing.CompletenessComplete),
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
		got.Reasoning.Mode != "adaptive" || got.Reasoning.Effort != "high" ||
		got.Reasoning.BudgetTokens == nil || *got.Reasoning.BudgetTokens != reasoningBudget ||
		got.UpstreamReportedModel != "upstream-model" ||
		got.ModelConsistency != telemetry.ModelConsistencyMatch ||
		got.Operation != execution.OperationChatCompletion ||
		got.CompletedAtMS != 1_785_085_323_000 {
		t.Fatalf("decoded usage/cost record = %#v", got)
	}
}

func TestDecodeAttemptRowsExposesOnlyNormalizedSafeFields(t *testing.T) {
	reasoningBudget := int64(4096)
	attempts, err := decodeAttemptRows([]models.RequestLogAttempt{{
		RequestID:             "00000000-0000-4000-8000-000000000605",
		Sequence:              1,
		GroupID:               7,
		GroupName:             "Primary",
		CredentialID:          11,
		UpstreamProtocol:      string(protocol.Anthropic),
		ReasoningMode:         "enabled",
		ReasoningEffort:       "high",
		ReasoningBudgetTokens: &reasoningBudget,
		UpstreamModel:         "model",
		StatusCode:            200,
		DurationMs:            10,
		FailureCategory:       string(telemetry.FailureCategoryOK),
		Action:                string(telemetry.ActionTerminate),
	}})
	if err != nil {
		t.Fatalf("decodeAttemptRows() error = %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one attempt", attempts)
	}
	encoded, err := json.Marshal(attempts[0])
	if err != nil {
		t.Fatalf("marshal decoded attempt: %v", err)
	}
	for _, forbidden := range [][]byte{[]byte(`"key_mask"`), []byte("prov"), []byte("safe")} {
		if bytes.Contains(encoded, forbidden) {
			t.Fatalf("decoded attempt retains historical key material %q: %s", forbidden, encoded)
		}
	}
	if bytes.Contains(encoded, []byte(`"key_id"`)) ||
		!bytes.Contains(encoded, []byte(`"credential_id":11`)) {
		t.Fatalf("decoded attempt identity JSON = %s, want credential_id only", encoded)
	}
	if attempts[0].UpstreamProtocol != protocol.Anthropic ||
		attempts[0].Reasoning.Mode != "enabled" || attempts[0].Reasoning.Effort != "high" ||
		attempts[0].Reasoning.BudgetTokens == nil ||
		*attempts[0].Reasoning.BudgetTokens != reasoningBudget {
		t.Fatalf("decoded attempt observation = %#v", attempts[0])
	}
}

func TestDecodeAttemptRowsRejectsInvalidUpstreamProtocolOnEveryAttempt(t *testing.T) {
	_, err := decodeAttemptRows([]models.RequestLogAttempt{
		{Sequence: 1, UpstreamProtocol: "private-sdk-wire"},
		{Sequence: 2, UpstreamProtocol: string(protocol.OpenAICompletions)},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid upstream protocol") {
		t.Fatalf("decodeAttemptRows() error = %v, want invalid upstream protocol", err)
	}
}

func TestDecodeRequestLogRowsRejectsInvalidUsageCostValues(t *testing.T) {
	base := models.RequestLog{
		ID:                  "00000000-0000-4000-8000-000000000602",
		CompletedAtMS:       1_785_110_400_000,
		Protocol:            string(protocol.OpenAICompletions),
		ModelConsistency:    string(telemetry.ModelConsistencyNotApplicable),
		Status:              string(telemetry.RequestStatusSuccess),
		UsageState:          string(usage.StateComplete),
		CostState:           string(pricing.CostStatePriced),
		PricingCompleteness: string(pricing.CompletenessComplete),
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
				ModelConsistency:     string(telemetry.ModelConsistencyNotApplicable),
				Status:               string(telemetry.RequestStatusSuccess),
				UsageState:           string(test.usageState),
				CostState:            string(test.costState),
				PricingCompleteness:  string(test.completeness),
				EstimatedCostNanoUSD: test.cost,
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

func TestDecodeRequestLogRowsRejectsInvalidModelConsistency(t *testing.T) {
	row := models.RequestLog{
		ID:                    "00000000-0000-4000-8000-000000000606",
		CompletedAtMS:         1_785_110_400_000,
		Protocol:              string(protocol.OpenAICompletions),
		UpstreamModel:         "model-a",
		UpstreamReportedModel: "",
		ModelConsistency:      string(telemetry.ModelConsistencyMismatch),
		Status:                string(telemetry.RequestStatusSuccess),
		UsageState:            string(usage.StateNotApplicable),
		CostState:             string(pricing.CostStateNotApplicable),
		PricingCompleteness:   string(pricing.CompletenessNotApplicable),
	}
	if _, err := decodeRequestLogRows([]models.RequestLog{row}); err == nil {
		t.Fatal("decodeRequestLogRows() accepted inconsistent model observation")
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
		{Sequence: 1, GroupID: 12, GroupName: "retry-first", UpstreamModel: "different-upstream-model", WillRetry: true},
		{Sequence: 2, GroupID: 13, GroupName: "retry-second", UpstreamModel: "different-upstream-model"},
	}
	markError := func(row *models.RequestLog) {
		row.Status = string(telemetry.RequestStatusError)
		row.UpstreamReportedModel = ""
		row.ModelConsistency = string(telemetry.ModelConsistencyNotApplicable)
	}
	target := requestLogQueryRow(targetID, from, 71, "client-model", integerAttempts)
	target.UpstreamModel = "different-upstream-model"
	markError(&target)
	createRequestLogQueryRow(t, db, target)

	atTo := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000202",
		to,
		71,
		"client-model",
		integerAttempts,
	)
	markError(&atTo)
	createRequestLogQueryRow(t, db, atTo)

	beforeFrom := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000203",
		from.Add(-time.Millisecond),
		71,
		"client-model",
		integerAttempts,
	)
	markError(&beforeFrom)
	createRequestLogQueryRow(t, db, beforeFrom)

	stringGroup := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000204",
		from.Add(time.Minute),
		71,
		"client-model",
		nil,
	)
	markError(&stringGroup)
	createRequestLogQueryRow(t, db, stringGroup)

	realGroup := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000205",
		from.Add(2*time.Minute),
		71,
		"client-model",
		nil,
	)
	markError(&realGroup)
	createRequestLogQueryRow(t, db, realGroup)

	upstreamOnly := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000206",
		from.Add(3*time.Minute),
		71,
		"wrong-client-model",
		integerAttempts,
	)
	upstreamOnly.UpstreamModel = "client-model"
	markError(&upstreamOnly)
	createRequestLogQueryRow(t, db, upstreamOnly)

	wrongAccessKey := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000208",
		from.Add(5*time.Minute),
		72,
		"client-model",
		integerAttempts,
	)
	markError(&wrongAccessKey)
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
	markError(&zeroAttempts)
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
		{Sequence: 1, GroupID: 12, GroupName: "retry", ChannelID: channel.OpenAI, CredentialID: 1, Operation: execution.OperationChatCompletion, RouteMode: channel.RouteConverted, UpstreamProtocol: protocol.Anthropic, UpstreamModel: "retry-model", DispatchState: execution.DispatchMaybeSent, FailureCategory: telemetry.FailureCategoryRateLimited, Action: telemetry.ActionRetry, WillRetry: true},
		{Sequence: 2, GroupID: 13, GroupName: "final", ChannelID: channel.OpenAI, CredentialID: 2, Operation: execution.OperationChatCompletion, RouteMode: channel.RouteNative, UpstreamProtocol: protocol.OpenAICompletions, UpstreamModel: "final-model", DispatchState: execution.DispatchMaybeSent, FailureCategory: telemetry.FailureCategoryOK, Action: telemetry.ActionTerminate},
	}
	event.UpstreamModel = "final-model"
	event.UpstreamReportedModel = "final-model"
	event.Usage = telemetry.UsageObservation{
		GroupID: 13, ChannelID: channel.OpenAI, CredentialID: 2, AttemptSequence: 2,
		Result: usage.Result{
			State: usage.StateNotApplicable,
		},
		Pricing: telemetry.PricingObservation{
			UpstreamModel: "final-model",
			CostState:     string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable),
		},
	}
	row := mustMapEvent(t, redact.New(), event, (*pricing.Table)(nil))
	if row.GroupID != 13 {
		t.Fatalf("top-level GroupID = %d, want final attribution 13", row.GroupID)
	}
	createRequestLogQueryRow(t, db, row)

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
		if page.Items[0].RouteMode != channel.RouteNative {
			t.Fatalf("List(GroupID=%d) final route mode = %q, want %q", groupID, page.Items[0].RouteMode, channel.RouteNative)
		}
		if page.Items[0].UpstreamProtocol != protocol.OpenAICompletions {
			t.Fatalf("List(GroupID=%d) final upstream protocol = %q", groupID, page.Items[0].UpstreamProtocol)
		}
	}
}

func TestServiceListDoesNotInferMissingFinalRouteModeFromEarlierAttempt(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000215",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		71,
		"client-model",
		[]Attempt{
			{Sequence: 1, GroupID: 12, ChannelID: channel.OpenAI, CredentialID: 1, RouteMode: channel.RouteConverted, UpstreamProtocol: protocol.Anthropic},
			{Sequence: 2, GroupID: 12, ChannelID: channel.OpenAI, CredentialID: 1},
		},
	)
	row.GroupID = 12
	row.ChannelID = string(channel.OpenAI)
	row.CredentialID = 1
	createRequestLogQueryRow(t, db, row)

	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("List() items = %#v, want one item", page.Items)
	}
	if page.Items[0].RouteMode != "" {
		t.Fatalf("List() final route mode = %q, want empty", page.Items[0].RouteMode)
	}
	if page.Items[0].UpstreamProtocol != "" {
		t.Fatalf("List() final upstream protocol = %q, want empty", page.Items[0].UpstreamProtocol)
	}
}

func TestServiceListAndDetailExposeFinalPricingMode(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000216",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		71,
		"client-model",
		[]Attempt{{
			Sequence: 1, GroupID: 12, ChannelID: channel.OpenAI, CredentialID: 1,
			RouteMode: channel.RouteNative, UpstreamProtocol: protocol.OpenAICompletions,
			UpstreamModel: "priced-model",
		}},
	)
	row.GroupID = 12
	row.ChannelID = string(channel.OpenAI)
	row.CredentialID = 1
	receipt, err := json.Marshal(pricing.Receipt{
		SchemaVersion: 4,
		Method:        pricing.ReceiptMethodUnitRateSum,
		MethodVersion: 1,
		Currency:      "USD",
		PricingMode:   pricing.ModeFast,
		Rule: pricing.ReceiptRule{
			ChannelID: string(channel.OpenAI),
			ModelID:   "priced-model",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	row.AttemptRows[0].PricingReceipt = receipt
	createRequestLogQueryRow(t, db, row)

	service := newRequestLogTestService(db)
	page, err := service.List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].PricingMode != pricing.ModeFast {
		t.Fatalf("List() items = %#v, want fast pricing mode", page.Items)
	}
	detail, err := service.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.PricingMode != pricing.ModeFast {
		t.Fatalf("Get() pricing mode = %q, want fast", detail.PricingMode)
	}
}

func TestServiceListAndDetailExposeFinalContextThreshold(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000217",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		71,
		"client-model",
		[]Attempt{{
			Sequence: 1, GroupID: 12, ChannelID: channel.OpenAI, CredentialID: 1,
			RouteMode: channel.RouteNative, UpstreamProtocol: protocol.OpenAICompletions,
			UpstreamModel: "tiered-model",
		}},
	)
	row.GroupID = 12
	row.ChannelID = string(channel.OpenAI)
	row.CredentialID = 1
	threshold := int64(272_000)
	receipt, err := json.Marshal(pricing.Receipt{
		SchemaVersion:          4,
		Method:                 pricing.ReceiptMethodUnitRateSum,
		MethodVersion:          1,
		Currency:               "USD",
		PricingMode:            pricing.ModeStandard,
		ContextThresholdTokens: &threshold,
		Rule: pricing.ReceiptRule{
			ChannelID: string(channel.OpenAI),
			ModelID:   "tiered-model",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	row.AttemptRows[0].PricingReceipt = receipt
	createRequestLogQueryRow(t, db, row)

	service := newRequestLogTestService(db)
	page, err := service.List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ContextThresholdTokens == nil ||
		*page.Items[0].ContextThresholdTokens != threshold {
		t.Fatalf("List() context threshold = %#v, want %d", page.Items, threshold)
	}
	detail, err := service.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.ContextThresholdTokens == nil || *detail.ContextThresholdTokens != threshold {
		t.Fatalf("Get() context threshold = %#v, want %d", detail.ContextThresholdTokens, threshold)
	}
}

func TestServiceListAttemptFiltersMustMatchTheSameAttempt(t *testing.T) {
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000211",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		71,
		"client-model",
		[]Attempt{
			{
				Sequence: 1, GroupID: 12, GroupName: "retry", CredentialID: 1,
				StatusCode: 429, FailureCategory: telemetry.FailureCategoryRateLimited,
				Action: telemetry.ActionRetry, WillRetry: true,
			},
			{
				Sequence: 2, GroupID: 13, GroupName: "final", CredentialID: 2,
				StatusCode: 200, FailureCategory: telemetry.FailureCategoryOK,
				Action: telemetry.ActionTerminate,
			},
		},
	)
	createRequestLogQueryRow(t, db, row)
	service := newRequestLogTestService(db)
	groupID := uint(12)
	statusOK := 200

	page, err := service.List(context.Background(), ListQuery{
		GroupID: &groupID, AttemptStatusCode: &statusOK, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List() mismatched attempt filters error = %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("mismatched attempt filters returned %#v, want empty", page.Items)
	}

	statusRateLimited := 429
	page, err = service.List(context.Background(), ListQuery{
		GroupID: &groupID, AttemptStatusCode: &statusRateLimited,
		FailureCategory: telemetry.FailureCategoryRateLimited, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List() matching attempt filters error = %v", err)
	}
	if got, want := requestIDs(page.Items), []string{row.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("matching attempt filters IDs = %v, want %v", got, want)
	}
}

func TestServiceListZeroRetryRangeIncludesRequestsWithoutUpstreamAttempts(t *testing.T) {
	db := openRequestLogQueryDB(t)
	base := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	zero := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000212", base, 71, "zero", nil,
	)
	single := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000213", base.Add(time.Second), 71, "single",
		[]Attempt{{Sequence: 1, GroupID: 12, GroupName: "single", CredentialID: 1}},
	)
	retried := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000214", base.Add(2*time.Second), 71, "retried",
		[]Attempt{
			{Sequence: 1, GroupID: 12, GroupName: "retry", CredentialID: 1, WillRetry: true},
			{Sequence: 2, GroupID: 12, GroupName: "final", CredentialID: 2},
		},
	)
	for _, row := range []models.RequestLog{zero, single, retried} {
		createRequestLogQueryRow(t, db, row)
	}
	minimum, maximum := 0, 0
	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{
		RetryCountMin: &minimum, RetryCountMax: &maximum, Limit: 50,
	})
	if err != nil {
		t.Fatalf("List() zero retry range error = %v", err)
	}
	if got, want := requestIDs(page.Items), []string{single.ID, zero.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("zero retry range IDs = %v, want %v", got, want)
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

func TestServiceListOmitsAttemptsAndDetailLoadsThem(t *testing.T) {
	reasoningBudget := int64(4096)
	db := openRequestLogQueryDB(t)
	row := requestLogQueryRow(
		"00000000-0000-4000-8000-000000000401",
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		91,
		"zero-attempt",
		nil,
	)
	row.AttemptRows = []models.RequestLogAttempt{{
		RequestID:             row.ID,
		Sequence:              1,
		CompletedAtMS:         row.CompletedAtMS,
		GroupID:               7,
		GroupName:             "Primary",
		ChannelID:             string(channel.Anthropic),
		CredentialID:          9,
		Operation:             string(execution.OperationResponsesCreate),
		RouteMode:             string(channel.RouteConverted),
		UpstreamProtocol:      string(protocol.Anthropic),
		ReasoningMode:         "enabled",
		ReasoningEffort:       "high",
		ReasoningBudgetTokens: &reasoningBudget,
		StatusCode:            200,
		DurationMs:            10,
		FailureCategory:       string(telemetry.FailureCategoryOK),
		Action:                string(telemetry.ActionTerminate),
	}}
	row.GroupID = 7
	row.ChannelID = string(channel.Anthropic)
	row.CredentialID = 9
	row.Operation = string(execution.OperationResponsesCreate)
	row.AttemptCount = 1
	createRequestLogQueryRow(t, db, row)

	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Attempts != nil ||
		page.Items[0].AttemptCount != 1 {
		t.Fatalf("list item = %#v, want lightweight item without attempts", page.Items)
	}
	detail, err := newRequestLogTestService(db).Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].GroupName != "Primary" ||
		detail.RouteMode != channel.RouteConverted ||
		detail.UpstreamProtocol != protocol.Anthropic ||
		detail.Operation != execution.OperationResponsesCreate ||
		detail.Attempts[0].Reasoning.BudgetTokens == nil ||
		*detail.Attempts[0].Reasoning.BudgetTokens != reasoningBudget {
		t.Fatalf("detail attempts = %#v", detail.Attempts)
	}
}

func openRequestLogQueryDB(t *testing.T) *gorm.DB {
	t.Helper()
	return sqlitetest.OpenMigrated(t)
}

func requestLogQueryRow(
	id string,
	completedAt time.Time,
	accessKeyID uint,
	clientModel string,
	attempts []Attempt,
) models.RequestLog {
	attemptRows := make([]models.RequestLogAttempt, 0, len(attempts))
	for index, attempt := range attempts {
		groupID := attempt.GroupID
		if groupID == 0 {
			groupID = 1
		}
		credentialID := attempt.CredentialID
		if credentialID == 0 {
			credentialID = attempt.CredentialID
		}
		if credentialID == 0 {
			credentialID = 1
		}
		sequence := attempt.Sequence
		if sequence == 0 {
			sequence = index + 1
		}
		failureCategory := attempt.FailureCategory
		if failureCategory == "" {
			failureCategory = telemetry.FailureCategoryOK
		}
		action := attempt.Action
		if action == "" {
			action = telemetry.ActionTerminate
		}
		attemptRows = append(attemptRows, models.RequestLogAttempt{
			RequestID:             id,
			Sequence:              sequence,
			CompletedAtMS:         completedAt.UTC().UnixMilli(),
			GroupID:               groupID,
			GroupName:             attempt.GroupName,
			ChannelID:             string(attempt.ChannelID),
			CredentialID:          credentialID,
			Operation:             string(attempt.Operation),
			RouteMode:             string(attempt.RouteMode),
			UpstreamModel:         attempt.UpstreamModel,
			UpstreamRequestID:     attempt.UpstreamRequestID,
			DispatchState:         string(attempt.DispatchState),
			ResponseStarted:       attempt.ResponseStarted,
			UpstreamProtocol:      string(attempt.UpstreamProtocol),
			ReasoningMode:         attempt.Reasoning.Mode,
			ReasoningEffort:       attempt.Reasoning.Effort,
			ReasoningBudgetTokens: attempt.Reasoning.BudgetTokens,
			StatusCode:            attempt.StatusCode,
			DurationMs:            attempt.DurationMs,
			FailureCategory:       string(failureCategory),
			Action:                string(action),
			WillRetry:             attempt.WillRetry,
			ErrorCode:             attempt.ErrorCode,
			ErrorSummary:          attempt.ErrorSummary,
			Committed:             attempt.Committed,
		})
	}
	return models.RequestLog{
		ID:                    id,
		CompletedAtMS:         completedAt.UTC().UnixMilli(),
		AccessKeyID:           accessKeyID,
		Protocol:              string(protocol.OpenAICompletions),
		ClientModel:           clientModel,
		UpstreamModel:         "upstream-" + clientModel,
		UpstreamReportedModel: "upstream-" + clientModel,
		ModelConsistency:      string(telemetry.ModelConsistencyMatch),
		Status:                string(telemetry.RequestStatusSuccess),
		StatusCode:            200,
		DurationMs:            25,
		AttemptCount:          len(attemptRows),
		UsageState:            string(usage.StateNotApplicable),
		CostState:             string(pricing.CostStateNotApplicable),
		AttemptRows:           attemptRows,
	}
}

func createRequestLogQueryRow(t *testing.T, db *gorm.DB, row models.RequestLog) {
	t.Helper()
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create RequestLog %s: %v", row.ID, err)
	}
	if len(row.AttemptRows) > 0 {
		if err := db.Create(&row.AttemptRows).Error; err != nil {
			t.Fatalf("create RequestLog attempts %s: %v", row.ID, err)
		}
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
