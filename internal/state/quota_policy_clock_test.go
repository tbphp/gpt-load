package state

import (
	"testing"
	"time"

	"gpt-load/internal/policy"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestNormalizedQuotaUsesSampleClockAndResetBoundary(t *testing.T) {
	observed := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	reset := observed.Add(time.Hour)
	observedMS, resetMS, seconds, utilization := observed.UnixMilli(), reset.UnixMilli(), int64(18000), 0.95
	window := providerobservation.QuotaWindow{
		ID: "short", SourceID: "test-source", Scope: "account", State: "available",
		ObservedAtMS: &observedMS, ResetAtMS: &resetMS, WindowSeconds: &seconds, Utilization: &utilization,
	}
	config, err := policy.Compile([]byte(`{"schema_version":1,"rules":[{"id":"low","name":"low","domain":"scheduling","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.1},"actions":[{"type":"exclude_candidate"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	facts := NormalizeQuotaWindows([]providerobservation.QuotaWindow{window}, 7)
	for _, test := range []struct {
		name     string
		now      time.Time
		excluded bool
	}{
		{"before observation", observed.Add(-time.Millisecond), false},
		{"at observation", observed, true},
		{"before reset", reset.Add(-time.Nanosecond), true},
		{"at reset", reset, false},
		{"after reset", reset.Add(time.Hour), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := config.EvalScheduling(&policy.EvalContext{Now: test.now, QuotaWindows: facts})
			if result.Excluded != test.excluded {
				t.Fatalf("excluded = %v, want %v", result.Excluded, test.excluded)
			}
		})
	}
	if facts[0].State != policy.FactStateMeasured || !facts[0].ObservedAt.Equal(observed) ||
		facts[0].SourceID != "test-source" || facts[0].IdentityGeneration != 7 {
		t.Fatalf("evaluation changed sample metadata: %#v", facts[0])
	}
	legacy := window
	legacy.ObservedAtMS = nil
	mixed := NormalizeQuotaWindows([]providerobservation.QuotaWindow{window, legacy}, 7)
	if config.EvalScheduling(&policy.EvalContext{Now: observed, QuotaWindows: mixed}).Excluded {
		t.Fatal("a known low sample masked a matching member without sample time")
	}
	orConfig, err := policy.Compile([]byte(`{"schema_version":1,"rules":[{"id":"either","name":"either","domain":"scheduling","enabled":true,"when":{"any":[{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.1},{"fact":"request.model","op":"eq","value":"Astra"}]},"actions":[{"type":"exclude_candidate"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !orConfig.EvalScheduling(&policy.EvalContext{
		Now: reset, QuotaWindows: facts,
		RequestModel: policy.StringFact{Value: "Astra", State: policy.FactStateMeasured},
	}).Excluded {
		t.Fatal("expired quota must not suppress a definitely true OR branch")
	}
}
