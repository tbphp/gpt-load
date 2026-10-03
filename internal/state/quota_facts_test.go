package state

import (
	"math"
	"testing"
	"time"

	"gpt-load/internal/policy"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestNormalizeQuotaWindows(t *testing.T) {
	windowSec := int64(18000)
	utilization := 0.25
	remaining := 75.0
	limit := 100.0
	observedAt := int64(1000)
	resetAt := int64(5000)

	tests := []struct {
		name      string
		window    providerobservation.QuotaWindow
		wantScope string
		wantWin   int
		wantRatio float64
		wantState policy.FactState
	}{
		{
			name: "valid utilization",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.75,
			wantState: policy.FactStateMeasured,
		},
		{
			name: "valid remaining and limit",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Remaining:     &remaining,
				Limit:         &limit,
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.75,
			wantState: policy.FactStateMeasured,
		},
		{
			name: "state exhausted",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "exhausted",
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateMeasured,
		},
		{
			name: "ambiguous: exhausted state with positive remaining",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "exhausted",
				Remaining:     &remaining,
				Limit:         &limit,
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "ambiguous: utilization and remaining conflict",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization, // remaining = 0.75
				Remaining:     func() *float64 { v := 10.0; return &v }(),
				Limit:         &limit, // remaining = 0.10, diff = 0.65 > 0.01
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "illegal: NaN utilization",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   func() *float64 { v := math.NaN(); return &v }(),
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "illegal: negative limit",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Remaining:     &remaining,
				Limit:         func() *float64 { v := -10.0; return &v }(),
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "missing: limit missing",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Remaining:     &remaining,
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "missing: window seconds missing on account scope",
			window: providerobservation.QuotaWindow{
				Scope:        "account",
				State:        "available",
				Utilization:  &utilization,
				ObservedAtMS: &observedAt,
				ResetAtMS:    &resetAt,
			},
			wantScope: "account",
			wantWin:   0,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "missing: reset_at missing on account scope",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
				ObservedAtMS:  &observedAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "missing: observed_at missing on account scope",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
				ResetAtMS:     &resetAt,
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "invalid: observed_at after reset_at",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
				ObservedAtMS:  func() *int64 { v := int64(6000); return &v }(),
				ResetAtMS:     &resetAt, // 5000 < 6000
			},
			wantScope: "account",
			wantWin:   18000,
			wantRatio: 0.0,
			wantState: policy.FactStateUnknown,
		},
		{
			name: "model scope cannot impersonate account",
			window: providerobservation.QuotaWindow{
				Scope:         "account",
				ModelIDs:      []string{"gpt-5.3-codex-spark"},
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
				ObservedAtMS:  &observedAt,
				ResetAtMS:     &resetAt,
			},
			wantScope: "model",
			wantWin:   18000,
			wantRatio: 0.75,
			wantState: policy.FactStateMeasured,
		},
		{
			name: "non-account scope preserved",
			window: providerobservation.QuotaWindow{
				Scope:         "custom_feature",
				WindowSeconds: &windowSec,
				State:         "available",
				Utilization:   &utilization,
			},
			wantScope: "custom_feature",
			wantWin:   18000,
			wantRatio: 0.75,
			wantState: policy.FactStateMeasured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeQuotaWindowFact(tt.window, 1)
			if got.Scope != tt.wantScope {
				t.Fatalf("Scope = %q, want %q", got.Scope, tt.wantScope)
			}
			if got.WindowSeconds != tt.wantWin {
				t.Fatalf("WindowSeconds = %d, want %d", got.WindowSeconds, tt.wantWin)
			}
			if math.Abs(got.Ratio-tt.wantRatio) > 1e-6 {
				t.Fatalf("Ratio = %v, want %v", got.Ratio, tt.wantRatio)
			}
			if got.State != tt.wantState {
				t.Fatalf("State = %q, want %q", got.State, tt.wantState)
			}
		})
	}
}

