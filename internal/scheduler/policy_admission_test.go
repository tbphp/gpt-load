package scheduler

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/policy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestPolicyAdmission_EnabledModelExclusion_RequestModel(t *testing.T) {
	groupJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-block-req-gpt4o",
				"name": "Block request gpt-4o",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: groupJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	// 1. Next() must reject because candidate is excluded by policy
	iterator := New(snapshot, credentials, query)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted due to policy exclusion, got %v", err)
	}

	// 2. Inspect() must report policy exclusion reason
	inspection, err := Inspect(snapshot, []CredentialRuntimeView{
		{ID: 101, GroupID: 1, Status: state.CredentialStatusActive, IdentityGeneration: 1},
	}, query, time.Now())
	if err != nil {
		t.Fatalf("Inspect error = %v", err)
	}
	if inspection.Routable {
		t.Fatalf("expected Routable=false due to policy exclusion")
	}
	if len(inspection.Groups) == 0 || len(inspection.Groups[0].Credentials) == 0 {
		t.Fatalf("expected inspection groups and credentials to be populated")
	}
	if inspection.Groups[0].Credentials[0].Reason != ReasonPolicyExcluded {
		t.Fatalf("expected ReasonPolicyExcluded, got %q", inspection.Groups[0].Credentials[0].Reason)
	}
}

func TestPolicyAdmission_EnabledModelExclusion_UpstreamModel(t *testing.T) {
	groupJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-block-upstream",
				"name": "Block provider-gpt-4o",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "provider-gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 2, Config: groupJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	// Group 2 upstream model is "provider-gpt-4o"
	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 201, GroupID: 2, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	iterator := New(snapshot, credentials, query)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted due to upstream model exclusion, got %v", err)
	}
}

func TestPolicyAdmission_DisabledRule_DoesNotExclude(t *testing.T) {
	disabledJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-disabled",
				"name": "Disabled exclusion",
				"domain": "scheduling",
				"enabled": false,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: disabledJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	iterator := New(snapshot, credentials, query)
	selection, err := iterator.Next()
	if err != nil {
		t.Fatalf("expected selection to succeed with disabled rule, got error = %v", err)
	}
	if selection.CredentialID != 101 {
		t.Fatalf("expected Credential 101, got %d", selection.CredentialID)
	}
}

func TestPolicyAdmission_GroupVersusCredentialScope(t *testing.T) {
	credJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-cred-101",
				"name": "Exclude Credential 101",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	// Only Credential 101 has the exclusion policy; Group 1 and Credential 102 have none
	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: credJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = policyView

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
			{ID: 102, GroupID: 1, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	iterator := New(snapshot, credentials, query)

	// 1. First selection must pick Credential 102 because Credential 101 is excluded by its policy
	selection, err := iterator.Next()
	if err != nil {
		t.Fatalf("expected Credential 102 to be selected, got %v", err)
	}
	if selection.CredentialID != 102 {
		t.Fatalf("expected Credential 102, got %d", selection.CredentialID)
	}

	// 2. Second selection must return ErrExhausted (102 already tried, 101 excluded)
	_, err = iterator.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected ErrExhausted on second call, got %v", err)
	}

	// 3. ChargeReplay: Credential 101 must be ineligible due to its credential policy
	modelID := "gpt-4o"
	ref101 := state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1}
	charged := iterator.ChargeReplay(Selection{
		CredentialID:    101,
		GroupID:         1,
		UpstreamModelID: &modelID,
		Group:           snapshot.Groups[1],
	}, ref101)
	if charged {
		t.Fatalf("expected ChargeReplay on excluded Credential 101 to be false")
	}

	// 4. ChargeReplay: Credential 102 is NOT excluded, so it can be charged
	ref102 := state.CredentialRef{ID: 102, GroupID: 1, IdentityGeneration: 1}
	charged = iterator.ChargeReplay(Selection{
		CredentialID:    102,
		GroupID:         1,
		UpstreamModelID: &modelID,
		Group:           snapshot.Groups[1],
	}, ref102)
	if !charged {
		t.Fatalf("expected ChargeReplay on eligible Credential 102 to be true")
	}
}

