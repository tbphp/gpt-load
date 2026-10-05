package state

import (
	"math"
	"testing"
	"time"

	"gpt-load/internal/policy"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func ptrF64(v float64) *float64 { return &v }

func ptrI64(v int64) *int64 { return &v }

func TestNormalizeQuotaWindows(t *testing.T) {
	windowSec, observedAt, resetAt := int64(18000), int64(1000), int64(5000)
	utilization, remaining, limit := 0.25, 75.0, 100.0

	for _, tt := range []struct {
		name      string
		window    providerobservation.QuotaWindow
		wantScope string
		wantWin   int
		wantRatio float64
		wantState policy.FactState
	}{
		{"valid utilization", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: &utilization, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.75, policy.FactStateMeasured},
		{"valid remaining and limit", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Remaining: &remaining, Limit: &limit, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.75, policy.FactStateMeasured},
		{"state exhausted", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "exhausted", ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateMeasured},
		{"ambiguous: exhausted state with positive remaining", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "exhausted", Remaining: &remaining, Limit: &limit, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"ambiguous: utilization and remaining conflict", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: &utilization, Remaining: ptrF64(10.0), Limit: &limit, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"illegal: NaN utilization", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: ptrF64(math.NaN()), ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"illegal: negative limit", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Remaining: &remaining, Limit: ptrF64(-10.0), ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"missing: limit missing", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Remaining: &remaining, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"missing: window seconds missing on account scope", providerobservation.QuotaWindow{Scope: "account", State: "available", Utilization: &utilization, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "account", 0, 0.0, policy.FactStateUnknown},
		{"missing: reset_at missing on account scope", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: &utilization, ObservedAtMS: &observedAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"missing: observed_at missing on account scope", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: &utilization, ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"invalid: observed_at after reset_at", providerobservation.QuotaWindow{Scope: "account", WindowSeconds: &windowSec, State: "available", Utilization: &utilization, ObservedAtMS: ptrI64(6000), ResetAtMS: &resetAt}, "account", 18000, 0.0, policy.FactStateUnknown},
		{"model scope cannot impersonate account", providerobservation.QuotaWindow{Scope: "account", ModelIDs: []string{"gpt-5.3-codex-spark"}, WindowSeconds: &windowSec, State: "available", Utilization: &utilization, ObservedAtMS: &observedAt, ResetAtMS: &resetAt}, "model", 18000, 0.75, policy.FactStateMeasured},
		{"non-account scope preserved", providerobservation.QuotaWindow{Scope: "custom_feature", WindowSeconds: &windowSec, State: "available", Utilization: &utilization}, "custom_feature", 18000, 0.75, policy.FactStateMeasured},
	} {
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

// quotaWindow 构造一个 account 作用域、可用的额度窗口。
func quotaWindow(windowSec, observedAt, resetAt int64, utilization float64) providerobservation.QuotaWindow {
	return providerobservation.QuotaWindow{
		ID: "primary", Scope: "account", WindowSeconds: &windowSec, State: "available",
		Utilization: &utilization, ResetAtMS: &resetAt, ObservedAtMS: &observedAt,
	}
}

func TestCredentialRegistryQuotaFactsIsolationAndIdentityGuard(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 10, Fingerprint: "fp1", EncryptedValue: "cipher1"},
		{ID: 2, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 20, Fingerprint: "fp2", EncryptedValue: "cipher2"},
	})
	windows := []providerobservation.QuotaWindow{quotaWindow(18000, 1799999000000, 1800000000000, 0.95)}

	// 1. 旧 identity 的写入必须被拒绝。
	if registry.ApplyQuotaWindows(1, 9, windows) {
		t.Fatal("ApplyQuotaWindows with stale generation 9 must return false")
	}
	// 2. identity 匹配时成功。
	if !registry.ApplyQuotaWindows(1, 10, windows) {
		t.Fatal("ApplyQuotaWindows with matching generation 10 must return true")
	}

	viewByID := func(views []CredentialRuntimeView) map[uint]CredentialRuntimeView {
		m := make(map[uint]CredentialRuntimeView, len(views))
		for _, v := range views {
			m[v.ID] = v
		}
		return m
	}

	// 3. 凭据 1 有事实，凭据 2 不受影响。
	views := viewByID(registry.Snapshot())
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	if len(views[1].QuotaWindows) != 1 || math.Abs(views[1].QuotaWindows[0].Ratio-0.05) > 1e-6 {
		t.Fatalf("view1 QuotaWindows = %#v, want ratio 0.05", views[1].QuotaWindows)
	}
	if len(views[2].QuotaWindows) != 0 {
		t.Fatalf("view2 QuotaWindows leaked from cred 1: %#v", views[2].QuotaWindows)
	}

	// 4. 调用方修改返回的 slice 不得影响注册表。
	views[1].QuotaWindows[0].Ratio = 0.99
	if got := viewByID(registry.Snapshot())[1].QuotaWindows[0].Ratio; math.Abs(got-0.05) > 1e-6 {
		t.Fatalf("registry mutated by caller modification: got ratio %v, want 0.05", got)
	}

	// 5. CollectCredentialCandidates 返回深拷贝。
	metaByID := func(list []CredentialMeta) map[uint]CredentialMeta {
		m := make(map[uint]CredentialMeta, len(list))
		for _, c := range list {
			m[c.ID] = c
		}
		return m
	}
	cands := metaByID(registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{}))
	cands[1].QuotaWindows[0].Ratio = 0.88
	if got := metaByID(registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{}))[1].QuotaWindows[0].Ratio; math.Abs(got-0.05) > 1e-6 {
		t.Fatalf("registry mutated by candidate caller modification: got ratio %v, want 0.05", got)
	}

	// 6. nil/空窗口清空额度事实。
	if !registry.ApplyQuotaWindows(1, 10, nil) {
		t.Fatal("ApplyQuotaWindows(1, 10, nil) failed")
	}
	if v := viewByID(registry.Snapshot())[1]; len(v.QuotaWindows) != 0 || v.ObservedQuotaRemaining() != nil {
		t.Fatalf("expected cleared quota windows, got %#v", v)
	}
}