func TestCredentialRegistryQuotaFactsIsolationAndIdentityGuard(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 10, Fingerprint: "fp1", EncryptedValue: "cipher1"},
		{ID: 2, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 20, Fingerprint: "fp2", EncryptedValue: "cipher2"},
	})

	resetAt := int64(1800000000000)
	observedAt := int64(1799999000000)
	windowSec := int64(18000)
	utilization := 0.95

	windows1 := []providerobservation.QuotaWindow{
		{
			ID: "primary", Scope: "account", WindowSeconds: &windowSec,
			State: "available", Utilization: &utilization,
			ResetAtMS: &resetAt, ObservedAtMS: &observedAt,
		},
	}

	// 1. Identity generation mismatch must be rejected (old identity write)
	if registry.ApplyQuotaWindows(1, 9, windows1) {
		t.Fatal("ApplyQuotaWindows with stale generation 9 must return false")
	}

	// 2. Matching generation succeeds
	if !registry.ApplyQuotaWindows(1, 10, windows1) {
		t.Fatal("ApplyQuotaWindows with matching generation 10 must return true")
	}

	// 3. Facts on cred 1 must be present, isolated from cred 2
	views := registry.Snapshot()
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	var view1, view2 CredentialRuntimeView
	for _, v := range views {
		if v.ID == 1 {
			view1 = v
		} else if v.ID == 2 {
			view2 = v
		}
	}

	if len(view1.QuotaWindows) != 1 || math.Abs(view1.QuotaWindows[0].Ratio-0.05) > 1e-6 {
		t.Fatalf("view1 QuotaWindows = %#v, want ratio 0.05", view1.QuotaWindows)
	}
	if len(view2.QuotaWindows) != 0 {
		t.Fatalf("view2 QuotaWindows leaked from cred 1: %#v", view2.QuotaWindows)
	}

	// 4. Mutating returned view's slice must not mutate registry entry
	view1.QuotaWindows[0].Ratio = 0.99
	viewsAgain := registry.Snapshot()
	for _, v := range viewsAgain {
		if v.ID == 1 {
			if math.Abs(v.QuotaWindows[0].Ratio-0.05) > 1e-6 {
				t.Fatalf("registry mutated by caller modification: got ratio %v, want 0.05", v.QuotaWindows[0].Ratio)
			}
		}
	}

	// 5. CollectCredentialCandidates deep clone verification
	candidates := registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{})
	var cand1 CredentialMeta
	for _, c := range candidates {
		if c.ID == 1 {
			cand1 = c
		}
	}
	cand1.QuotaWindows[0].Ratio = 0.88
	candidatesAgain := registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{})
	for _, c := range candidatesAgain {
		if c.ID == 1 {
			if math.Abs(c.QuotaWindows[0].Ratio-0.05) > 1e-6 {
				t.Fatalf("registry mutated by candidate caller modification: got ratio %v, want 0.05", c.QuotaWindows[0].Ratio)
			}
		}
	}

	// 6. Applying nil/empty windows clears quota facts
	if !registry.ApplyQuotaWindows(1, 10, nil) {
		t.Fatal("ApplyQuotaWindows(1, 10, nil) failed")
	}
	viewsCleared := registry.Snapshot()
	for _, v := range viewsCleared {
		if v.ID == 1 {
			if len(v.QuotaWindows) != 0 || v.ObservedQuotaRemaining() != nil {
				t.Fatalf("expected cleared quota windows, got %#v", v)
			}
		}
	}
}

func TestCredentialRegistryApplyQuotaWindowsIsolation(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "fp1", EncryptedValue: "cipher1"},
	})

	resetAt := int64(1800000000000)
	observedAt := int64(1799999000000)
	windowSec := int64(18000)
	utilization := 0.20

	inputWindows := []providerobservation.QuotaWindow{
		{
			ID:            "primary",
			Scope:         "account",
			WindowSeconds: &windowSec,
			State:         "available",
			Utilization:   &utilization,
			ResetAtMS:     &resetAt,
			ObservedAtMS:  &observedAt,
		},
	}

	if !registry.ApplyQuotaWindows(1, 1, inputWindows) {
		t.Fatal("ApplyQuotaWindows(1, 1, inputWindows) = false")
	}

	// Caller modifies inputWindows after passing it to ApplyQuotaWindows
	inputWindows[0].Scope = "corrupted"
	newUtil := 0.99
	inputWindows[0].Utilization = &newUtil

	views := registry.Snapshot()
	if len(views) != 1 || len(views[0].QuotaWindows) != 1 {
		t.Fatalf("expected 1 view with 1 quota window, got %#v", views)
	}
	if views[0].QuotaWindows[0].Scope != "account" {
		t.Fatalf("registry scope mutated by caller: got %q, want 'account'", views[0].QuotaWindows[0].Scope)
	}
	if math.Abs(views[0].QuotaWindows[0].Ratio-0.80) > 1e-6 {
		t.Fatalf("registry ratio mutated by caller: got %v, want 0.80", views[0].QuotaWindows[0].Ratio)
	}

	// Calling SetCredentialQuotaObservation with nil clears both health display and quota facts
	if !registry.SetCredentialQuotaObservation(1, nil, time.Time{}) {
		t.Fatal("SetCredentialQuotaObservation(nil) = false")
	}
	clearedViews := registry.Snapshot()
	if len(clearedViews) != 1 {
		t.Fatalf("expected 1 view, got %d", len(clearedViews))
	}
	if clearedViews[0].ObservedQuotaRemaining() != nil {
		t.Fatalf("expected ObservedQuotaRemaining to be nil after clearing, got %v", clearedViews[0].ObservedQuotaRemaining())
	}
	if len(clearedViews[0].QuotaWindows) != 0 {
		t.Fatalf("expected QuotaWindows to be empty after clearing, got %#v", clearedViews[0].QuotaWindows)
	}
}

