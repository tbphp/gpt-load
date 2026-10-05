package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/policy"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

// Scheduling policy exclusion (exclude_candidate) for the preferred affinity
// candidate must fall back to an alternate target and re-learn affinity.
func TestPolicyRouting_PreferredCandidateExcludedFallsBackToAlternate(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"fallback conversation"}]}`

	// Request 1 learns affinity to sk-one (credential 1).
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})

	in := policyRegressionInputWithTwoCredentials(handler)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope: "credential", GroupID: 1, CredentialID: 1,
		Config: excludeCandidateConfig("block-cred-1"),
	}}
	if _, err := manager.Publish(in); err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}

	// Request 2: preferred credential 1 is denied, scheduler falls back to sk-two.
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})

	// Request 3: affinity was re-learned on sk-two.
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true})
}

// exclude_models removes only the listed request model from a candidate's
// scheduling surface; other models stay routable through that candidate.
func TestPolicyRouting_ExcludeModelsSkipsCandidate(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	engine := newAffinityTestEngine(t, handler)

	in := policyRegressionInputWithTwoCredentials(handler)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope: "credential", GroupID: 1, CredentialID: 1,
		Config: []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"block-model","name":"Block model","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_models","models":["gpt-4o"]}]}]}`),
	}}
	if _, err := manager.Publish(in); err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}

	serveAffinityRequest(t, engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"exclude model"}]}`)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-two"})
}

// A candidate bound by previous_response_id cannot bypass a scheduling exclusion
// through replay or fallback: the request fails instead of escaping to another credential.
func TestPolicyRouting_DeniedBoundCandidateCannotBypassPolicy(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		storedResponse("initial-resp"),
		storedResponse("must-not-be-reached"),
	}}
	handler, engine, _ := newContinuationFixture(t, forwarder)

	// Step 1: initial request binds response "initial-resp" to credential 1.
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial","store":true}`, http.StatusOK)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})

	// Step 2: exclude credential 1 for gpt-4o.
	in := policyRegressionInputWithTwoCredentials(handler)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope: "credential", GroupID: 1, CredentialID: 1,
		Config: excludeCandidateConfig("block-cred-1"),
	}}
	if _, err := handler.manager.Publish(in); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// Step 3: continuation referencing the bound response must not fall back.
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"continue","previous_response_id":"initial-resp"}`, http.StatusServiceUnavailable)
	if len(forwarder.inputs) != 1 {
		t.Fatalf("denied bound candidate escaped to another credential: %d inputs", len(forwarder.inputs))
	}
}

// Publishing a policy that does not change scheduling compatibility retains eligible soft affinity.
func TestPolicyRouting_PolicyOnlySaveRetainsEligibleAffinity(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable affinity conversation"}]}`

	// Request 1 learns affinity to sk-one (credential 1).
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false})

	// Publish a policy-only change: a rule targeting an unrelated model.
	in := policyRegressionInputWithTwoCredentials(handler)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope: "group", GroupID: 1,
		Config: []byte(`{"schema_version":1,"rules":[{"id":"unrelated-rule","name":"Block unrelated model","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"non-existent-model"},"actions":[{"type":"exclude_candidate"}]}]}`),
	}}
	current := manager.Current()
	published, err := manager.Publish(in)
	if err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}
	if published.Revision != current.Revision+1 {
		t.Fatalf("Revision = %d, want %d", published.Revision, current.Revision+1)
	}
	if published.AffinityRevision != current.AffinityRevision {
		t.Fatalf("AffinityRevision = %d, want preserved %d", published.AffinityRevision, current.AffinityRevision)
	}

	// Request 2: affinity to sk-one survives the policy-only publication.
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true})
}