func TestPolicyAdmission_SameCredential_AlternateTargetPreserved(t *testing.T) {
	// Group 3 has two models: "model-a" (upstream "up-a") and "model-b" (upstream "up-b")
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{
				ConnectionType: "api_key", ID: 3, Name: "three", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{
					{ID: "up-a", Alias: "model-a"},
					{ID: "up-b", Alias: "model-b"},
				},
				Enabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("state.Compile error = %v", err)
	}

	// Credential policy on Credential 301 excludes up-a only
	credJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-cred-block-a",
				"name": "Block up-a",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "up-a"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	policyView, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 3, CredentialID: 301, Config: credJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}
	snapshot.Policies = policyView

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 301, GroupID: 3, IdentityGeneration: 1},
		},
	}

	// 1. Query for model-a: must be excluded!
	reqModelA := "model-a"
	queryA := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModelA,
	}
	iteratorA := New(snapshot, credentials, queryA)
	_, err = iteratorA.Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected model-a to be excluded, got %v", err)
	}

	// 2. Query for model-b: MUST be preserved and selected on the same credential!
	reqModelB := "model-b"
	queryB := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModelB,
	}
	iteratorB := New(snapshot, credentials, queryB)
	selectionB, err := iteratorB.Next()
	if err != nil {
		t.Fatalf("expected model-b to be selected, got error = %v", err)
	}
	if selectionB.CredentialID != 301 {
		t.Fatalf("expected Credential 301, got %d", selectionB.CredentialID)
	}
	if selectionB.UpstreamModelID == nil || *selectionB.UpstreamModelID != "up-b" {
		t.Fatalf("expected upstream model 'up-b', got %v", selectionB.UpstreamModelID)
	}
}

func TestPolicyAdmission_NoPolicyBehavior(t *testing.T) {
	snapshotNil := schedulerSnapshot()
	snapshotNil.Policies = nil

	snapshotEmpty := schedulerSnapshot()
	snapshotEmpty.Policies = policy.NewEmptyRuntimeView()

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	selNil, errNil := New(snapshotNil, credentials, query).Next()
	selEmpty, errEmpty := New(snapshotEmpty, credentials, query).Next()

	if errNil != nil || errEmpty != nil {
		t.Fatalf("expected both to succeed, got errNil=%v, errEmpty=%v", errNil, errEmpty)
	}
	if selNil.CredentialID != selEmpty.CredentialID || selNil.GroupID != selEmpty.GroupID {
		t.Fatalf("expected identical selection, got nil=%+v, empty=%+v", selNil, selEmpty)
	}
}

func TestPolicyAdmission_StaleFailedPublication_RetainsLastValidView(t *testing.T) {
	manager := state.NewManager()

	validRuleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-valid-block",
				"name": "Block gpt-4o",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	invalidRuleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-invalid",
				"name": "Invalid op",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "bad_op", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ConnectionType: "api_key", ID: 1, Name: "one", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "gpt-4o", Alias: "gpt-4o"}}, Enabled: true,
			},
		},
		PolicyBindings: []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: validRuleJSON},
		},
	}

	// 1. Initial valid publish
	snapshot, err := manager.Publish(input)
	if err != nil {
		t.Fatalf("initial manager.Publish error = %v", err)
	}

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
		},
	}
	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  &reqModel,
	}

	// Candidate must be excluded
	_, err = New(manager.Current(), credentials, query).Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected initial view to exclude candidate, got %v", err)
	}

	// 2. Attempt publish with invalid policy
	badInput := input
	badInput.PolicyBindings = []policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: invalidRuleJSON},
	}
	_, err = manager.Publish(badInput)
	if err == nil {
		t.Fatalf("expected manager.Publish with bad policy to fail")
	}

	// 3. Manager.Current() must be unchanged and retain the previous valid snapshot and policy!
	currentSnapshot := manager.Current()
	if currentSnapshot.Revision != snapshot.Revision {
		t.Fatalf("expected snapshot revision %d to be retained, got %d", snapshot.Revision, currentSnapshot.Revision)
	}
	_, err = New(currentSnapshot, credentials, query).Next()
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected retained snapshot to still exclude candidate, got %v", err)
	}
}

