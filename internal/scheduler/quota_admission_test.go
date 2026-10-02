package scheduler

import (
	"errors"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/policy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

// Helper to compile a quota-based exclusion policy for a credential or group.
// Rule: if credential.quota.remaining_ratio (account, 18000s) < 0.10, exclude candidate.
func quotaLowPolicyJSON() []byte {
	return []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-quota-low",
				"name": "Exclude on Low Quota",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.10
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
}

func validQuotaFact(ratio float64, now time.Time) policy.QuotaWindowFact {
	return policy.QuotaWindowFact{
		Scope:         "account",
		WindowSeconds: 18000,
		Ratio:         ratio,
		State:         policy.FactStateMeasured,
		ObservedAt:    now.Add(-time.Minute),
		ResetAt:       now.Add(time.Hour),
	}
}

func TestQuotaAdmission_FreshAccountRatio_MatchAndMismatch(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 1. Quota ratio 0.05 < 0.10 -> 命中规则，排除候选
	lowQuotaCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.05, now),
				},
			},
		},
	}
	iterator := New(snapshot, lowQuotaCreds, query)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted when quota ratio is low, got %v", err)
	}

	// 2. Quota ratio 0.25 >= 0.10 -> 未命中规则，正常准入并选中
	healthyQuotaCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.25, now),
				},
			},
		},
	}
	iteratorHealthy := New(snapshot, healthyQuotaCreds, query)
	selection, err := iteratorHealthy.Next()
	if err != nil {
		t.Fatalf("expected healthy quota credential to be selected, got %v", err)
	}
	if selection.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", selection.CredentialID)
	}
}

func TestQuotaAdmission_MultipleSameSelectorWindows_MinReducer(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 两个同选择器窗口：Ratio 分别为 0.80 与 0.05。min(0.80, 0.05) = 0.05 < 0.10 -> 排除！
	multiWindowCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.80, now),
					validQuotaFact(0.05, now),
				},
			},
		},
	}
	iterator := New(snapshot, multiWindowCreds, query)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected min reducer to pick 0.05 and exclude, got %v", err)
	}

	// 两个同选择器窗口：Ratio 分别为 0.80 与 0.20。min(0.80, 0.20) = 0.20 >= 0.10 -> 不排除！
	multiWindowHealthy := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.80, now),
					validQuotaFact(0.20, now),
				},
			},
		},
	}
	iteratorHealthy := New(snapshot, multiWindowHealthy, query)
	sel, err := iteratorHealthy.Next()
	if err != nil {
		t.Fatalf("expected min reducer 0.20 to not exclude, got %v", err)
	}
	if sel.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", sel.CredentialID)
	}
}

func TestQuotaAdmission_MissingWindow_EvaluatesToUnknown(t *testing.T) {
	// Policy 选择 18000s 窗口
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 凭据仅有 3600s 窗口，缺失 18000s 窗口 -> unknown -> 默认不排除
	fact3600 := validQuotaFact(0.01, now)
	fact3600.WindowSeconds = 3600
	creds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{fact3600},
			},
		},
	}

	iterator := New(snapshot, creds, query)
	sel, err := iterator.Next()
	if err != nil {
		t.Fatalf("missing window must evaluate to unknown and not exclude candidate, got %v", err)
	}
	if sel.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", sel.CredentialID)
	}
}

func TestQuotaAdmission_MixedEffectiveAndUnknownMembers_EvaluatesToUnknown(t *testing.T) {
	// Policy 规则：lt 0.10 => exclude
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 混合有效成员 (0.05 < 0.10) 与未知成员 (FactStateUnknown)。
	// 根据合同：未知状态成员绝不能被有效成员掩盖，整叶必须为 unknown -> 不排除！
	mixedCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.05, now),
					{Scope: "account", WindowSeconds: 18000, Ratio: 0.0, State: policy.FactStateUnknown},
				},
			},
		},
	}

	iterator := New(snapshot, mixedCreds, query)
	sel, err := iterator.Next()
	if err != nil {
		t.Fatalf("mixed unknown member must evaluate to unknown and not exclude, got %v", err)
	}
	if sel.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", sel.CredentialID)
	}
}

func TestQuotaAdmission_ModelScopeMismatch_EvaluatesToUnknown(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 凭据具有 model-specific 窗口 (Scope: "model")，不得冒充 account 窗口
	factModel := validQuotaFact(0.01, now)
	factModel.Scope = "model"
	modelScopeCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{factModel},
			},
		},
	}

	iterator := New(snapshot, modelScopeCreds, query)
	sel, err := iterator.Next()
	if err != nil {
		t.Fatalf("model scope mismatch must evaluate to unknown and not exclude, got %v", err)
	}
	if sel.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", sel.CredentialID)
	}
}