// An incompatible configuration change invalidates cached affinity and re-schedules.
func TestPolicyRouting_IncompatibleConfigInvalidatesAffinity(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"session to invalidate"}]}`

	// Request 1 learns affinity to sk-one.
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false})

	// Publish an incompatible change: group configuration changed.
	in := policyRegressionInputWithTwoCredentials(handler)
	in.Groups[0].Name = "openai-modified"
	current := manager.Current()
	published, err := manager.Publish(in)
	if err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}
	if published.AffinityRevision == current.AffinityRevision {
		t.Fatalf("AffinityRevision should increment on incompatible config, got %d", published.AffinityRevision)
	}

	// Request 2: affinity was invalidated, normal schedule picks the next candidate.
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false})
}

// multiply_price factors apply to the attempt and are recorded in the v7 pricing receipt.
func TestPolicyPricing_MultiplierAppliedToReceipt(t *testing.T) {
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{
			completeScriptedUpstreamResult(UpstreamResult{
				StatusCode:     http.StatusOK,
				RequestWritten: true,
				Body:           []byte(`{"id":"chat-1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`),
				Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
			}),
		},
	}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.priceTables = &mutableGatewayPriceTableProvider{
		table: mustGatewayPriceTable(t, 2_000_000_000, true),
	}
	engine := newAffinityTestEngine(t, handler)

	// Group x2 (suppressed) and credential override x3.
	in := policyRegressionCompileInput(handler)
	in.PolicyBindings = []policy.BindingConfig{
		{
			Scope:   "group",
			GroupID: 1,
			Config:  []byte(`{"schema_version":1,"rules":[{"id":"p-grp","name":"Group Double","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"multiply_price","factor":"2"}]}]}`),
		},
		{
			Scope:        "credential",
			GroupID:      1,
			CredentialID: 1,
			Config:       []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"p-cred","name":"Cred Triple","domain":"pricing","enabled":true,"when":{"fact":"upstream.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"multiply_price","factor":"3"}]}]}`),
		},
	}
	if _, err := manager.Publish(in); err != nil {
		t.Fatal(err)
	}

	serveAffinityRequest(t, engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"test dynamic pricing"}]}`)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(events[0].Usage.Pricing.ReceiptJSON), &receipt); err != nil {
		t.Fatalf("unmarshal receipt error: %v", err)
	}
	if receipt.SchemaVersion != 7 {
		t.Fatalf("receipt schema = %d, want 7", receipt.SchemaVersion)
	}
	if len(receipt.PolicyFactors) != 1 {
		t.Fatalf("expected 1 policy factor (credential override), got %d: %+v", len(receipt.PolicyFactors), receipt.PolicyFactors)
	}
	if receipt.PolicyFactors[0].RuleID != "p-cred" || receipt.PolicyFactors[0].BindingScope != "credential" || receipt.PolicyFactors[0].Factor != "3" {
		t.Fatalf("factor mismatch: %+v", receipt.PolicyFactors[0])
	}
	if receipt.BaseTotalNanoUSD == nil || receipt.TotalNanoUSD != *receipt.BaseTotalNanoUSD*3 {
		t.Fatalf("receipt total = %d, want base*3 (%v)", receipt.TotalNanoUSD, receipt.BaseTotalNanoUSD)
	}
	if err := pricing.ValidateReceipt(receipt); err != nil {
		t.Fatalf("ValidateReceipt failed on emitted receipt: %v", err)
	}
}

func policyRegressionCompileInput(h *Handler) state.CompileInput {
	return state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key",
			ID:             1,
			Name:           "openai",
			ChannelID:      channel.OpenAI,
			Params:         json.RawMessage(`{}`),
			Models:         []state.ModelConfig{{ID: "gpt-4o"}},
			Enabled:        true,
		}},
		Credentials: []state.CredentialConfig{{
			ID:                 1,
			GroupID:            1,
			Status:             state.CredentialStatusActive,
			Version:            1,
			IdentityGeneration: 1,
			Fingerprint:        "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      1,
			Name:    "client",
			KeyHash: h.encryption.Hash("gl-client"),
			Status:  state.AccessKeyStatusActive,
		}},
	}
}

func policyRegressionInputWithTwoCredentials(h *Handler) state.CompileInput {
	in := policyRegressionCompileInput(h)
	in.Credentials = append(in.Credentials, state.CredentialConfig{
		ID: 2, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2",
	})
	return in
}

func excludeCandidateConfig(ruleID string) []byte {
	return []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"` + ruleID + `","name":"Block model","domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"actions":[{"type":"exclude_candidate"}]}]}`)
}
