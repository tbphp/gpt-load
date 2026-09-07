package requestlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

func TestPriceMultipliersSurvivePersistenceAndAllCostQueries(t *testing.T) {
	for _, test := range []struct {
		name       string
		schema     int
		group      string
		accessKey  string
		tokens     usage.Tokens
		rate       int64
		lineAmount int64
		baseTotal  int64
		finalTotal int64
	}{
		{"v6 frozen original and adjusted totals", 6, "0.8", "1.5", usage.Tokens{UncachedInput: 1000}, 2_000_000_000, 2_000_000, 2_000_000, 2_400_000},
		// 原始两项各 0.6 nano，分别舍入为 1，合计 2 后乘 2，最终为 4。
		{"v6 rounds original lines before adjusting total", 6, "0.8", "2.5", usage.Tokens{UncachedInput: 1, Output: 1}, 600_000, 1, 2, 4},
		// v5 各项 0.6 × 2 后舍入为 1，总计 2；读取时不得套用 v6 的总费公式。
		{"v5 preserves historical line rounding", 5, "0.8", "2.5", usage.Tokens{UncachedInput: 1, Output: 1}, 600_000, 1, 0, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := channelScopedEvent(t, "00000000-0000-4000-8000-000000009001")
			event.Usage.Result = usage.Result{State: usage.StateComplete, Tokens: test.tokens}
			event.Usage.Pricing.EstimatedCostNanoUSD = test.finalTotal
			lines := []any{map[string]any{
				"code": "input", "quantity": test.tokens.UncachedInput, "rate_nano_usd_per_million": test.rate,
				"multiplier": map[string]any{"numerator": 1, "denominator": 1},
				"state":      "priced", "amount_nano_usd": test.lineAmount,
			}}
			if test.tokens.Output > 0 {
				lines = append(lines, map[string]any{
					"code": "output", "quantity": test.tokens.Output, "rate_nano_usd_per_million": test.rate,
					"multiplier": map[string]any{"numerator": 1, "denominator": 1},
					"state":      "priced", "amount_nano_usd": test.lineAmount,
				})
			}
			receipt := map[string]any{
				"schema_version": test.schema, "method": "unit_rate_sum", "method_version": 1,
				"currency": "USD", "pricing_mode": "standard",
				"rule":              map[string]any{"channel_id": "openai", "model_id": event.UpstreamModel},
				"price_multipliers": map[string]any{"group": test.group, "access_key": test.accessKey},
				"line_items":        lines,
				"total_nano_usd":    test.finalTotal,
			}
			if test.schema == 6 {
				receipt["base_total_nano_usd"] = test.baseTotal
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
			minimum := test.finalTotal - 1
			page, err := service.List(t.Context(), ListQuery{Limit: 10, CostMinNanoUSD: &minimum})
			if err != nil || len(page.Items) != 1 || page.Items[0].EstimatedCostNanoUSD != test.finalTotal || len(page.Items[0].Attempts) != 0 {
				t.Fatalf("filtered list = %#v, %v", page, err)
			}
			detail, err := service.Get(t.Context(), row.ID)
			if err != nil || detail.EstimatedCostNanoUSD != test.finalTotal || len(detail.Attempts) != 1 || detail.Attempts[0].PricingReceipt == nil {
				t.Fatalf("detail = %#v, %v", detail, err)
			}
			frozen, err := json.Marshal(detail.Attempts[0].PricingReceipt)
			if err != nil || !strings.Contains(string(frozen), fmt.Sprintf(`"price_multipliers":{"group":%q,"access_key":%q}`, test.group, test.accessKey)) {
				t.Fatalf("frozen receipt = %s, %v", frozen, err)
			}
			var frozenFields map[string]json.RawMessage
			if err := json.Unmarshal(frozen, &frozenFields); err != nil {
				t.Fatal(err)
			}
			base, hasBase := frozenFields["base_total_nano_usd"]
			if hasBase != (test.schema == 6) || (hasBase && string(base) != fmt.Sprint(test.baseTotal)) {
				t.Fatalf("frozen base total = %s, want schema v%d base %d", frozen, test.schema, test.baseTotal)
			}
			for _, line := range detail.Attempts[0].PricingReceipt.LineItems {
				if line.AmountNanoUSD == nil || *line.AmountNanoUSD != test.lineAmount {
					t.Fatalf("frozen line amount = %#v, want %d", line, test.lineAmount)
				}
			}
			from := event.CompletedAt.Truncate(time.Hour)
			report, err := service.QueryUsage(t.Context(), UsageQuery{
				FromMS: from.UnixMilli(), ToMS: from.Add(time.Hour).UnixMilli(), Granularity: UsageGranularityHour,
			})
			if err != nil || report.Summary.EstimatedCostNanoUSD != test.finalTotal || report.Summary.RequestCount != 1 {
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
				if !ok || total != test.finalTotal {
					t.Fatalf("cost distribution %s = %#v", dimension, distribution)
				}
			}
			home, err := service.QueryHomeStatistics(t.Context(), HomeStatisticsQuery{
				Range: HomeStatistics24H, ObservedAtMS: from.Add(time.Hour).UnixMilli(),
			})
			if err != nil || home.Summary.EstimatedCostNanoUSD != test.finalTotal || len(home.TopModels) != 1 || len(home.TopGroups) != 1 || len(home.TopAccessKeys) != 1 ||
				home.TopModels[0].EstimatedCostNanoUSD != test.finalTotal || home.TopGroups[0].EstimatedCostNanoUSD != test.finalTotal || home.TopAccessKeys[0].EstimatedCostNanoUSD != test.finalTotal {
				t.Fatalf("home statistics = %#v, %v", home, err)
			}
			for _, source := range []CredentialWindowUsageSource{CredentialWindowUsageSourceRequestLogs, CredentialWindowUsageSourceHourlyStats} {
				observed, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
					CredentialID: row.CredentialID, FromMS: from.UnixMilli(), ToMS: from.Add(time.Hour).UnixMilli(), Source: source,
				})
				if err != nil || observed.EstimatedCostNanoUSD != test.finalTotal {
					t.Fatalf("credential window %s = %#v, %v", source, observed, err)
				}
			}
			row.AttemptRows[0].ChannelID = "anthropic"
			if _, err := decodeAttemptPricingReceipt(row.AttemptRows[0]); err == nil {
				t.Fatalf("v%d receipt accepted a mismatching channel", test.schema)
			}
			if test.schema == 6 {
				event.Usage.Pricing.EstimatedCostNanoUSD = test.baseTotal
				if _, err := mapEvent(redact.New(), event); err == nil {
					t.Fatal("accepted original total as final estimated cost")
				}
			}
		})
	}
}
