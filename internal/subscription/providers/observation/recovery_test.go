package observation

import (
	"math"
	"testing"
)

func recoveryWindow(id, source string, period int64, used float64) QuotaWindow {
	return QuotaWindow{ID: id, SourceID: source, Scope: "account", Unit: "percent", State: "available", WindowSeconds: &period, Used: &used}
}

func TestQuotaRecoveredMatchesWindowsAndThreshold(t *testing.T) {
	weekly := recoveryWindow("primary", "codex", 604800, 80)
	for _, test := range []struct {
		name              string
		previous, current []QuotaWindow
		want              bool
	}{
		{"exactly five points", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("secondary", "codex", 604800, 75)}, true},
		{"below threshold", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 604800, 75.01)}, false},
		{"increase", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 604800, 81)}, false},
		{"period changed", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 18000, 0)}, false},
		{"source changed", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "spark", 604800, 0)}, false},
		{"first observation", nil, []QuotaWindow{weekly}, false},
		{"ambiguous old windows", []QuotaWindow{weekly, weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 604800, 0)}, false},
		{"ambiguous new windows", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 604800, 0), weekly}, false},
		{"stable ID fallback", []QuotaWindow{recoveryWindow("five_hour", "", 18000, 99)}, []QuotaWindow{recoveryWindow("five_hour", "", 18000, 94)}, true},
		{"different fallback ID", []QuotaWindow{recoveryWindow("five_hour", "", 18000, 99)}, []QuotaWindow{recoveryWindow("seven_day", "", 18000, 0)}, false},
		{"invalid number", []QuotaWindow{weekly}, []QuotaWindow{recoveryWindow("primary", "codex", 604800, math.NaN())}, false},
		{"reordered windows", []QuotaWindow{weekly, recoveryWindow("secondary", "codex", 18000, 0)}, []QuotaWindow{recoveryWindow("primary", "codex", 18000, 0), recoveryWindow("secondary", "codex", 604800, 75)}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := QuotaRecovered(test.previous, test.current); got != test.want {
				t.Fatalf("QuotaRecovered = %t, want %t", got, test.want)
			}
		})
	}
}

func TestQuotaRecoveredRequiresFreshNumericEvidence(t *testing.T) {
	old := recoveryWindow("primary", "codex", 604800, 80)
	for _, state := range []string{"unknown", "exhausted"} {
		next := recoveryWindow("primary", "codex", 604800, 0)
		next.State = state
		if QuotaRecovered([]QuotaWindow{old}, []QuotaWindow{next}) {
			t.Fatalf("state %s caused recovery", state)
		}
	}
	next := old
	next.Used = nil
	if QuotaRecovered([]QuotaWindow{old}, []QuotaWindow{next}) {
		t.Fatal("metadata-only observation caused recovery")
	}
	u := 0.75
	next.Utilization = &u
	if !QuotaRecovered([]QuotaWindow{old}, []QuotaWindow{next}) {
		t.Fatal("valid utilization did not cause recovery")
	}
}
