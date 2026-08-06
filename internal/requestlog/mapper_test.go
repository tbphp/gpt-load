package requestlog

import (
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventPersistsFrozenUsagePricingAndAttribution(t *testing.T) {
	event := testEvent("00000000-0000-4000-8000-000000000101")
	event.CompletedAt = time.Date(2026, time.July, 24, 20, 30, 0, 123, time.FixedZone("test", 8*60*60))
	event.Protocol = protocol.OpenAICompletions
	event.ClientModel = "client-alias"
	reasoningBudget := int64(8192)
	event.Reasoning = reasoning.Config{
		Mode:         "adaptive",
		Effort:       "high",
		BudgetTokens: &reasoningBudget,
	}
	event.Usage.Result = usage.Result{
		State: usage.StatePartial,
		Tokens: usage.Tokens{
			UncachedInput:     1,
			CacheRead:         2,
			CacheWrite5M:      3,
			CacheWrite1H:      4,
			CacheWriteUnknown: 5,
			Output:            6,
		},
	}
	event.Usage.AttemptSequence = 1
	event.Usage.KeyID = 8
	event.Usage.Pricing = telemetry.PricingObservation{
		PriceScopeKey:        "group:7",
		UpstreamModel:        "upstream-model",
		CostState:            string(pricing.CostStatePriced),
		PricingCompleteness:  string(pricing.CompletenessPartial),
		EstimatedCostNanoUSD: 123456,
	}

	row := mustMapEvent(t, redact.New(), event)
	if row.ID != event.RequestID || row.CompletedAtMS != 1_784_896_200_000 {
		t.Fatalf("identity/completed_at_ms = %q/%d", row.ID, row.CompletedAtMS)
	}
	if row.GroupID != 7 || row.ClientModel != "client-alias" || row.UpstreamModel != "upstream-model" {
		t.Fatalf("persisted attribution/models = %+v", row)
	}
	if row.ReasoningMode != "adaptive" || row.ReasoningEffort != "high" ||
		row.ReasoningBudgetTokens == nil || *row.ReasoningBudgetTokens != reasoningBudget {
		t.Fatalf("persisted reasoning = %+v", row)
	}
	if row.UncachedInputTokens != 1 || row.CacheReadTokens != 2 || row.CacheWrite5MTokens != 3 ||
		row.CacheWrite1HTokens != 4 || row.CacheWriteUnknownTokens != 5 || row.OutputTokens != 6 {
		t.Fatalf("persisted token dimensions = %+v", row)
	}
	if row.UsageState != string(usage.StatePartial) || row.CostState != string(pricing.CostStatePriced) ||
		row.PricingCompleteness != string(pricing.CompletenessPartial) || row.EstimatedCostNanoUSD != 123456 {
		t.Fatalf("persisted frozen pricing = %+v", row)
	}

	if len(row.AttemptRows) != 1 || row.AttemptRows[0].GroupID != 7 ||
		row.AttemptRows[0].KeyID != 8 {
		t.Fatalf("attempts = %+v", row.AttemptRows)
	}
}

func TestMapEventPersistsEveryValidFrozenPricingState(t *testing.T) {
	tests := []struct {
		name         string
		usageState   usage.State
		costState    pricing.CostState
		completeness pricing.Completeness
		cost         int64
	}{
		{name: "complete", usageState: usage.StateComplete, costState: pricing.CostStatePriced, completeness: pricing.CompletenessComplete, cost: 99},
		{name: "partial priced", usageState: usage.StatePartial, costState: pricing.CostStatePriced, completeness: pricing.CompletenessPartial, cost: 49},
		{name: "complete unpriced", usageState: usage.StateComplete, costState: pricing.CostStateUnpriced, completeness: pricing.CompletenessUnavailable},
		{name: "missing", usageState: usage.StateMissing, costState: pricing.CostStateUnpriced, completeness: pricing.CompletenessUnavailable},
		{name: "not applicable", usageState: usage.StateNotApplicable, costState: pricing.CostStateNotApplicable, completeness: pricing.CompletenessNotApplicable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("state-" + test.name)
			event.Usage.Result.State = test.usageState
			event.Usage.AttemptSequence = 1
			event.Usage.KeyID = 8
			event.Usage.Pricing = telemetry.PricingObservation{
				PriceScopeKey:        "provider:openai",
				UpstreamModel:        event.UpstreamModel,
				CostState:            string(test.costState),
				PricingCompleteness:  string(test.completeness),
				EstimatedCostNanoUSD: test.cost,
			}
			row := mustMapEvent(t, redact.New(), event)
			if row.UsageState != string(test.usageState) || row.CostState != string(test.costState) ||
				row.PricingCompleteness != string(test.completeness) || row.EstimatedCostNanoUSD != test.cost {
				t.Fatalf("row state = %+v", row)
			}
		})
	}
}