func TestQuotaAdmission_NextInspectChargeReplay_Consistency(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()
	lowWindows := []policy.QuotaWindowFact{
		validQuotaFact(0.05, now),
	}

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1, QuotaWindows: lowWindows},
		},
	}

	// 1. Next(): 凭据 101 因额度不足被排除，返回 ErrExhausted
	iterator := New(snapshot, credentials, query)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() expected ErrExhausted, got %v", err)
	}

	// 2. Inspect(): 凭据 101 必须报告 ReasonPolicyExcluded 且 Available=false
	runtimeViews := []CredentialRuntimeView{
		{
			ID: 101, GroupID: 1, Status: state.CredentialStatusActive, IdentityGeneration: 1,
			QuotaWindows: lowWindows,
		},
	}
	inspection, err := Inspect(snapshot, runtimeViews, query, now)
	if err != nil {
		t.Fatalf("Inspect error = %v", err)
	}
	if inspection.Routable {
		t.Fatal("Inspect expected Routable=false")
	}
	if len(inspection.Groups) == 0 || len(inspection.Groups[0].Credentials) == 0 {
		t.Fatal("Inspect expected group credentials to be populated")
	}
	credInspect := inspection.Groups[0].Credentials[0]
	if credInspect.Available {
		t.Fatal("Inspect expected Available=false")
	}
	if credInspect.Reason != ReasonPolicyExcluded {
		t.Fatalf("Inspect expected ReasonPolicyExcluded, got %q", credInspect.Reason)
	}

	// 3. ChargeReplay(): 凭据 101 必须被拒绝（返回 false）
	modelID := "gpt-4o"
	ref := state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1}
	charged := iterator.ChargeReplay(Selection{
		CredentialID:    101,
		GroupID:         1,
		UpstreamModelID: &modelID,
		Group:           snapshot.Groups[1],
	}, ref)
	if charged {
		t.Fatal("ChargeReplay expected false for policy-excluded credential")
	}

	// 4. 对比：若额度充裕 (0.50)，Next, Inspect, ChargeReplay 全部一致通过
	healthyWindows := []policy.QuotaWindowFact{
		validQuotaFact(0.50, now),
	}
	healthyCreds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1, QuotaWindows: healthyWindows},
		},
	}
	iterHealthy := New(snapshot, healthyCreds, query)
	sel, err := iterHealthy.Next()
	if err != nil || sel.CredentialID != 101 {
		t.Fatalf("Next() expected success for healthy credential, got err=%v, sel=%+v", err, sel)
	}

	inspectionHealthy, err := Inspect(snapshot, []CredentialRuntimeView{
		{
			ID: 101, GroupID: 1, Status: state.CredentialStatusActive, IdentityGeneration: 1,
			QuotaWindows: healthyWindows,
		},
	}, query, now)
	if err != nil || !inspectionHealthy.Routable {
		t.Fatalf("Inspect expected Routable=true, got err=%v, routable=%v", err, inspectionHealthy.Routable)
	}

	chargedHealthy := iterHealthy.ChargeReplay(Selection{
		CredentialID:    101,
		GroupID:         1,
		UpstreamModelID: &modelID,
		Group:           snapshot.Groups[1],
	}, ref)
	if !chargedHealthy {
		t.Fatal("ChargeReplay expected true for healthy credential")
	}
}

func TestQuotaAdmission_RuntimeClone_CredentialIsolation(t *testing.T) {
	// Group 1 有凭据 101 (低额度，0.05) 与 凭据 102 (健康额度，0.50)
	// Policy 作用于 Group 1：lt 0.10 => exclude
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	creds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.05, now),
				},
			},
			{
				ID: 102, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					validQuotaFact(0.50, now),
				},
			},
		},
	}

	iterator := New(snapshot, creds, query)

	// 101 因额度不足被排除，102 必须被正常选中（逐账号隔离）
	selection, err := iterator.Next()
	if err != nil {
		t.Fatalf("expected Credential 102 to be selected, got %v", err)
	}
	if selection.CredentialID != 102 {
		t.Fatalf("expected Credential 102, got %d", selection.CredentialID)
	}

	// 再次调用应当耗尽
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted on second Next(), got %v", err)
	}
}