func TestCredentialRuntimeViewAndMetaClone(t *testing.T) {
	facts := []policy.QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: policy.FactStateMeasured},
	}

	weight := 10
	now := time.Now()
	view := CredentialRuntimeView{
		ID:             1,
		GroupID:        2,
		WeightManual:   &weight,
		ModelCooldowns: map[string]time.Time{"gpt-4o": now},
		QuotaWindows:   facts,
	}

	clonedView := view.Clone()
	clonedView.QuotaWindows[0].Ratio = 0.99
	if view.QuotaWindows[0].Ratio != 0.15 {
		t.Fatalf("mutating cloned view mutated original: got %v, want 0.15", view.QuotaWindows[0].Ratio)
	}

	meta := CredentialMeta{
		ID:             1,
		GroupID:        2,
		WeightManual:   &weight,
		ModelCooldowns: map[string]time.Time{"gpt-4o": now},
		QuotaWindows:   facts,
	}

	clonedMeta := meta.Clone()
	clonedMeta.QuotaWindows[0].Ratio = 0.88
	if meta.QuotaWindows[0].Ratio != 0.15 {
		t.Fatalf("mutating cloned meta mutated original: got %v, want 0.15", meta.QuotaWindows[0].Ratio)
	}
}

func TestNormalizeQuotaWindows_ConflictSameSourceSampleBoundary(t *testing.T) {
	windowSec := int64(18000)
	observedAt := int64(1799999000000)
	resetAt := int64(1800000000000)

	rem1 := 9.9
	rem2 := 10.1
	limit := 100.0

	// 1. 同来源、同真实周期、同 sample/identity 冲突（0.099 与 0.101）
	// 两者均必须变为 FactStateUnknown
	w1 := providerobservation.QuotaWindow{
		ID:            "w1",
		Scope:         "account",
		WindowSeconds: &windowSec,
		SourceID:      "codex",
		State:         "available",
		Remaining:     &rem1,
		Limit:         &limit,
		ObservedAtMS:  &observedAt,
		ResetAtMS:     &resetAt,
	}
	w2 := providerobservation.QuotaWindow{
		ID:            "w2",
		Scope:         "account",
		WindowSeconds: &windowSec,
		SourceID:      "codex",
		State:         "available",
		Remaining:     &rem2,
		Limit:         &limit,
		ObservedAtMS:  &observedAt,
		ResetAtMS:     &resetAt,
	}

	factsConflict := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, w2}, 1)
	if len(factsConflict) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(factsConflict))
	}
	if factsConflict[0].State != policy.FactStateUnknown || factsConflict[1].State != policy.FactStateUnknown {
		t.Fatalf("expected both conflicting facts to be FactStateUnknown, got states %v and %v",
			factsConflict[0].State, factsConflict[1].State)
	}

	// 2. 不同来源、同周期、同样本（不属于同来源歧义）
	w2DiffSource := w2
	w2DiffSource.SourceID = "claude_adapter"
	factsDiffSource := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, w2DiffSource}, 1)
	if len(factsDiffSource) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(factsDiffSource))
	}
	if factsDiffSource[0].State != policy.FactStateMeasured || factsDiffSource[1].State != policy.FactStateMeasured {
		t.Fatalf("expected different source facts to both be FactStateMeasured, got %v and %v",
			factsDiffSource[0].State, factsDiffSource[1].State)
	}

	// 3. 同来源、同周期、同样本但数值相等（重复观测，非矛盾）
	w2Identical := w1
	w2Identical.ID = "w2_dup"
	factsIdentical := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, w2Identical}, 1)
	if len(factsIdentical) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(factsIdentical))
	}
	if factsIdentical[0].State != policy.FactStateMeasured || factsIdentical[1].State != policy.FactStateMeasured {
		t.Fatalf("expected identical duplicate facts to stay FactStateMeasured, got %v and %v",
			factsIdentical[0].State, factsIdentical[1].State)
	}

	// 4. 空来源单窗口（Claude），不可一刀切拒绝
	wClaude := w1
	wClaude.SourceID = ""
	factsClaude := NormalizeQuotaWindows([]providerobservation.QuotaWindow{wClaude}, 1)
	if len(factsClaude) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(factsClaude))
	}
	if factsClaude[0].State != policy.FactStateMeasured {
		t.Fatalf("expected blank source window to be FactStateMeasured, got %v", factsClaude[0].State)
	}
}
