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

// quotaLowPolicyJSON compiles a quota-based exclusion policy with the given group_policy mode.
// 凭据作用域需要 override 才会独立于分组规则生效；分组作用域保持 inherit。
// Rule: if credential.quota.remaining_ratio (account, 18000s) < 0.10, exclude candidate.
func quotaLowPolicyJSON(groupPolicy policy.GroupPolicyMode) []byte {
	return []byte(`{
		"schema_version": 1,
		"group_policy": "` + string(groupPolicy) + `",
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

func quotaFact(
	scope string,
	windowSeconds int,
	ratio float64,
	factState policy.FactState,
	now time.Time,
) policy.QuotaWindowFact {
	return policy.QuotaWindowFact{
		Scope:         scope,
		WindowSeconds: windowSeconds,
		Ratio:         ratio,
		State:         factState,
		ObservedAt:    now.Add(-time.Minute),
		ResetAt:       now.Add(time.Hour),
	}
}

func validQuotaFact(ratio float64, now time.Time) policy.QuotaWindowFact {
	return quotaFact("account", 18000, ratio, policy.FactStateMeasured, now)
}

func withReset(fact policy.QuotaWindowFact, resetAt time.Time) policy.QuotaWindowFact {
	fact.ResetAt = resetAt
	return fact
}

// quotaRuntime compiles the low-quota policy at the requested binding scope over group 1.
// 凭据作用域显式 override，否则默认 inherit 会忽略本地规则。
func quotaRuntime(t *testing.T, scope string, credentialID uint) *state.ConfigSnapshot {
	t.Helper()
	groupPolicy := policy.GroupPolicyInherit
	if scope == "credential" {
		groupPolicy = policy.GroupPolicyOverride
	}
	binding := policy.BindingConfig{Scope: scope, GroupID: 1, Config: quotaLowPolicyJSON(groupPolicy)}
	if scope == "credential" {
		binding.CredentialID = credentialID
	}
	view, err := policy.CompileRuntimeView([]policy.BindingConfig{binding})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}
	snapshot := schedulerSnapshot()
	snapshot.Policies = view
	return snapshot
}

func quotaQuery() Query {
	model := "gpt-4o"
	return Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &model,
	}
}

func quotaSource(id uint, windows []policy.QuotaWindowFact) fakeCredentialSource {
	return fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: id, GroupID: 1, IdentityGeneration: 1, QuotaWindows: windows},
	}}
}

func TestQuotaAdmission_NextInspectChargeReplay_Consistency(t *testing.T) {
	snapshot := quotaRuntime(t, "group", 0)
	query := quotaQuery()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	futureObserved := validQuotaFact(0.05, now)
	futureObserved.ObservedAt = now.Add(5 * time.Minute)

	tests := []struct {
		name     string
		windows  []policy.QuotaWindowFact
		excluded bool
	}{
		{"measured low quota excludes", []policy.QuotaWindowFact{validQuotaFact(0.05, now)}, true},
		{"measured healthy quota admits", []policy.QuotaWindowFact{validQuotaFact(0.25, now)}, false},
		{
			"min reducer picks lowest and excludes",
			[]policy.QuotaWindowFact{validQuotaFact(0.80, now), validQuotaFact(0.05, now)}, true,
		},
		{
			"min reducer stays healthy and admits",
			[]policy.QuotaWindowFact{validQuotaFact(0.80, now), validQuotaFact(0.20, now)}, false,
		},
		{
			"missing required window is unknown",
			[]policy.QuotaWindowFact{quotaFact("account", 3600, 0.01, policy.FactStateMeasured, now)}, false,
		},
		{
			"model scope mismatch is unknown",
			[]policy.QuotaWindowFact{quotaFact("model", 18000, 0.01, policy.FactStateMeasured, now)}, false,
		},
		{
			"mixed effective and unknown is unknown",
			[]policy.QuotaWindowFact{
				validQuotaFact(0.05, now),
				quotaFact("account", 18000, 0, policy.FactStateUnknown, now),
			}, false,
		},
		{
			"explicit unknown fact is unknown",
			[]policy.QuotaWindowFact{quotaFact("account", 18000, 0, policy.FactStateUnknown, now)}, false,
		},
		{"nil windows is unknown", nil, false},
		{
			"observed before reset excludes",
			[]policy.QuotaWindowFact{withReset(validQuotaFact(0.05, now), now.Add(10*time.Minute))}, true,
		},
		{
			"reset boundary is unknown",
			[]policy.QuotaWindowFact{withReset(validQuotaFact(0.05, now), now)}, false,
		},
		{
			"past reset is unknown",
			[]policy.QuotaWindowFact{withReset(validQuotaFact(0.05, now), now.Add(-10*time.Minute))}, false,
		},
		{"future observed time is unknown", []policy.QuotaWindowFact{futureObserved}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iterator := newWithClock(snapshot, quotaSource(101, tc.windows), query, clock)
			selection, nextErr := iterator.Next()

			// Next, Inspect and ChargeReplay must all agree on the same policy outcome.
			inspection, err := Inspect(snapshot, []CredentialRuntimeView{{
				ID: 101, GroupID: 1, Status: state.CredentialStatusActive, IdentityGeneration: 1,
				QuotaWindows: tc.windows,
			}}, query, now)
			if err != nil {
				t.Fatalf("Inspect error = %v", err)
			}
			if len(inspection.Groups) == 0 || len(inspection.Groups[0].Credentials) == 0 {
				t.Fatal("Inspect expected group credentials to be populated")
			}
			cred := inspection.Groups[0].Credentials[0]

			modelID := "gpt-4o"
			charged := iterator.ChargeReplay(Selection{
				CredentialID: 101, GroupID: 1, UpstreamModelID: &modelID, Group: snapshot.Groups[1],
			}, state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1})

			if tc.excluded {
				if !errors.Is(nextErr, ErrExhausted) {
					t.Fatalf("Next() error = %v, want ErrExhausted", nextErr)
				}
				if inspection.Routable || cred.Available || cred.Reason != ReasonPolicyExcluded {
					t.Fatalf("Inspect = routable:%v available:%v reason:%q, want policy-excluded",
						inspection.Routable, cred.Available, cred.Reason)
				}
				if charged {
					t.Fatal("ChargeReplay() = true for policy-excluded credential, want false")
				}
				return
			}
			if nextErr != nil || selection.CredentialID != 101 {
				t.Fatalf("Next() = %+v, %v; want Credential 101", selection, nextErr)
			}
			if !inspection.Routable || !cred.Available {
				t.Fatalf("Inspect = routable:%v available:%v, want admitted", inspection.Routable, cred.Available)
			}
			if !charged {
				t.Fatal("ChargeReplay() = false for admitted credential, want true")
			}
		})
	}
}

func TestQuotaAdmission_CredentialScopeIsolation(t *testing.T) {
	// Credential-scoped policy excludes 101 (low quota) but not 102 (healthy).
	snapshot := quotaRuntime(t, "credential", 101)
	query := quotaQuery()
	now := time.Now()

	iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 101, GroupID: 1, IdentityGeneration: 1, QuotaWindows: []policy.QuotaWindowFact{validQuotaFact(0.05, now)}},
		{ID: 102, GroupID: 1, IdentityGeneration: 1, QuotaWindows: []policy.QuotaWindowFact{validQuotaFact(0.50, now)}},
	}}, query)

	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 102 {
		t.Fatalf("Next() = %+v, %v; want Credential 102", selection, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second Next() error = %v, want ErrExhausted", err)
	}

	modelID := "gpt-4o"
	charge := func(id uint) bool {
		return iterator.ChargeReplay(Selection{
			CredentialID: id, GroupID: 1, UpstreamModelID: &modelID, Group: snapshot.Groups[1],
		}, state.CredentialRef{ID: id, GroupID: 1, IdentityGeneration: 1})
	}
	if charge(101) {
		t.Fatal("ChargeReplay() = true for policy-excluded Credential 101, want false")
	}
	if !charge(102) {
		t.Fatal("ChargeReplay() = false for eligible Credential 102, want true")
	}
}
