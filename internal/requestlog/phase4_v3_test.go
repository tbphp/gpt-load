package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventV3PersistsMillisecondLedgerAndQuotesUpstreamModel(t *testing.T) {
	event := telemetry.RequestEvent{
		RequestID:     "00000000-0000-4000-8000-000000004001",
		CompletedAt:   time.Date(2026, time.July, 24, 12, 34, 56, 789_000_000, time.UTC),
		AccessKeyID:   41,
		ClientModel:   "client-alias",
		UpstreamModel: "provider-model",
		Status:        telemetry.RequestStatusSuccess,
		StatusCode:    200,
		Attempts: []telemetry.Attempt{{
			Sequence: 1, GroupID: 73, KeyID: 9, UpstreamModel: "provider-model",
		}},
		Usage: telemetry.UsageObservation{
			GroupID: 73, KeyID: 9, AttemptSequence: 1,
			Result: usage.Result{
				State:  usage.StateComplete,
				Tokens: usage.Tokens{Output: 1_000_000},
			},
			Pricing: telemetry.PricingObservation{
				UpstreamModel: "provider-model",
				CostState:     string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessComplete),
				EstimatedCostNanoUSD: 2_000_000_000,
			},
		},
	}

	row := mustMapEvent(t, redact.New(), event)

	if row.CompletedAtMS != 1_784_896_496_789 {
		t.Fatalf("CompletedAtMS = %d, want 1784896496789", row.CompletedAtMS)
	}
	if row.ClientModel != "client-alias" || row.UpstreamModel != "provider-model" {
		t.Fatalf("projected models = %q/%q", row.ClientModel, row.UpstreamModel)
	}
	if row.EstimatedCostNanoUSD != 2_000_000_000 ||
		row.CostState != string(pricing.CostStatePriced) {
		t.Fatalf(
			"quote = %d/%q, want 2000000000/priced",
			row.EstimatedCostNanoUSD,
			row.CostState,
		)
	}
}