func TestQuotaAdmission_NextInspectChargeReplay_Consistency_UnknownFacts(t *testing.T) {
	// 验证在遇到 unknown 状态的额度事实时，Next, Inspect, ChargeReplay 三者保持一致且不排除
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	now := time.Now()

	// 各种产生 unknown 的事实形态
	unknownCases := []struct {
		name    string
		windows []policy.QuotaWindowFact
	}{
		{
			name:    "nil quota windows",
			windows: nil,
		},
		{
			name: "missing required window_seconds",
			windows: []policy.QuotaWindowFact{
				{Scope: "account", WindowSeconds: 3600, Ratio: 0.01, State: policy.FactStateMeasured, ResetAt: now.Add(time.Hour)},
			},
		},
		{
			name: "scope mismatch (model instead of account)",
			windows: []policy.QuotaWindowFact{
				{Scope: "model", WindowSeconds: 18000, Ratio: 0.01, State: policy.FactStateMeasured, ResetAt: now.Add(time.Hour)},
			},
		},
		{
			name: "explicit unknown fact state",
			windows: []policy.QuotaWindowFact{
				{Scope: "account", WindowSeconds: 18000, Ratio: 0.0, State: policy.FactStateUnknown},
			},
		},
		{
			name: "mixed effective low and unknown",
			windows: []policy.QuotaWindowFact{
				validQuotaFact(0.02, now),
				{Scope: "account", WindowSeconds: 18000, Ratio: 0.0, State: policy.FactStateUnknown},
			},
		},
	}

	for _, tc := range unknownCases {
		t.Run(tc.name, func(t *testing.T) {
			credentials := fakeCredentialSource{
				keys: []state.CredentialMeta{
					{ID: 101, GroupID: 1, IdentityGeneration: 1, QuotaWindows: tc.windows},
				},
			}

			// 1. Next() 必须成功选中
			iter := New(snapshot, credentials, query)
			sel, err := iter.Next()
			if err != nil {
				t.Fatalf("Next() expected success on unknown quota, got error = %v", err)
			}
			if sel.CredentialID != 101 {
				t.Fatalf("Next() expected Credential 101, got %d", sel.CredentialID)
			}

			// 2. Inspect() 必须报告 Available=true 且 Routable=true
			runtimeViews := []CredentialRuntimeView{
				{
					ID: 101, GroupID: 1, Status: state.CredentialStatusActive, IdentityGeneration: 1,
					QuotaWindows: tc.windows,
				},
			}
			inspection, err := Inspect(snapshot, runtimeViews, query, now)
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if !inspection.Routable {
				t.Fatal("Inspect() expected Routable=true on unknown quota")
			}
			if len(inspection.Groups) == 0 || len(inspection.Groups[0].Credentials) == 0 {
				t.Fatal("Inspect() expected group credentials to be populated")
			}
			if !inspection.Groups[0].Credentials[0].Available {
				t.Fatal("Inspect() expected credential Available=true on unknown quota")
			}

			// 3. ChargeReplay() 必须返回 true
			modelID := "gpt-4o"
			ref := state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1}
			charged := iter.ChargeReplay(Selection{
				CredentialID:    101,
				GroupID:         1,
				UpstreamModelID: &modelID,
				Group:           snapshot.Groups[1],
			}, ref)
			if !charged {
				t.Fatal("ChargeReplay() expected true on unknown quota")
			}
		})
	}
}

func TestQuotaAdmission_ResetExpiration_BeforeAtAfter(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	fixedNow := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	tests := []struct {
		name         string
		resetAt      time.Time
		wantExcluded bool
	}{
		{
			name:         "before reset: valid measured low quota excludes",
			resetAt:      fixedNow.Add(10 * time.Minute),
			wantExcluded: true,
		},
		{
			name:         "at reset: measured expires to unknown, does not exclude",
			resetAt:      fixedNow,
			wantExcluded: false,
		},
		{
			name:         "after reset: measured expired to unknown, does not exclude",
			resetAt:      fixedNow.Add(-10 * time.Minute),
			wantExcluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds := fakeCredentialSource{
				keys: []state.CredentialMeta{
					{
						ID: 101, GroupID: 1, IdentityGeneration: 1,
						QuotaWindows: []policy.QuotaWindowFact{
							{
								Scope:         "account",
								WindowSeconds: 18000,
								Ratio:         0.05,
								State:         policy.FactStateMeasured,
								ObservedAt:    fixedNow.Add(-10 * time.Minute),
								ResetAt:       tt.resetAt,
							},
						},
					},
				},
			}

			iter := newWithClock(snapshot, creds, query, clock)
			_, err := iter.Next()
			if tt.wantExcluded {
				if !errors.Is(err, ErrExhausted) {
					t.Fatalf("expected ErrExhausted before reset, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected candidate not excluded at/after reset, got %v", err)
				}
			}
		})
	}
}

func TestQuotaAdmission_FutureObservedAt_EvaluatesToUnknown(t *testing.T) {
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: quotaLowPolicyJSON()},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	fixedNow := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	// 观测时间处于未来 (ObservedAt > now)，必须判定为 unknown 不排除
	creds := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{
				ID: 101, GroupID: 1, IdentityGeneration: 1,
				QuotaWindows: []policy.QuotaWindowFact{
					{
						Scope:         "account",
						WindowSeconds: 18000,
						Ratio:         0.05,
						State:         policy.FactStateMeasured,
						ObservedAt:    fixedNow.Add(5 * time.Minute),
						ResetAt:       fixedNow.Add(1 * time.Hour),
					},
				},
			},
		},
	}

	iter := newWithClock(snapshot, creds, query, clock)
	sel, err := iter.Next()
	if err != nil {
		t.Fatalf("future observedAt must evaluate to unknown and not exclude, got %v", err)
	}
	if sel.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", sel.CredentialID)
	}
}
