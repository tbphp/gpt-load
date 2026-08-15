package requestlog

import (
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gpt-load/internal/execution"
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
	event.Attempts[0].CompletedAt = event.CompletedAt.Add(-90 * time.Second)
	event.Protocol = protocol.OpenAICompletions
	event.Operation = execution.OperationChatCompletion
	event.ClientModel = "client-alias"
	reasoningBudget := int64(8192)
	event.Reasoning = reasoning.Config{
		Mode:         "adaptive",
		Effort:       "high",
		BudgetTokens: &reasoningBudget,
	}
	attemptBudget := int64(4096)
	event.Attempts[0].UpstreamProtocol = protocol.OpenAICompletions
	event.Attempts[0].Reasoning = reasoning.Config{
		Effort: "high", BudgetTokens: &attemptBudget,
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
	event.Usage.CredentialID = 8
	event.Usage.Pricing = telemetry.PricingObservation{
		UpstreamModel:        "upstream-model",
		CostState:            string(pricing.CostStatePriced),
		PricingCompleteness:  string(pricing.CompletenessPartial),
		EstimatedCostNanoUSD: 123456,
	}

	row := mustMapEvent(t, redact.New(), event)
	if row.ID != event.RequestID || row.CompletedAtMS != 1_784_896_200_000 {
		t.Fatalf("identity/completed_at_ms = %q/%d", row.ID, row.CompletedAtMS)
	}
	if row.GroupID != 7 || row.ClientModel != "client-alias" || row.UpstreamModel != "upstream-model" ||
		row.UpstreamReportedModel != "upstream-model" ||
		row.ModelConsistency != string(telemetry.ModelConsistencyMatch) {
		t.Fatalf("persisted attribution/models = %+v", row)
	}
	if row.ReasoningMode != "adaptive" || row.ReasoningEffort != "high" ||
		row.ReasoningBudgetTokens == nil || *row.ReasoningBudgetTokens != reasoningBudget {
		t.Fatalf("persisted reasoning = %+v", row)
	}
	if row.Operation != string(execution.OperationChatCompletion) || len(row.AttemptRows) != 1 ||
		row.AttemptRows[0].UpstreamProtocol != string(protocol.OpenAICompletions) ||
		row.AttemptRows[0].ReasoningEffort != "high" ||
		row.AttemptRows[0].ReasoningBudgetTokens == nil ||
		*row.AttemptRows[0].ReasoningBudgetTokens != attemptBudget {
		t.Fatalf("persisted execution observation = %+v / %+v", row, row.AttemptRows)
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
		row.AttemptRows[0].CredentialID != 8 ||
		row.AttemptRows[0].CompletedAtMS != event.Attempts[0].CompletedAt.UnixMilli() {
		t.Fatalf("attempts = %+v", row.AttemptRows)
	}
}

func TestMapEventRejectsInvalidAttemptUpstreamProtocol(t *testing.T) {
	event := testEvent("invalid-upstream-api")
	event.Attempts[0].UpstreamProtocol = protocol.Protocol("private-sdk-name")
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("mapEvent() accepted an invalid upstream protocol")
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
			event.Usage.CredentialID = 8
			event.Usage.Pricing = telemetry.PricingObservation{
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
		event.Usage.CredentialID = 8
		event.Usage.Pricing = telemetry.PricingObservation{
			UpstreamModel: event.UpstreamModel,
			CostState:     string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessComplete),
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
		{name: "priced without identity", mutate: func(event *telemetry.RequestEvent) { event.Usage.Pricing.UpstreamModel = "" }},
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
	event.Usage.Pricing.UpstreamModel = "model"
	if _, err := mapEvent(redact.New(), event); err == nil {
		t.Fatal("unbound no-model resource with pricing identity was accepted")
	}
	event.Usage.Pricing.UpstreamModel = ""
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
	unsafeSummary := string([]byte{0xff}) + "\r\n\t " + secret + "   " + strings.Repeat("界", 2_000)
	event := testEvent("redact")
	event.ErrorSummary = unsafeSummary
	event.Attempts[0].ErrorSummary = unsafeSummary
	row := mustMapEvent(t, redact.New(), event)
	if len(row.ErrorSummary) <= 1024 || len(row.ErrorSummary) > maxSummaryBytes || !utf8.ValidString(row.ErrorSummary) ||
		strings.Contains(row.ErrorSummary, secret) || !strings.HasSuffix(row.ErrorSummary, truncatedMarker) {
		t.Fatalf("request summary was not sanitized: %q", row.ErrorSummary)
	}
	if len(row.AttemptRows) != 1 || len(row.AttemptRows[0].ErrorSummary) <= 1024 ||
		strings.Contains(row.AttemptRows[0].ErrorSummary, secret) ||
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

func TestMapEventRedactsAndBoundsUpstreamReportedModelAfterFrozenComparison(t *testing.T) {
	event := testEvent("response-model-projection")
	event.UpstreamReportedModel = "sk-obviously-secret-" + strings.Repeat("界", 100)
	event.ModelConsistency = telemetry.ModelConsistencyMismatch

	row := mustMapEvent(t, redact.New(), event)
	if len(row.UpstreamReportedModel) > maxModelBytes ||
		!utf8.ValidString(row.UpstreamReportedModel) ||
		strings.Contains(row.UpstreamReportedModel, "sk-obviously-secret") ||
		!strings.Contains(row.UpstreamReportedModel, redact.Placeholder) {
		t.Fatalf("projected upstream reported model = %q", row.UpstreamReportedModel)
	}
	if row.ModelConsistency != string(telemetry.ModelConsistencyMismatch) {
		t.Fatalf("model consistency = %q, want mismatch", row.ModelConsistency)
	}
}

func TestMapEventPreservesFrozenMismatchWhenRedactionCollapsesModelValues(t *testing.T) {
	event := testEvent("redacted-model-mismatch")
	event.UpstreamModel = "sk-upstream-model-secret-a"
	event.Attempts[0].UpstreamModel = event.UpstreamModel
	event.Usage.Pricing.UpstreamModel = event.UpstreamModel
	event.UpstreamReportedModel = "sk-upstream-model-secret-b"
	event.ModelConsistency = telemetry.ModelConsistencyMismatch

	row := mustMapEvent(t, redact.New(), event)
	if row.UpstreamModel != redact.Placeholder || row.UpstreamReportedModel != redact.Placeholder ||
		row.ModelConsistency != string(telemetry.ModelConsistencyMismatch) {
		t.Fatalf("redacted model mismatch = %q/%q/%q", row.UpstreamModel, row.UpstreamReportedModel, row.ModelConsistency)
	}
}

func TestMapEventRejectsInconsistentModelObservation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*telemetry.RequestEvent)
	}{
		{name: "empty state", mutate: func(event *telemetry.RequestEvent) {
			event.ModelConsistency = ""
		}},
		{name: "unknown with reported model", mutate: func(event *telemetry.RequestEvent) {
			event.ModelConsistency = telemetry.ModelConsistencyUnknown
		}},
		{name: "match with different model", mutate: func(event *telemetry.RequestEvent) {
			event.ModelConsistency = telemetry.ModelConsistencyMatch
			event.UpstreamReportedModel = "different"
		}},
		{name: "mismatch with same model", mutate: func(event *telemetry.RequestEvent) {
			event.ModelConsistency = telemetry.ModelConsistencyMismatch
		}},
		{name: "not applicable with reported model", mutate: func(event *telemetry.RequestEvent) {
			event.ModelConsistency = telemetry.ModelConsistencyNotApplicable
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("invalid-model-observation")
			test.mutate(&event)
			if _, err := mapEvent(redact.New(), event); err == nil {
				t.Fatal("mapEvent() error = nil")
			}
		})
	}
}

func TestMapEventIgnoresModelObservationForUnsuccessfulRequest(t *testing.T) {
	event := testEvent("unsuccessful-model-observation")
	event.Status = telemetry.RequestStatusError
	event.ModelConsistency = telemetry.ModelConsistencyMismatch
	event.UpstreamReportedModel = "different"

	row := mustMapEvent(t, redact.New(), event)
	if row.UpstreamReportedModel != "" ||
		row.ModelConsistency != string(telemetry.ModelConsistencyNotApplicable) {
		t.Fatalf("unsuccessful model observation = %q/%q", row.UpstreamReportedModel, row.ModelConsistency)
	}
}