func TestPolicyAdmission_PreferredCandidateFairnessProtection(t *testing.T) {
	// Rule excludes Credential 101 for model "gpt-4o"
	credJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "block-101",
				"name": "Block credential 101",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 101, Config: credJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	snapshot := schedulerSnapshot()
	snapshot.Policies = pv

	credentials := fakeCredentialSource{
		keys: []state.CredentialMeta{
			{ID: 101, GroupID: 1, IdentityGeneration: 1},
			{ID: 102, GroupID: 1, IdentityGeneration: 1},
		},
	}

	reqModel := "gpt-4o"
	query := Query{
		ClientProtocol:        protocol.OpenAICompletions,
		Operation:             execution.OperationChatCompletion,
		ExternalModel:         &reqModel,
		PreferredCredentialID: 101, // Credential 101 is preferred, but excluded by policy!
	}

	iterator := New(snapshot, credentials, query)

	// Next() must select alternate candidate 102
	selection, err := iterator.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if selection.CredentialID != 102 {
		t.Fatalf("Next() selected %d, want alternate eligible candidate 102", selection.CredentialID)
	}

	// Verify scheduler progress: Credential 101 must NOT have its fairness ledger advanced!
	iterator.progress.WithLock(func(ledger *state.SchedulingLedger) {
		member101 := ledger.Members[101]
		if member101 != nil && member101.LastSelected > 0 {
			t.Fatalf("expected denied preferred credential 101 not to have fairness advancement, got LastSelected=%d", member101.LastSelected)
		}
	})

	// ChargeReplay on denied candidate 101 must be rejected and not mutate progress
	ref101 := state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1}
	charged := iterator.ChargeReplay(Selection{
		CredentialID: 101, GroupID: 1, UpstreamModelID: &reqModel,
	}, ref101)
	if charged {
		t.Fatalf("ChargeReplay on policy-denied candidate succeeded, want false")
	}
}

func TestPolicyAdmission_PreferredAcrossNativeFirst(t *testing.T) {
	it := New(channelSchedulerSnapshot(t), fakeCredentialSource{keys: []state.CredentialMeta{{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2}}}, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"), PreferredCredentialID: 11})
	sel, err := it.Next()
	if err != nil || sel.CredentialID != 11 {
		t.Fatalf("eligible preferred credential 11 lost across allowed tiers: selected=%d mode=%s err=%v", sel.CredentialID, sel.RouteMode, err)
	}
}

func TestPolicyAdmission_PreferredAcrossStoreFallback(t *testing.T) {
	it := New(responsesStoreSchedulerSnapshot(t, true), fakeCredentialSource{keys: []state.CredentialMeta{{ID: 21, GroupID: 2}, {ID: 31, GroupID: 3}}}, Query{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, ResponsesStorePreference: execution.ResponsesStorePreferencePreferStored, ExternalModel: modelPointer("gpt"), PreferredCredentialID: 31})
	sel, err := it.Next()
	if err != nil || sel.CredentialID != 31 {
		t.Fatalf("eligible preferred credential 31 lost across allowed store fallback: selected=%d downgraded=%v err=%v", sel.CredentialID, sel.ResponsesStoreDowngraded, err)
	}
}

func TestPolicyAdmission_ModelLessReplayChecksPolicyBeforeCharging(t *testing.T) {
	snapshot := schedulerSnapshot()
	source := fakeCredentialSource{keys: []state.CredentialMeta{{ID: 101, GroupID: 1, IdentityGeneration: 1}}}
	model := "gpt-4o"
	it := New(snapshot, source, Query{ClientProtocol: protocol.OpenAICompletions, ExternalModel: &model})
	selection := Selection{CredentialID: 101, GroupID: 1, Group: snapshot.Groups[1]}
	ref := state.CredentialRef{ID: 101, GroupID: 1, IdentityGeneration: 1}
	if !it.ChargeReplay(selection, ref) {
		t.Fatal("no-policy model-less replay regression")
	}
	before := it.progress.CaptureCheckpoint().Sequence
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{{Scope: "group", GroupID: 1, Config: []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"exclude_candidate"}}]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	it.policies = pv
	if it.ChargeReplay(selection, ref) {
		t.Fatal("denied model-less replay admitted")
	}
	if it.progress.CaptureCheckpoint().Sequence != before {
		t.Fatal("denied replay charged progress")
	}
}
