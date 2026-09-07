package requestlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

func TestPriceMultipliersSurvivePersistenceAndAllCostQueries(t *testing.T) {
	event := channelScopedEvent(t, "00000000-0000-4000-8000-000000009001")
	event.Usage.Result = usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1000}}
	event.Usage.Pricing.EstimatedCostNanoUSD = 2_400_000
	receipt := map[string]any{
		"schema_version": 5, "method": "unit_rate_sum", "method_version": 1,
		"currency": "USD", "pricing_mode": "standard",
		"rule":              map[string]any{"channel_id": "openai", "model_id": event.UpstreamModel},
		"price_multipliers": map[string]any{"group": "0.8", "access_key": "1.5"},
		"line_items": []any{map[string]any{
			"code": "input", "quantity": 1000, "rate_nano_usd_per_million": 2_000_000_000,
			"multiplier": map[string]any{"numerator": 1, "denominator": 1},
			"state":      "priced", "amount_nano_usd": 2_400_000,
		}},
		"total_nano_usd": 2_400_000,
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	event.Usage.Pricing.ReceiptJSON = string(encoded)
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatalf("map adjusted estimate: %v", err)
	}
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	if err := service.writer.WriteBatch(t.Context(), []models.RequestLog{row}); err != nil {
		t.Fatal(err)
	}
	// 重放同一请求不能重复累计已应用倍率的金额。
	if err := service.writer.WriteBatch(t.Context(), []models.RequestLog{row}); err != nil {
		t.Fatal(err)
	}
	minimum := int64(2_300_000)
	page, err := service.List(t.Context(), ListQuery{Limit: 10, CostMinNanoUSD: &minimum})
	if err != nil || len(page.Items) != 1 || page.Items[0].EstimatedCostNanoUSD != 2_400_000 || len(page.Items[0].Attempts) != 0 {
		t.Fatalf("filtered list = %#v, %v", page, err)
	}
	detail, err := service.Get(t.Context(), row.ID)
	if err != nil || len(detail.Attempts) != 1 || detail.Attempts[0].PricingReceipt == nil {
		t.Fatalf("detail = %#v, %v", detail, err)
	}
	frozen, err := json.Marshal(detail.Attempts[0].PricingReceipt)
	if err != nil || !strings.Contains(string(frozen), `"price_multipliers":{"group":"0.8","access_key":"1.5"}`) {
		t.Fatalf("frozen receipt = %s, %v", frozen, err)
	}
	from := event.CompletedAt.Truncate(time.Hour)
	report, err := service.QueryUsage(t.Context(), UsageQuery{
		FromMS: from.UnixMilli(), ToMS: from.Add(time.Hour).UnixMilli(), Granularity: UsageGranularityHour,
	})
	if err != nil || report.Summary.EstimatedCostNanoUSD != 2_400_000 || report.Summary.RequestCount != 1 {
		t.Fatalf("usage report = %#v, %v", report, err)
	}
	for _, dimension := range []UsageDistributionDimension{UsageDistributionDimensionGroup, UsageDistributionDimensionModel, UsageDistributionDimensionAccessKey} {
		distribution, ok := report.Distributions.Get(dimension, UsageDistributionMetricCost)
		var total int64
		for _, item := range distribution.Items {
			total += item.EstimatedCostNanoUSD
		}
		if distribution.Other != nil {
			total += distribution.Other.EstimatedCostNanoUSD
		}
		if !ok || total != 2_400_000 {
			t.Fatalf("cost distribution %s = %#v", dimension, distribution)
		}
	}
	home, err := service.QueryHomeStatistics(t.Context(), HomeStatisticsQuery{
		Range: HomeStatistics24H, ObservedAtMS: from.Add(time.Hour).UnixMilli(),
	})
	if err != nil || home.Summary.EstimatedCostNanoUSD != 2_400_000 || len(home.TopModels) != 1 || len(home.TopGroups) != 1 || len(home.TopAccessKeys) != 1 ||
		home.TopModels[0].EstimatedCostNanoUSD != 2_400_000 || home.TopGroups[0].EstimatedCostNanoUSD != 2_400_000 || home.TopAccessKeys[0].EstimatedCostNanoUSD != 2_400_000 {
		t.Fatalf("home statistics = %#v, %v", home, err)
	}
	for _, source := range []CredentialWindowUsageSource{CredentialWindowUsageSourceRequestLogs, CredentialWindowUsageSourceHourlyStats} {
		observed, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
			CredentialID: row.CredentialID, FromMS: from.UnixMilli(), ToMS: from.Add(time.Hour).UnixMilli(), Source: source,
		})
		if err != nil || observed.EstimatedCostNanoUSD != 2_400_000 {
			t.Fatalf("credential window %s = %#v, %v", source, observed, err)
		}
	}
	row.AttemptRows[0].ChannelID = "anthropic"
	if _, err := decodeAttemptPricingReceipt(row.AttemptRows[0]); err == nil {
		t.Fatal("v5 receipt accepted a mismatching channel")
	}
}