func TestCredentialRuntimeViewAndMetaClone(t *testing.T) {
	facts := []policy.QuotaWindowFact{{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: policy.FactStateMeasured}}
	weight, now := 10, time.Now()
	view := CredentialRuntimeView{
		ID: 1, GroupID: 2, WeightManual: &weight,
		ModelCooldowns: map[string]time.Time{"gpt-4o": now}, QuotaWindows: facts,
	}
	clonedView := view.Clone()
	clonedView.QuotaWindows[0].Ratio = 0.99
	if view.QuotaWindows[0].Ratio != 0.15 {
		t.Fatalf("mutating cloned view mutated original: got %v, want 0.15", view.QuotaWindows[0].Ratio)
	}

	meta := CredentialMeta{
		ID: 1, GroupID: 2, WeightManual: &weight,
		ModelCooldowns: map[string]time.Time{"gpt-4o": now}, QuotaWindows: facts,
	}
	clonedMeta := meta.Clone()
	clonedMeta.QuotaWindows[0].Ratio = 0.88
	if meta.QuotaWindows[0].Ratio != 0.15 {
		t.Fatalf("mutating cloned meta mutated original: got %v, want 0.15", meta.QuotaWindows[0].Ratio)
	}
}

// TestNormalizeQuotaWindows_ConflictSameSourceSampleBoundary 验证同来源同样本的冲突退化为 unknown，
// 而不同来源、重复等值观测与空来源（Claude）仍为 measured。
func TestNormalizeQuotaWindows_ConflictSameSourceSampleBoundary(t *testing.T) {
	windowSec, observedAt, resetAt := int64(18000), int64(1799999000000), int64(1800000000000)
	limit := 100.0
	conflicting := func(id string, remaining float64) providerobservation.QuotaWindow {
		return providerobservation.QuotaWindow{
			ID: id, Scope: "account", WindowSeconds: &windowSec, SourceID: "codex", State: "available",
			Remaining: &remaining, Limit: &limit, ObservedAtMS: &observedAt, ResetAtMS: &resetAt,
		}
	}
	w1, w2 := conflicting("w1", 9.9), conflicting("w2", 10.1)

	// 1. 同来源同周期同样本冲突 (0.099 vs 0.101) -> 两者均 unknown。
	if facts := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, w2}, 1); len(facts) != 2 ||
		facts[0].State != policy.FactStateUnknown || facts[1].State != policy.FactStateUnknown {
		t.Fatalf("expected both conflicting facts unknown, got %#v", facts)
	}

	// 2. 不同来源、同周期同样本 -> 均为 measured。
	wDiffSource := w2
	wDiffSource.SourceID = "claude_adapter"
	if facts := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, wDiffSource}, 1); len(facts) != 2 ||
		facts[0].State != policy.FactStateMeasured || facts[1].State != policy.FactStateMeasured {
		t.Fatalf("expected different-source facts measured, got %#v", facts)
	}

	// 3. 同来源同周期同样本、数值相等（重复观测）-> measured。
	wIdentical := w1
	wIdentical.ID = "w2_dup"
	if facts := NormalizeQuotaWindows([]providerobservation.QuotaWindow{w1, wIdentical}, 1); len(facts) != 2 ||
		facts[0].State != policy.FactStateMeasured || facts[1].State != policy.FactStateMeasured {
		t.Fatalf("expected identical duplicate facts measured, got %#v", facts)
	}

	// 4. 空来源单窗口（Claude）不可一刀切拒绝。
	wClaude := w1
	wClaude.SourceID = ""
	if facts := NormalizeQuotaWindows([]providerobservation.QuotaWindow{wClaude}, 1); len(facts) != 1 ||
		facts[0].State != policy.FactStateMeasured {
		t.Fatalf("expected blank source window measured, got %#v", facts)
	}
}