func TestMapEventRejectsInvalidFrozenObservationAtomically(t *testing.T) {
	base := func() telemetry.RequestEvent {
		event := testEvent("invalid-frozen")
		event.Usage.Result = usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 1}}
		event.Usage.AttemptSequence = 1
		event.Usage.KeyID = 8
		event.Usage.Pricing = telemetry.PricingObservation{
			PriceScopeKey: "group:7", UpstreamModel: event.UpstreamModel,
			CostState: string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessComplete),
			EstimatedCostNanoUSD: 1,
		}
		return event
	}
	tests := []struct {
		name   string
		mutate func(*telemetry.RequestEvent)
	}{
		{name: "negative token", mutate: func(event *telemetry.RequestEvent) { event.Usage.Result.Tokens.Output = -1 }},
		{name: "cross field overflow", mutate: func(event *telemetry.RequestEvent) {
			event.Usage.Result.Tokens.UncachedInput = math.MaxInt64
			event.Usage.Result.Tokens.Output = 1
		}},
		{name: "negative cost", mutate: func(event *telemetry.RequestEvent) { event.Usage.Pricing.EstimatedCostNanoUSD = -1 }},
		{name: "priced without identity", mutate: func(event *telemetry.RequestEvent) { event.Usage.Pricing.PriceScopeKey = "" }},
		{name: "invalid canonical scope", mutate: func(event *telemetry.RequestEvent) { event.Usage.Pricing.PriceScopeKey = "group:07" }},
		{name: "group scope does not match bound group", mutate: func(event *telemetry.RequestEvent) {
			event.Usage.Pricing.PriceScopeKey = "group:8"
		}},
		{name: "invalid matrix", mutate: func(event *telemetry.RequestEvent) {
			event.Usage.Pricing.PricingCompleteness = string(pricing.CompletenessUnavailable)
		}},
		{name: "missing matching attempt", mutate: func(event *telemetry.RequestEvent) { event.Usage.AttemptSequence = 2 }},
		{name: "inconsistent top model", mutate: func(event *telemetry.RequestEvent) { event.UpstreamModel = "different" }},
		{name: "inconsistent pricing model", mutate: func(event *telemetry.RequestEvent) { event.Usage.Pricing.UpstreamModel = "different" }},
		{name: "control character in bound model", mutate: func(event *telemetry.RequestEvent) {
			model := "model\nname"
			event.UpstreamModel = model
			event.Attempts[0].UpstreamModel = model
			event.Usage.Pricing.UpstreamModel = model
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base()
			test.mutate(&event)
			if _, err := mapEvent(redact.New(), event); err == nil {
				t.Fatal("mapEvent() error = nil")
			}
		})
	}
}

func TestMapEventAllowsUnboundNoModelResourceOnlyAsNotApplicable(t *testing.T) {
	event := telemetry.RequestEvent{
		RequestID:   "resource",
		CompletedAt: time.Now(),
		Status:      telemetry.RequestStatusSuccess,
		Usage: telemetry.UsageObservation{
			Result: usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{
				CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable),
			},
		},
	}
	if _, err := mapEvent(redact.New(), event); err != nil {
		t.Fatalf("mapEvent() error = %v", err)
	}
	event.Usage.Pricing.PriceScopeKey = "group:7"
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("unbound no-model resource with pricing identity was accepted")
	}
	event.Usage.Pricing.PriceScopeKey = ""
	event.Usage.Result.State = usage.StateComplete
	event.Usage.Pricing.CostState = string(pricing.CostStateUnpriced)
	event.Usage.Pricing.PricingCompleteness = string(pricing.CompletenessUnavailable)
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("unbound billable usage was accepted")
	}
}

func TestMapEventRejectsPreEpochCompletion(t *testing.T) {
	event := testEvent("pre-epoch-completion")
	event.CompletedAt = time.Unix(-1, 0)
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("mapEvent() error = nil")
	}
}

func TestMapEventDefensivelyRedactsAndBoundsSummaries(t *testing.T) {
	const secret = "sk-this-is-a-secret-value"
	unsafeSummary := string([]byte{0xff}) + "\r\n\t " + secret + "   " + strings.Repeat("界", 500)
	event := testEvent("redact")
	event.ErrorSummary = unsafeSummary
	event.Attempts[0].ErrorSummary = unsafeSummary
	row := mustMapEvent(t, redact.New(), event)
	if len(row.ErrorSummary) > maxSummaryBytes || !utf8.ValidString(row.ErrorSummary) ||
		strings.Contains(row.ErrorSummary, secret) || !strings.HasSuffix(row.ErrorSummary, truncatedMarker) {
		t.Fatalf("request summary was not sanitized: %q", row.ErrorSummary)
	}
	if len(row.AttemptRows) != 1 || strings.Contains(row.AttemptRows[0].ErrorSummary, secret) ||
		!strings.HasSuffix(row.AttemptRows[0].ErrorSummary, truncatedMarker) {
		t.Fatalf("attempt summary was not sanitized: %+v", row.AttemptRows)
	}
}

func TestMapEventBoundsUnattributedAttemptModelsButRejectsOversizedBoundModel(t *testing.T) {
	event := testEvent("model-bounds")
	event.Attempts = append(event.Attempts, telemetry.Attempt{Sequence: 2, UpstreamModel: strings.Repeat("界", 100)})
	row := mustMapEvent(t, redact.New(), event)
	if len(row.AttemptRows[1].UpstreamModel) > maxModelBytes ||
		!utf8.ValidString(row.AttemptRows[1].UpstreamModel) ||
		!strings.HasSuffix(row.AttemptRows[1].UpstreamModel, truncatedMarker) {
		t.Fatalf("unattributed attempt model = %q", row.AttemptRows[1].UpstreamModel)
	}

	overlong := strings.Repeat("x", maxModelBytes+1)
	event.UpstreamModel = overlong
	event.Attempts[0].UpstreamModel = overlong
	event.Usage.Pricing.UpstreamModel = overlong
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("oversized bound model was accepted")
	}
}
